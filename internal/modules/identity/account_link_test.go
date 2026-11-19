package identity

import (
	"net/url"
	"testing"

	"github.com/omwk/go-admin-api/internal/modules/identity/refresh"
	"github.com/stretchr/testify/require"
)

func TestAccountLinkAbsoluteTenantAndFragment(t *testing.T) {
	// BE-IDN-016 §4 #1/3/4/9: all account links share the same safe URL contract.
	raw, err := refresh.Generate()
	require.NoError(t, err)
	for purpose, path := range map[string]string{"verify": "/auth/register/verify", "reset": "/auth/password/reset", "invite": "/auth/invite/accept", "email_change": "/auth/email/verify"} {
		t.Run(purpose, func(t *testing.T) {
			link, err := AccountLink("http://127.0.0.1:5555/base/", purpose, 42, "租户 & acme", raw)
			require.NoError(t, err)
			u, err := url.Parse(link)
			require.NoError(t, err)
			require.True(t, u.IsAbs())
			require.Equal(t, "/base"+path, u.Path)
			require.Equal(t, "租户 & acme", u.Query().Get("tenant"))
			if purpose == "email_change" {
				require.False(t, u.Query().Has("tenant_id"))
			} else {
				require.Equal(t, "42", u.Query().Get("tenant_id"))
			}
			require.Empty(t, u.Query().Get("token"))
			fragment, err := url.ParseQuery(u.Fragment)
			require.NoError(t, err)
			require.True(t, fragment.Get("token") == raw)
			u.Fragment = ""
			require.NotContains(t, u.String(), raw)
		})
	}
}

func TestAccountLinkRejectsMissingAndInvalidBase(t *testing.T) {
	raw, err := refresh.Generate()
	require.NoError(t, err)
	for _, base := range []string{"", " ", "/relative", "ftp://example.invalid", "https://example.invalid?view=mail", "https://example.invalid#section"} {
		link, err := AccountLink(base, "verify", 1, "default", raw)
		require.ErrorContains(t, err, "app.public_url")
		require.Empty(t, link)
		require.NotContains(t, err.Error(), raw)
	}
}
