package order

import (
	"encoding/json"
	"testing"

	"github.com/omwk/go-admin-api/internal/modules/ingestion"
	"github.com/stretchr/testify/require"
)

func TestCanonicalOrderRejectsLossyNumbersAndUnknownFields(t *testing.T) {
	for _, value := range []any{float64(100), "100", json.Number("100.1"), json.Number("9223372036854775808")} {
		var h canonicalHeader
		require.Error(t, decodeCanonical(map[string]any{"paid_amount_cents": value}, &h, false))
	}
	for _, value := range []any{int64(100), json.Number("100")} {
		var h canonicalHeader
		require.NoError(t, decodeCanonical(map[string]any{"paid_amount_cents": value}, &h, false))
		require.EqualValues(t, 100, *h.Paid)
	}
	var h canonicalHeader
	require.Error(t, decodeCanonical(map[string]any{"ext.phone": "forbidden"}, &h, false))
	var i canonicalItem
	require.Error(t, decodeCanonical(map[string]any{"buyer_id": "forbidden"}, &i, true))
}

func TestOrderMappedDetailRequiresCompleteItems(t *testing.T) {
	h := canonicalHeader{ExpectedCount: new(int)}
	for _, collections := range []map[string][]map[string]any{nil, {"items": nil}, {"skus": nil}} {
		require.Error(t, decodeMappedItems(ingestion.Mapped{Collections: collections}, h, &Snapshot{}))
	}
	h.Complete, h.EmptyConfirmed = true, true
	require.NoError(t, decodeMappedItems(ingestion.Mapped{Collections: map[string][]map[string]any{"items": {}}}, h, &Snapshot{}))
}
