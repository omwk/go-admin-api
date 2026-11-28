package gateway

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNormalizeIPWhitelist(t *testing.T) {
	got, err := NormalizeIPWhitelist([]string{" 10.1.2.3 ", "10.1.2.3/8", "::1", "2001:db8::1/64"})
	require.NoError(t, err)
	require.Equal(t, []string{"10.1.2.3", "10.0.0.0/8", "::1", "2001:db8::/64"}, got)
	for _, value := range []string{"bad-ip", "10.0.0.0/33", "0.0.0.0/0", "::/0"} {
		t.Run(value, func(t *testing.T) {
			_, err := NormalizeIPWhitelist([]string{value})
			require.Error(t, err)
		})
	}
}

func TestValidateClientUpdateRequiresMutableFieldAndVersion(t *testing.T) {
	_, err := ValidateClientUpdate(ClientUpdate{}, 1)
	require.Error(t, err)
	name := "  updated  "
	update, err := ValidateClientUpdate(ClientUpdate{Name: &name}, 2)
	require.NoError(t, err)
	require.Equal(t, "updated", *update.Name)
	_, err = ValidateClientUpdate(ClientUpdate{Name: &name}, 0)
	require.Error(t, err)
}
