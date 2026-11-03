package sync

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestChildIdentifierQuarantineReason(t *testing.T) {
	for _, tc := range []struct {
		name   string
		child  map[string]any
		reason string
	}{
		{"missing", map[string]any{}, "missing_required"},
		{"null", map[string]any{"external_sku_id": nil}, "missing_required"},
		{"blank", map[string]any{"external_sku_id": " "}, "missing_required"},
		{"number", map[string]any{"external_sku_id": 42}, "type_mismatch"},
		{"object", map[string]any{"external_sku_id": map[string]any{}}, "type_mismatch"},
		{"valid", map[string]any{"external_sku_id": "sku-1"}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.reason, validateChildren([]map[string]any{tc.child}))
		})
	}
}
