package order

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"

	"github.com/omwk/go-admin-api/internal/kernel/errs"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func verifyRelationships(tx *gorm.DB, s Snapshot) error {
	var count int64
	if err := tx.Table("shop").Where("tenant_id=? AND id=? AND platform_id=? AND deleted_at IS NULL", s.TenantID, s.ShopID, s.PlatformID).Count(&count).Error; err != nil {
		return err
	}
	if count != 1 {
		return errs.New(errs.ErrDataScopeDenied, 403, "订单来源关系不匹配")
	}
	var sources []struct{ ExternalID string }
	if err := tx.Table("raw_dedup").Select("external_id").Where("tenant_id=? AND shop_id=? AND platform_id=? AND resource_type=? AND raw_id=? AND fetched_at=?",
		s.TenantID, s.ShopID, s.PlatformID, "order", s.Source.RawID, s.Source.FetchedAt).Limit(2).Find(&sources).Error; err != nil {
		return err
	}
	if len(sources) != 1 || sources[0].ExternalID != s.ExternalOrderID {
		return errs.New(errs.ErrDataScopeDenied, 403, "订单来源关系不匹配")
	}
	return nil
}

func loadOrder(tx *gorm.DB, s Snapshot) (Order, error) {
	// The identity lock also serializes the absent-parent case without aborting a transaction.
	key := "order:" + identityKey(s.TenantID, s.ShopID, s.ExternalOrderID)
	if err := tx.Exec("SELECT pg_advisory_xact_lock(hashtextextended(?,0))", key).Error; err != nil {
		return Order{}, err
	}
	var row Order
	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Select(orderColumns).
		Where("tenant_id=? AND shop_id=? AND external_order_id=?", s.TenantID, s.ShopID, s.ExternalOrderID).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return Order{}, nil
	}
	return row, err
}

func sourceDecision(old Order, s Snapshot, hash string) string {
	if old.ID == 0 {
		return ""
	}
	if old.PlatformID != s.PlatformID {
		return "source_identity_ambiguous"
	}
	if old.Revision != nil && s.Revision == nil {
		return "revision_missing"
	}
	sameSource := old.RawID == s.Source.RawID
	sameRevision := old.Revision == nil && s.Revision == nil || old.Revision != nil && s.Revision != nil && *old.Revision == *s.Revision
	if sameSource && sameRevision && old.MappingChecksum == s.Source.MappingChecksum && old.ContentHash == hash {
		return "skipped_same"
	}
	if old.Revision != nil && s.Revision != nil {
		if *s.Revision < *old.Revision {
			return "skipped_stale"
		}
		if *s.Revision == *old.Revision {
			if hash == old.ContentHash && old.MappingChecksum == s.Source.MappingChecksum {
				return "skipped_same"
			}
			if !sameSource || old.MappingChecksum == s.Source.MappingChecksum {
				return "revision_conflict"
			}
		}
		return ""
	}
	if !sameSource && s.Source.FetchedAt.Before(old.SourceFetchedAt) {
		return "skipped_stale"
	}
	return ""
}

func snapshotHash(s Snapshot) (string, error) {
	s.Source, s.ExpectedVersion, s.Revision = Source{}, 0, nil
	s.Mode, s.Correction = "", nil
	if s.Items != nil {
		copySet := *s.Items
		copySet.Rows = append([]ItemInput(nil), s.Items.Rows...)
		sort.Slice(copySet.Rows, func(i, j int) bool { return copySet.Rows[i].ExternalItemID < copySet.Rows[j].ExternalItemID })
		s.Items = &copySet
	}
	value := struct {
		Snapshot
		AddressFields [3][]byte
	}{Snapshot: s}
	if s.Address != nil {
		value.AddressFields = [3][]byte{s.Address.ReceiverName, s.Address.Phone, s.Address.Address}
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return "", errors.New("hash order facts failed")
	}
	defer clear(raw)
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:]), nil
}
