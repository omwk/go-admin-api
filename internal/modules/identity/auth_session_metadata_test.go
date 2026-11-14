package identity

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/require"
)

func TestSessionMetadataLoginAndRefreshBounded(t *testing.T) {
	// BE-IDN-003: both issuance paths persist bounded UTF-8 client data without invalid inet input.
	f := newAuthFixture(t)
	tokens, err := f.service.Login(f.ctx, 1, f.store.user.Username, f.password, RequestMeta{
		IP: strings.Repeat("x", 1000), UserAgent: strings.Repeat("界", 300),
	})
	require.NoError(t, err)
	for _, row := range f.store.tokens {
		require.Nil(t, row.IP)
		require.LessOrEqual(t, len(row.UserAgent), 256)
		require.True(t, utf8.ValidString(row.UserAgent))
	}
	_, err = f.service.Refresh(f.ctx, tokens.RefreshToken, RequestMeta{
		IP: "2001:0db8:0000:0000:0000:0000:0000:0001", UserAgent: strings.Repeat("a", 255) + "界",
	})
	require.NoError(t, err)
	for _, row := range f.store.tokens {
		if row.RevokedAt == nil {
			require.NotNil(t, row.IP)
			require.Equal(t, "2001:db8::1", *row.IP)
			require.Equal(t, strings.Repeat("a", 255), row.UserAgent)
		}
	}
}
