package crypto

import (
	"encoding/base64"
	"encoding/json"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestEnvironmentInjectsKeysAndRejectsInvalidConfiguration(t *testing.T) {
	encoded := base64.StdEncoding.EncodeToString(randomBytes(t, 32))
	for _, tc := range []struct {
		name, active, keys string
		valid              bool
	}{
		{"valid", "2", "", true}, {"missing keys", "2", "null", false},
		{"invalid JSON", "2", "{", false}, {"invalid base64", "2", `{"2":"!"}`, false},
		{"wrong length", "2", `{"2":""}`, false}, {"missing active key", "3", "", false},
		{"invalid active", "x", "", false}, {"nonpositive active", "0", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			keys := tc.keys
			if keys == "" {
				data, err := json.Marshal(map[int]string{1: encoded, 2: "base64:" + encoded})
				require.NoError(t, err)
				keys = string(data)
			}
			k, err := FromEnvironment(func(name string) string {
				if name == "CH_CRYPTO_KEYS" {
					return keys
				}
				if name == "CH_CRYPTO_ACTIVE_VERSION" {
					return tc.active
				}
				return ""
			})
			if tc.valid {
				require.NoError(t, err)
				_, _, v, err := k.Seal(randomBytes(t, 64), "1:2:3:cookie:1")
				require.NoError(t, err)
				require.Equal(t, 2, v)
			} else {
				require.Error(t, err)
				require.NotContains(t, err.Error(), encoded)
			}
		})
	}
	_, err := FromEnvironment(nil)
	require.Error(t, err)
}

func TestCredentialAADCanonicalEncodingAndValidation(t *testing.T) {
	a := CredentialAAD{TenantID: 1, ShopID: 2, CredentialID: 3, Kind: "oauth2", VersionNo: 4}
	encoded, err := a.Encode()
	require.NoError(t, err)
	require.Equal(t, "1:2:3:oauth2:4", encoded)
	for _, kind := range []string{"oauth2", "api_key", "cookie", "session"} {
		a.Kind = kind
		_, err := a.Encode()
		require.NoError(t, err)
	}
	for _, dimension := range []int{0, 1, 2, 3, 4} {
		t.Run(strconv.Itoa(dimension), func(t *testing.T) {
			invalid := a
			switch dimension {
			case 0:
				invalid.TenantID = 0
			case 1:
				invalid.ShopID = -1
			case 2:
				invalid.CredentialID = 0
			case 3:
				invalid.Kind = "oauth2:1"
			case 4:
				invalid.VersionNo = 0
			}
			_, err := invalid.Encode()
			require.Error(t, err)
		})
	}
}
