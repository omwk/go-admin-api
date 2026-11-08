package bootstrap

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestShareIntegrationPasswordGateCookieAttributesAndBinding(t *testing.T) {
	// REQ-SHR-005/006: verification and root admission are separate capabilities.
	f := newShareAccessFixture(t)
	c := f.customer(t, "password")
	in := f.publication()
	in.CustomerID, in.Password = c.ID, f.randomValue(t)
	link := f.issue(t, in)
	detail := fmt.Sprintf("%s/items/%d", link.URL, f.spu)
	w := f.anonymous(t, "GET", link.URL, "192.0.2.1", nil, nil)
	require.Equal(t, 401, w.Code)
	require.NotContains(t, w.Body.String(), "Approved quote")
	require.NotContains(t, w.Body.String(), "Pricing product")
	w = f.anonymous(t, "POST", link.URL+"/verify", "192.0.2.1", map[string]string{"password": in.Password}, nil)
	require.Equal(t, 200, w.Code)
	cookie := shareCookie(t, w)
	require.True(t, cookie.HttpOnly)
	require.True(t, cookie.Secure)
	require.Equal(t, http.SameSiteLaxMode, cookie.SameSite)
	require.Equal(t, "/api/v1/share", cookie.Path)
	require.Empty(t, cookie.Domain)
	require.Equal(t, 7200, cookie.MaxAge)
	require.Equal(t, f.now.Add(2*time.Hour), cookie.Expires)
	require.Equal(t, 401, f.anonymous(t, "GET", detail, "192.0.2.1", nil, cookie).Code)
	w = f.anonymous(t, "GET", link.URL, "192.0.2.1", nil, cookie)
	require.Equal(t, 200, w.Code)
	cookie = shareCookie(t, w)
	require.Equal(t, 200, f.anonymous(t, "GET", detail, "192.0.2.2", nil, cookie).Code, "not bound to a mobile network address")
	other := f.issue(t, in)
	require.Equal(t, 401, f.anonymous(t, "GET", other.URL, "192.0.2.2", nil, cookie).Code)
	altered := *cookie
	altered.Value = altered.Value[:len(altered.Value)-6] + "AAAAAA"
	require.Equal(t, 401, f.anonymous(t, "GET", link.URL, "192.0.2.2", nil, &altered).Code)
	f.now = f.now.Add(2 * time.Hour)
	require.Equal(t, 401, f.anonymous(t, "GET", link.URL, "192.0.2.2", nil, cookie).Code)
}

func TestShareIntegrationFiveFailuresIsolateTokenAndSource(t *testing.T) {
	f := newShareAccessFixture(t)
	c := f.customer(t, "budget")
	in := f.publication()
	in.CustomerID, in.Password = c.ID, f.randomValue(t)
	a, b := f.issue(t, in), f.issue(t, in)
	wrong := f.randomValue(t)
	for i := range 5 {
		w := f.anonymous(t, "POST", a.URL+"/verify", "192.0.2.1", map[string]string{"password": wrong}, nil)
		if i < 4 {
			require.Equal(t, 401, w.Code)
		} else {
			require.Equal(t, 429, w.Code)
			require.NotEmpty(t, w.Header().Get("Retry-After"))
		}
	}
	for _, tc := range []struct{ url, ip string }{{b.URL, "192.0.2.1"}, {a.URL, "192.0.2.2"}} {
		w := f.anonymous(t, "POST", tc.url+"/verify", tc.ip, map[string]string{"password": in.Password}, nil)
		require.Equal(t, 200, w.Code)
	}
	require.Equal(t, 429, f.anonymous(t, "POST", a.URL+"/verify", "192.0.2.1", map[string]string{"password": in.Password}, nil).Code)
}
