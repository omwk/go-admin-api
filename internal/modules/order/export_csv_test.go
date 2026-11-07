package order

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestOrderExportProjectionWhitelistAndFormulaSafety(t *testing.T) {
	// REQ-ORD-008: the CSV contract excludes all PII and platform extension maps.
	header := ExportCSVHeader()
	for _, field := range []string{"receiver_name", "phone", "address", "postal_code", "buyer_id", "buyer_nick", "raw", "ext", "_ingestion"} {
		require.NotContains(t, header, field)
	}
	for _, formula := range []string{"=1+1", "+1", "-1", "@SUM(A1)", " \t=1+1", "\r=1+1", "\t123"} {
		data, err := json.Marshal(ExportProjection{ID: 1, OrderNo: formula, Status: "completed", Currency: "CNY", CreatedAt: time.Now().UTC()})
		require.NoError(t, err)
		cells, err := ExportCSVRecord(data)
		require.NoError(t, err)
		require.Len(t, cells, len(header))
		require.Equal(t, "'"+formula, cells[2])
	}
	_, err := ExportCSVRecord(json.RawMessage(`{"id":1,"address":"PII-CANARY-123"}`))
	require.Error(t, err)
	require.NotContains(t, err.Error(), "PII-CANARY-123")
	for _, field := range header {
		require.False(t, strings.HasPrefix(field, "_"))
	}
}
