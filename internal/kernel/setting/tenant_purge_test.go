package setting

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestTenantPurgeGraceDefaultsAndValidation(t *testing.T) {
	// BE-IDN-011 §2.3: days are integral, inclusive 0–365, with a safe default.
	days, err := TenantPurgeGraceDays(context.Background(), nil, 1)
	require.NoError(t, err)
	require.Equal(t, 7, days)
	for _, raw := range []string{`0`, `7`, `365`} {
		_, err := DefaultRegistry().Validate("tenant", map[string]json.RawMessage{"purge_grace_days": json.RawMessage(raw)})
		require.NoError(t, err, raw)
	}
	for _, raw := range []string{`-1`, `366`, `0.5`, `"0"`, `null`, `true`} {
		_, err := DefaultRegistry().Validate("tenant", map[string]json.RawMessage{"purge_grace_days": json.RawMessage(raw)})
		require.Error(t, err, raw)
	}
}
