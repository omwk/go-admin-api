package setting

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/omwk/go-admin-api/internal/kernel/errs"
	"github.com/stretchr/testify/require"
)

func TestTenantRegistrationDefaultsAndValidation(t *testing.T) {
	items, found := DefaultRegistry().Items("tenant")
	require.True(t, found)
	require.Len(t, items, 3)
	defaults := map[string]any{}
	for _, item := range items {
		defaults[item.Key] = item.Default
	}
	require.Equal(t, false, defaults["allow_self_registration"])
	require.Equal(t, "", defaults["default_role_code"])
	require.Equal(t, 10, defaults["registration_hourly_limit"])
	for _, tc := range []struct{ key, raw string }{
		{"allow_self_registration", `"true"`}, {"allow_self_registration", `null`},
		{"default_role_code", `false`}, {"registration_hourly_limit", `0`},
		{"registration_hourly_limit", `1.5`}, {"registration_hourly_limit", `"10"`},
		{"unknown", `true`},
	} {
		t.Run(tc.key+tc.raw, func(t *testing.T) {
			_, err := DefaultRegistry().Validate("tenant", map[string]json.RawMessage{tc.key: json.RawMessage(tc.raw)})
			require.Error(t, err)
			require.Equal(t, 400, errs.From(err).HTTPStatus)
			require.Equal(t, tc.key, errs.From(err).Details["key"])
		})
	}
	values, err := values(context.Background(), nil, 1, "tenant")
	require.NoError(t, err)
	require.EqualValues(t, 10, values["registration_hourly_limit"])
}
