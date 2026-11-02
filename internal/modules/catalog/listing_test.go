package catalog

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/omwk/go-admin-api/internal/modules/ingestion"
	"github.com/stretchr/testify/require"
)

func TestMergeExtKeepsMappingFieldsAndReservedSourceMetadata(t *testing.T) {
	got := mergeExt(map[string]any{"ext": map[string]any{"channel": "web"}}, ingestion.Mapped{
		SourceOrder: 4, SourceFetchedAt: time.Date(2026, 11, 1, 1, 2, 3, 0, time.UTC), MappingChecksum: "new-checksum",
	})
	var ext map[string]any
	require.NoError(t, json.Unmarshal(got, &ext))
	require.Equal(t, "web", ext["channel"])
	reserved, ok := ext[ReservedExtKey].(map[string]any)
	require.True(t, ok)
	require.Equal(t, float64(4), reserved["source_order"])
	require.Equal(t, "new-checksum", reserved["mapping_checksum"])
}

func TestCompareSourceProtectsNewerSourceButAllowsNewChecksum(t *testing.T) {
	fetched := time.Date(2026, 11, 1, 1, 0, 0, 0, time.UTC)
	old := ingestion.Mapped{SourceOrder: 5, SourceFetchedAt: fetched, MappingChecksum: "old"}
	raw := mergeExt(nil, old)
	require.Equal(t, 1, compareSource(raw, ingestion.Mapped{SourceOrder: 4, SourceFetchedAt: fetched.Add(time.Hour), MappingChecksum: "new"}))
	require.Equal(t, 0, compareSource(raw, old))
	require.Equal(t, -1, compareSource(raw, ingestion.Mapped{SourceOrder: 5, SourceFetchedAt: fetched, MappingChecksum: "new"}))
}

func TestIntegerOrRejectsFractionalValues(t *testing.T) {
	require.Equal(t, int64(12), integerOr("12", 0))
	require.Equal(t, int64(0), integerOr("12.5", 0))
}
