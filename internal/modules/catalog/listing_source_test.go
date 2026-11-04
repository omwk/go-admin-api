package catalog

import (
	"testing"
	"time"

	"github.com/omwk/go-admin-api/internal/modules/ingestion"
	"github.com/stretchr/testify/require"
)

func TestSourceMetadataRequiresCompleteIdentity(t *testing.T) {
	// REQ-SYNC-011 / ADR-018: neither checksum nor a later timestamp proves identity.
	now := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name, ext string
		id        int64
		reason    string
	}{
		{"missing", `{}`, 5, "source_metadata_unknown"},
		{"invalid time", `{"_ingestion":{"source_order":5,"source_fetched_at":"invalid","mapping_checksum":"old"}}`, 5, "source_metadata_unknown"},
		{"order mismatch", `{"_ingestion":{"source_order":4,"source_fetched_at":"2026-11-01T00:00:00Z","mapping_checksum":"old"}}`, 5, "source_identity_ambiguous"},
		{"missing checksum", `{"_ingestion":{"source_order":5,"source_fetched_at":"2026-11-01T00:00:00Z"}}`, 5, "source_metadata_unknown"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, reason := listingSource(Listing{RawID: &tc.id, Ext: []byte(tc.ext)})
			require.Equal(t, tc.reason, reason)
		})
	}
	incoming := ingestion.Mapped{RawID: 5, SourceOrder: 5, SourceFetchedAt: now, MappingChecksum: "new"}
	require.Equal(t, -1, compareSource([]byte(`{"_ingestion":{"source_order":5,"source_fetched_at":"2026-11-01T00:00:00Z","mapping_checksum":"old"}}`), incoming))
}
