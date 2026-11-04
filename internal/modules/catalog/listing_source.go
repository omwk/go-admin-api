package catalog

import (
	"bytes"
	"encoding/json"
	"time"

	"github.com/omwk/go-admin-api/internal/modules/ingestion"
	"gorm.io/gorm"
)

type sourceMetadata struct {
	Order     int64     `json:"source_order"`
	FetchedAt time.Time `json:"source_fetched_at"`
	Checksum  string    `json:"mapping_checksum"`
}

func listingSource(row Listing) (sourceMetadata, string) {
	var ext map[string]json.RawMessage
	var source sourceMetadata
	if json.Unmarshal(row.Ext, &ext) != nil || len(ext[ReservedExtKey]) == 0 || json.Unmarshal(ext[ReservedExtKey], &source) != nil || source.Order <= 0 || source.FetchedAt.IsZero() || source.Checksum == "" || row.RawID == nil {
		return source, "source_metadata_unknown"
	}
	if source.Order != *row.RawID {
		return source, "source_identity_ambiguous"
	}
	return source, ""
}

func verifySourceDirectory(tx *gorm.DB, record ingestion.Mapped, id int64, fetched time.Time) (string, error) {
	var rows []struct {
		ExternalID string
		FetchedAt  time.Time
	}
	err := tx.Table("raw_dedup").Select("external_id,fetched_at").Where("tenant_id=? AND platform_id=? AND shop_id=? AND resource_type=? AND raw_id=?", record.TenantID, record.PlatformID, record.ShopID, "product", id).Limit(2).Find(&rows).Error
	if err != nil {
		return "", err
	}
	if len(rows) != 1 || rows[0].ExternalID != record.ExternalID || !rows[0].FetchedAt.Equal(fetched) {
		return "source_identity_ambiguous", nil
	}
	return "", nil
}

func arbitrateSource(tx *gorm.DB, row Listing, incoming ingestion.Mapped) (string, string, error) {
	old, reason := listingSource(row)
	if reason != "" {
		return "", reason, nil
	}
	if incoming.RawID <= 0 || incoming.SourceOrder != incoming.RawID || incoming.SourceFetchedAt.IsZero() || incoming.MappingChecksum == "" {
		return "", "source_identity_ambiguous", nil
	}
	if row.PlatformID != incoming.PlatformID || row.ExternalProductID != incoming.ExternalID {
		return "", "source_identity_ambiguous", nil
	}
	for _, ref := range []struct {
		id int64
		at time.Time
	}{{old.Order, old.FetchedAt}, {incoming.RawID, incoming.SourceFetchedAt}} {
		reason, err := verifySourceDirectory(tx, incoming, ref.id, ref.at)
		if err != nil || reason != "" {
			return "", reason, err
		}
	}
	if old.Order > incoming.SourceOrder {
		return "skipped_stale", "", nil
	}
	if old.Order == incoming.SourceOrder {
		if !old.FetchedAt.Equal(incoming.SourceFetchedAt) {
			return "", "source_identity_ambiguous", nil
		}
		if old.Checksum == incoming.MappingChecksum {
			return "skipped_same", "", nil
		}
	}
	return "", "", nil
}

func compareSource(raw json.RawMessage, incoming ingestion.Mapped) int {
	var ext map[string]json.RawMessage
	var old sourceMetadata
	if json.NewDecoder(bytes.NewReader(raw)).Decode(&ext) != nil || json.Unmarshal(ext[ReservedExtKey], &old) != nil {
		return -1
	}
	if old.Order > incoming.SourceOrder {
		return 1
	}
	if old.Order == incoming.SourceOrder && old.FetchedAt.Equal(incoming.SourceFetchedAt) && old.Checksum == incoming.MappingChecksum {
		return 0
	}
	return -1
}
