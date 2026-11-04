package bootstrap

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/omwk/go-admin-api/internal/modules/ingestion"
	"github.com/stretchr/testify/require"
)

// seedMappedSource supplies real persisted provenance to canonical-only fixtures.
func seedMappedSource(t *testing.T, f *syncFixture, m *ingestion.Mapped) {
	t.Helper()
	payload := []byte(`{"fixture":"` + m.ExternalID + `","revision":"` + time.Now().UTC().Format(time.RFC3339Nano) + `"}`)
	refs, err := rawServiceFor(t, f.database, m.TenantID, m.PlatformID).SaveRaw(f.ctx, m.ShopID, "product", []ingestion.RawItem{{ExternalID: m.ExternalID, Payload: payload}})
	require.NoError(t, err)
	m.RawID, m.SourceOrder, m.SourceFetchedAt = refs[0].ID, refs[0].ID, refs[0].FetchedAt
	if m.MappingChecksum == "" {
		m.MappingChecksum = "catalog-fixture"
	}
}

func seedExistingListingSource(t *testing.T, f *syncFixture, id int64, m *ingestion.Mapped) {
	t.Helper()
	old := *m
	seedMappedSource(t, f, &old)
	ext, err := json.Marshal(map[string]any{"_ingestion": map[string]any{"source_order": old.SourceOrder, "source_fetched_at": old.SourceFetchedAt.UTC().Format(time.RFC3339Nano), "mapping_checksum": old.MappingChecksum}})
	require.NoError(t, err)
	require.NoError(t, f.database.Exec(f.ctx, "UPDATE listing SET raw_id=?,ext=? WHERE id=?", old.RawID, ext, id))
	seedMappedSource(t, f, m)
}
