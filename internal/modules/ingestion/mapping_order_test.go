package ingestion

import (
	"encoding/json"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestOrderMappingWhitelist(t *testing.T) {
	for _, target := range []string{"ext.phone", "ext.unlisted", "ext", "buyer_nick", "skus"} {
		spec := Spec{SchemaVersion: 1, Resource: "order", KeyField: "id", Fields: []FieldSpec{{Target: target, Source: "$.value"}}}
		raw, err := json.Marshal(spec)
		require.NoError(t, err)
		_, err = Compile(raw)
		require.Error(t, err, target)
	}
	for _, target := range []string{"lifecycle_status", "paid_amount_cents", "receiver_name", "phone", "address", "items_complete", "deleted"} {
		spec := Spec{SchemaVersion: 1, Resource: "order", KeyField: "id", Fields: []FieldSpec{{Target: target, Source: "$.value"}}}
		raw, err := json.Marshal(spec)
		require.NoError(t, err)
		_, err = Compile(raw)
		require.NoError(t, err, target)
	}
}
