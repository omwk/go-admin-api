package shopee

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/omwk/go-admin-api/internal/modules/connector"
	"github.com/omwk/go-admin-api/internal/modules/ingestion"
	"github.com/stretchr/testify/require"
)

func testCredential(t *testing.T) connector.Credential {
	t.Helper()
	values := map[string]json.RawMessage{}
	for _, key := range []string{"partner_id", "partner_key", "access_token", "shop_id"} {
		secret := make([]byte, 24)
		_, err := rand.Read(secret)
		require.NoError(t, err)
		encoded, err := json.Marshal(hex.EncodeToString(secret))
		require.NoError(t, err)
		values[key] = encoded
	}
	return connector.Credential{Kind: "oauth", Values: values}
}

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile("testdata/" + name + ".json")
	require.NoError(t, err)
	return data
}

func assertSignature(t *testing.T, r *http.Request, c connector.Credential) {
	t.Helper()
	cred, err := decodeCredentials(c)
	require.NoError(t, err)
	query := r.URL.Query()
	require.Equal(t, cred.partner, query.Get("partner_id"))
	require.Equal(t, cred.shop, query.Get("shop_id"))
	require.Equal(t, cred.token, query.Get("access_token"))
	mac := hmac.New(sha256.New, []byte(cred.key))
	_, err = mac.Write([]byte(cred.partner + r.URL.Path + query.Get("timestamp") + cred.token + cred.shop))
	require.NoError(t, err)
	require.Equal(t, hex.EncodeToString(mac.Sum(nil)), query.Get("sign"))
}

func TestShopeeFixtures(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		kind   connector.ErrKind
		reason string
	}{
		{"normal", 200, "", ""}, {"expired", 401, connector.ErrAuthExpired, ""},
		{"rate_limited", 429, connector.ErrRateLimited, ""},
		{"missing_required", 200, "", "missing_required"}, {"unknown_status", 200, "", "invalid_value"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			credential := testCredential(t)
			list, detail := fixture(t, "list"), fixture(t, tc.name)
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				assertSignature(t, r, credential)
				w.Header().Set("Content-Type", "application/json")
				if tc.status != 200 {
					w.Header().Set("Retry-After", "7")
					w.WriteHeader(tc.status)
					_, err := w.Write(detail)
					require.NoError(t, err)
					return
				}
				if r.URL.Path == listPath {
					_, err := w.Write(list)
					require.NoError(t, err)
					return
				}
				require.Equal(t, detailPath, r.URL.Path)
				require.Equal(t, "100", r.URL.Query().Get("item_id_list"))
				_, err := w.Write(detail)
				require.NoError(t, err)
			}))
			defer server.Close()
			platform, err := New(server.URL, server.Client())
			require.NoError(t, err)
			result, err := platform.Fetch(context.Background(), credential, connector.FetchRequest{Resource: connector.ResourceProduct, PageSize: 100})
			if tc.kind != "" {
				assertSafeFailure(t, err, credential, tc.kind)
				if tc.status == 429 {
					var p *connector.PlatformError
					require.ErrorAs(t, err, &p)
					require.Equal(t, 7, p.RetryAfterSeconds)
				}
				return
			}
			require.NoError(t, err)
			require.Equal(t, 2, calls)
			require.True(t, result.HasMore)
			require.Equal(t, "1", result.NextCursor)
			require.Len(t, result.Items, 1)
			require.Equal(t, "100", result.Items[0].ExternalID)
			assertFixtureMapping(t, result.Items[0].Payload, tc.reason)
		})
	}
}

func assertFixtureMapping(t *testing.T, raw json.RawMessage, reason string) {
	t.Helper()
	program, err := ingestion.CompileWithStatusTables([]byte(`{"schema_version":1,"resource":"product","fields":[{"target":"title","source":"$.item_name","required":true},{"target":"status","source":"$.item_status","required":true,"transform":"map_status(value, 'shopee_product')"}]}`), map[string]map[string]string{"shopee_product": {"NORMAL": "online", "UNLIST": "offline"}})
	require.NoError(t, err)
	values, problems, err := program.Apply(raw)
	require.NoError(t, err)
	if reason == "" {
		require.Empty(t, problems)
		require.Equal(t, "online", values["status"])
		return
	}
	require.Len(t, problems, 1)
	require.Equal(t, reason, problems[0].Kind)
	if reason == "invalid_value" {
		require.NotEqual(t, "online", values["status"])
		require.NotEqual(t, "offline", values["status"])
	}
}

func assertSafeFailure(t *testing.T, err error, c connector.Credential, kind connector.ErrKind) {
	t.Helper()
	var failure *connector.PlatformError
	require.ErrorAs(t, err, &failure)
	require.Equal(t, kind, failure.Kind)
	for _, encoded := range c.Values {
		var value string
		require.NoError(t, json.Unmarshal(encoded, &value))
		require.NotContains(t, fmt.Sprintf("%+v", err), value)
	}
}

func TestShopeePaginationAndTransportSafety(t *testing.T) {
	credential := testCredential(t)
	for _, mode := range []string{"empty", "500", "echo", "redirect", "bad_cursor"} {
		t.Run(mode, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch mode {
				case "empty":
					require.Equal(t, "1", r.URL.Query().Get("offset"))
					_, err := w.Write([]byte(`{"response":{"item":[],"has_next_page":false,"next_offset":1}}`))
					require.NoError(t, err)
				case "500":
					w.WriteHeader(500)
				case "echo":
					_, err := w.Write([]byte(`{"error":"error_token","message":` + string(credential.Values["access_token"]) + `}`))
					require.NoError(t, err)
				case "redirect":
					w.Header().Set("Location", r.URL.String())
					w.WriteHeader(302)
				case "bad_cursor":
					_, err := w.Write([]byte(`{"response":{"item":[],"has_next_page":true,"next_offset":1}}`))
					require.NoError(t, err)
				}
			}))
			defer server.Close()
			platform, err := New(server.URL, server.Client())
			require.NoError(t, err)
			result, err := platform.Fetch(context.Background(), credential, connector.FetchRequest{Resource: connector.ResourceProduct, Cursor: "1", PageSize: 100})
			if mode == "empty" {
				require.NoError(t, err)
				require.Empty(t, result.Items)
				require.False(t, result.HasMore)
				return
			}
			kind := connector.ErrInvalidRequest
			if mode == "500" {
				kind = connector.ErrUnavailable
			}
			if mode == "echo" {
				kind = connector.ErrAuthExpired
			}
			assertSafeFailure(t, err, credential, kind)
		})
	}
	platform, err := New("https://fixture.invalid", &http.Client{Timeout: time.Millisecond})
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = platform.Fetch(ctx, credential, connector.FetchRequest{Resource: connector.ResourceProduct, PageSize: 100})
	assertSafeFailure(t, err, credential, connector.ErrUnavailable)
	require.False(t, strings.Contains(err.Error(), "access_token"))
}
