package gateway

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/oklog/ulid/v2"
	kernelcrypto "github.com/omwk/go-admin-api/internal/kernel/crypto"
	"github.com/omwk/go-admin-api/internal/kernel/errs"
	"github.com/stretchr/testify/require"
)

type authTestStore struct {
	client Client
	keys   []Secret
	err    error
	touch  func() error
}

func (s *authTestStore) FindClient(_ context.Context, app string) (Client, error) {
	if s.err != nil {
		return Client{}, s.err
	}
	if s.client.AppID != app {
		return Client{}, authenticationFailure(80101)
	}
	return s.client, nil
}
func (s *authTestStore) Secrets(context.Context, int64, int64) ([]Secret, error) {
	return s.keys, s.err
}
func (s *authTestStore) Touch(context.Context, Principal, time.Time) error {
	if s.touch != nil {
		return s.touch()
	}
	return s.err
}

type testNonces struct {
	seen map[string]bool
	err  error
}

func (n *testNonces) Claim(_ context.Context, app, nonce string, ttl time.Duration) (bool, error) {
	if n.err != nil {
		return false, n.err
	}
	if ttl != 600*time.Second {
		return false, errors.New("incorrect replay interval")
	}
	key := app + ":" + nonce
	fresh := !n.seen[key]
	n.seen[key] = true
	return fresh, nil
}

type authFixture struct {
	auth   *Authenticator
	store  *authTestStore
	nonces *testNonces
	sealer kernelcrypto.Sealer
	now    time.Time
	text   string
}

func randomTestBytes(t *testing.T, size int) []byte {
	t.Helper()
	raw := make([]byte, size)
	_, err := rand.Read(raw)
	require.NoError(t, err)
	t.Cleanup(func() { clear(raw) })
	return raw
}

func newAuthFixture(t *testing.T) *authFixture {
	t.Helper()
	sealer, err := kernelcrypto.NewKeyring(map[int][]byte{1: randomTestBytes(t, 32)}, 1)
	require.NoError(t, err)
	f := &authFixture{now: time.Unix(1_800_000_000, 0).UTC(), sealer: sealer}
	f.store = &authTestStore{client: Client{ID: 1, TenantID: 1, OwnerUserID: 2, AppID: ulid.Make().String(), Status: "active"}}
	f.nonces = &testNonces{seen: map[string]bool{}}
	f.text = f.key(t, 1)
	f.auth, err = NewAuthenticator(f.store, f.nonces, sealer, func(*http.Request) string { return "192.0.2.1" }, func() time.Time { return f.now })
	require.NoError(t, err)
	return f
}

func (f *authFixture) key(t *testing.T, id int64) string {
	t.Helper()
	text := base64.StdEncoding.EncodeToString(randomTestBytes(t, 32))
	key := Secret{ID: id, TenantID: 1, ClientID: 1, Version: 1, Status: "active"}
	var err error
	key.CipherText, key.Nonce, key.KeyVersion, err = f.sealer.Seal([]byte(text), secretAAD(key))
	require.NoError(t, err)
	f.store.keys = append(f.store.keys, key)
	return text
}

func (f *authFixture) request(t *testing.T, key string, offset int64) *http.Request {
	t.Helper()
	r := httptest.NewRequest("GET", "/open/v1/orders/example/status?a=1", nil)
	r.Header.Set("X-App-Id", f.store.client.AppID)
	r.Header.Set("X-Timestamp", strconv.FormatInt(f.now.Unix()+offset, 10))
	r.Header.Set("X-Nonce", hex.EncodeToString(randomTestBytes(t, 16)))
	// Independent six-field client construction uses the issued base64 text bytes.
	canonical := strings.Join([]string{"GET", "/open/v1/orders/example/status", "a=1", r.Header.Get("X-Timestamp"), r.Header.Get("X-Nonce"), hex.EncodeToString(sha256.New().Sum(nil))}, "\n")
	mac := hmac.New(sha256.New, []byte(key))
	mac.Write([]byte(canonical))
	r.Header.Set("X-Signature", base64.StdEncoding.EncodeToString(mac.Sum(nil)))
	return r
}

func TestHMACAuthenticationMatrix(t *testing.T) {
	// REQ-GW-003/009: authentication failures preserve one public message.
	for _, tc := range []struct {
		name string
		code int
		edit func(*authFixture, *http.Request)
	}{
		{"valid", 0, func(*authFixture, *http.Request) {}},
		{"wrong signature", 80102, func(_ *authFixture, r *http.Request) {
			r.Header.Set("X-Signature", base64.StdEncoding.EncodeToString(make([]byte, 32)))
		}},
		{"expired timestamp", 80103, func(f *authFixture, r *http.Request) {
			r.Header.Set("X-Timestamp", strconv.FormatInt(f.now.Unix()-301, 10))
		}},
		{"future timestamp", 80103, func(f *authFixture, r *http.Request) {
			r.Header.Set("X-Timestamp", strconv.FormatInt(f.now.Unix()+301, 10))
		}},
		{"suspended", 80101, func(f *authFixture, _ *http.Request) { f.store.client.Status = "suspended" }},
		{"revoked key", 80102, func(f *authFixture, _ *http.Request) { f.store.keys[0].Status = "revoked" }},
		{"key at expiry", 80102, func(f *authFixture, _ *http.Request) { f.store.keys[0].ExpiresAt = &f.now }},
		{"missing headers", 80101, func(_ *authFixture, r *http.Request) { r.Header.Del("X-App-Id") }},
		{"duplicate header", 80101, func(_ *authFixture, r *http.Request) { r.Header.Add("X-Nonce", r.Header.Get("X-Nonce")) }},
		{"method changed", 80102, func(_ *authFixture, r *http.Request) { r.Method = "HEAD" }},
		{"path changed", 80102, func(_ *authFixture, r *http.Request) { r.URL.Path += "-changed" }},
		{"query changed", 80102, func(_ *authFixture, r *http.Request) { r.URL.RawQuery = "a=2" }},
		{"body changed", 80102, func(_ *authFixture, r *http.Request) { r.Body = newTestBody("changed") }},
		{"nonce changed", 80102, func(_ *authFixture, r *http.Request) { r.Header.Set("X-Nonce", strings.Repeat("b", 32)) }},
		{"timestamp changed", 80102, func(f *authFixture, r *http.Request) {
			r.Header.Set("X-Timestamp", strconv.FormatInt(f.now.Unix()+1, 10))
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newAuthFixture(t)
			r := f.request(t, f.text, 0)
			tc.edit(f, r)
			p, err := f.auth.Authenticate(context.Background(), r)
			if tc.code == 0 {
				require.NoError(t, err)
				require.EqualValues(t, 1, p.SecretID)
			} else {
				require.Error(t, err)
				app := errs.From(PublicError(err))
				require.Equal(t, tc.code, app.Code)
				require.Equal(t, 401, app.HTTPStatus)
				require.Equal(t, "应用凭证不可用", app.Message)
			}
		})
	}
}

func TestHMACReplayAndRotation(t *testing.T) {
	f := newAuthFixture(t)
	other := f.key(t, 2)
	first := f.request(t, f.text, -300)
	_, err := f.auth.Authenticate(context.Background(), first)
	require.NoError(t, err)
	_, err = f.auth.Authenticate(context.Background(), first)
	require.Equal(t, 80103, errs.From(err).Code)
	_, err = f.auth.Authenticate(context.Background(), f.request(t, other, 300))
	require.NoError(t, err)
	// A failed HMAC still consumes its nonce across both keys.
	failed := f.request(t, other, 0)
	failed.Header.Set("X-Signature", base64.StdEncoding.EncodeToString(make([]byte, 32)))
	_, err = f.auth.Authenticate(context.Background(), failed)
	require.Equal(t, 80102, errs.From(err).Code)
	_, err = f.auth.Authenticate(context.Background(), failed)
	require.Equal(t, 80103, errs.From(err).Code)
}

func TestHMACDependencyAndAADFailures(t *testing.T) {
	for _, dependency := range []string{"redis", "primary", "admission", "aad"} {
		t.Run(dependency, func(t *testing.T) {
			f := newAuthFixture(t)
			r := f.request(t, f.text, 0)
			switch dependency {
			case "redis":
				f.nonces.err = errors.New("injected dependency failure")
			case "primary":
				f.store.err = Unavailable()
			case "admission":
				f.store.touch = func() error { return authenticationFailure(80102) }
			case "aad":
				f.store.keys[0].ID++
			}
			_, err := f.auth.Authenticate(context.Background(), r)
			require.Error(t, err)
			app := errs.From(PublicError(err))
			if dependency == "aad" {
				require.Equal(t, 500, app.HTTPStatus)
			} else if dependency == "admission" {
				require.Equal(t, 401, app.HTTPStatus)
			} else {
				require.Equal(t, 503, app.HTTPStatus)
			}
			require.Empty(t, app.Details)
		})
	}
}

type testBody struct{ *strings.Reader }

func (testBody) Close() error           { return nil }
func newTestBody(value string) testBody { return testBody{strings.NewReader(value)} }
