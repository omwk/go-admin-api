package gateway

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"time"

	"github.com/oklog/ulid/v2"
	kernelcrypto "github.com/omwk/go-admin-api/internal/kernel/crypto"
	"github.com/omwk/go-admin-api/internal/kernel/errs"
)

// AuthenticationWindow bounds clock skew in either direction.
const AuthenticationWindow = 5 * time.Minute

// NonceTTL covers the whole validity interval of a future-dated request.
const NonceTTL = 2 * AuthenticationWindow

// MaxBodyBytes bounds the request bytes hashed by the open API.
const MaxBodyBytes = 1 << 20

// Authenticator verifies application credentials independently from human sessions.
type Authenticator struct {
	store  AuthStore
	nonces Nonces
	sealer kernelcrypto.Sealer
	source func(*http.Request) string
	now    func() time.Time
}

// NewAuthenticator requires current-state, nonce and decryption dependencies.
func NewAuthenticator(store AuthStore, nonces Nonces, sealer kernelcrypto.Sealer, source func(*http.Request) string, now func() time.Time) (*Authenticator, error) {
	if store == nil || nonces == nil || sealer == nil || source == nil || now == nil {
		return nil, errors.New("gateway authentication dependencies are required")
	}
	return &Authenticator{store: store, nonces: nonces, sealer: sealer, source: source, now: now}, nil
}

// Authenticate reserves a nonce before HMAC and rechecks primary state before returning.
func (a *Authenticator) Authenticate(ctx context.Context, r *http.Request) (Principal, error) {
	appID, timestamp, nonce, signature, err := authenticationHeaders(r)
	if err != nil {
		return Principal{}, err
	}
	client, err := a.store.FindClient(ctx, appID)
	if err != nil {
		return Principal{}, err
	}
	if client.Status != "active" {
		return Principal{}, authenticationFailure(80101)
	}
	now := a.now().UTC()
	seconds, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil || strconv.FormatInt(seconds, 10) != timestamp || seconds < now.Unix()-300 || seconds > now.Unix()+300 {
		return Principal{}, authenticationFailure(80103)
	}
	fresh, err := a.nonces.Claim(ctx, appID, nonce, NonceTTL)
	if err != nil {
		return Principal{}, Unavailable()
	}
	if !fresh {
		return Principal{}, authenticationFailure(80103)
	}
	body, err := boundedBody(r)
	if err != nil {
		return Principal{}, err
	}
	canonical, err := CanonicalRequest(r, timestamp, nonce, body)
	if err != nil {
		return Principal{}, err
	}
	principal, err := a.verify(ctx, client, canonical, signature, now)
	if err != nil {
		return Principal{}, err
	}
	if !allowedIP(client.IPWhitelist, a.source(r)) {
		return Principal{}, errs.New(80105, 403, "当前来源未获授权")
	}
	if err := a.store.Touch(ctx, principal, a.now().UTC()); err != nil {
		return Principal{}, err
	}
	return principal, nil
}

func (a *Authenticator) verify(ctx context.Context, client Client, canonical string, signature []byte, now time.Time) (Principal, error) {
	keys, err := a.store.Secrets(ctx, client.TenantID, client.ID)
	if err != nil {
		return Principal{}, err
	}
	if len(keys) > 2 {
		return Principal{}, Unavailable()
	}
	var matched int64
	for _, key := range keys {
		if key.Status != "active" || key.ExpiresAt != nil && !now.Before(*key.ExpiresAt) {
			continue
		}
		if key.TenantID != client.TenantID || key.ClientID != client.ID {
			return Principal{}, Unavailable()
		}
		plain, openErr := a.sealer.Open(key.CipherText, key.Nonce, secretAAD(key), key.KeyVersion)
		if openErr != nil {
			return Principal{}, openErr
		}
		if matchesSignature(plain, canonical, signature) {
			matched = key.ID
		}
		clear(plain)
	}
	if matched == 0 {
		return Principal{}, authenticationFailure(80102)
	}
	return Principal{TenantID: client.TenantID, ClientID: client.ID, OwnerUserID: client.OwnerUserID, SecretID: matched, AppID: client.AppID}, nil
}

func authenticationHeaders(r *http.Request) (string, string, string, []byte, error) {
	if r == nil {
		return "", "", "", nil, authenticationFailure(80101)
	}
	for _, name := range []string{"X-App-Id", "X-Timestamp", "X-Nonce", "X-Signature"} {
		if len(r.Header.Values(name)) != 1 || r.Header.Get(name) == "" {
			return "", "", "", nil, authenticationFailure(80101)
		}
	}
	appID, timestamp, nonce := r.Header.Get("X-App-Id"), r.Header.Get("X-Timestamp"), r.Header.Get("X-Nonce")
	if _, err := ulid.ParseStrict(appID); err != nil || len(timestamp) > 19 || len(nonce) != 32 {
		return "", "", "", nil, authenticationFailure(80101)
	}
	for _, c := range nonce {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-') {
			return "", "", "", nil, authenticationFailure(80101)
		}
	}
	signature, ok := decodeSignature(r.Header.Get("X-Signature"))
	if !ok {
		return "", "", "", nil, authenticationFailure(80102)
	}
	return appID, timestamp, nonce, signature, nil
}

func boundedBody(r *http.Request) ([]byte, error) {
	if r.Body == nil {
		return nil, nil
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, MaxBodyBytes+1))
	if err != nil {
		return nil, Invalid()
	}
	if len(body) > MaxBodyBytes {
		return nil, errs.New(10001, 413, "请求体超过限制")
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	return body, nil
}

func allowedIP(whitelist []string, source string) bool {
	if len(whitelist) == 0 {
		return true
	}
	ip, err := netip.ParseAddr(source)
	if err != nil {
		return false
	}
	for _, value := range whitelist {
		candidate, err := netip.ParseAddr(strings.TrimSuffix(strings.TrimSuffix(value, "/32"), "/128"))
		if err == nil && candidate.Unmap() == ip.Unmap() {
			return true
		}
	}
	return false
}
