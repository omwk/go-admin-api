package bootstrap

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/omwk/go-admin-api/internal/modules/identity"
	"github.com/omwk/go-admin-api/internal/modules/share"
	"github.com/stretchr/testify/require"
)

type shareAccessFixture struct {
	*shareFixture
	links *share.LinkService
}

func newShareAccessFixture(t *testing.T) *shareAccessFixture {
	t.Helper()
	f := &shareAccessFixture{shareFixture: newShareFixture(t)}
	var err error
	f.links, err = share.NewLinkService(share.LinkDependencies{Store: &shareStore{database: f.database}, Idempotency: newIdempotencyStore(f.database),
		Sealer: f.sealer, Cache: f.redis.cache, Now: func() time.Time { return f.now }})
	require.NoError(t, err)
	profiles, err := identity.NewProfileService(newProfileStore(f.database))
	require.NoError(t, err)
	require.NoError(t, share.RegisterLinkRoutes(f.router, f.identityFixture.service, profiles, f.links))
	share.RegisterPublicRoutes(f.router, f.links, 1)
	return f
}

func (f *shareAccessFixture) publication() share.LinkInput {
	return share.LinkInput{Title: "Approved quote", Currency: "CNY", Mode: "snapshot", AllowFallback: true, Items: []share.Member{{SPUID: f.spu}}}
}

func (f *shareAccessFixture) issue(t *testing.T, in share.LinkInput) share.LinkReceipt {
	t.Helper()
	r, e := f.request(t, "POST", "/share-links", in)
	require.Equal(t, 201, r.Code)
	var out share.LinkReceipt
	require.NoError(t, json.Unmarshal(e.Data, &out))
	require.NotEmpty(t, out.URL)
	return out
}

func (f *shareAccessFixture) anonymous(t *testing.T, method, path, ip string, payload any, cookie *http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	raw, err := json.Marshal(payload)
	require.NoError(t, err)
	req := httptest.NewRequest(method, "https://quote.test"+path, bytes.NewReader(raw))
	req.RemoteAddr = ip + ":32100"
	req.Header.Set("Content-Type", "application/json")
	if cookie != nil {
		req.AddCookie(cookie)
	}
	w := httptest.NewRecorder()
	f.router.ServeHTTP(w, req)
	require.Equal(t, "private, no-store", w.Header().Get("Cache-Control"))
	require.Equal(t, "no-referrer", w.Header().Get("Referrer-Policy"))
	f.noLeaks(t, "anonymous response", w.Body.String())
	return w
}

func sharePublicData(t *testing.T, w *httptest.ResponseRecorder) share.PublicContent {
	t.Helper()
	require.Equal(t, 200, w.Code, w.Body.String())
	var envelope struct {
		Data share.PublicContent `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &envelope))
	return envelope.Data
}

func shareCookie(t *testing.T, w *httptest.ResponseRecorder) *http.Cookie {
	t.Helper()
	cookies := w.Result().Cookies()
	require.Len(t, cookies, 1)
	return cookies[0]
}

func shareToken(link share.LinkReceipt) string { return strings.TrimPrefix(link.URL, "/api/v1/share/") }
