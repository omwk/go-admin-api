package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/omwk/go-admin-api/internal/modules/ingestion"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ReservedExtKey is owned by the ingestion engine; mapping rules cannot write it.
const ReservedExtKey = "_ingestion"

// Listing is the M3 canonical platform product projection.
type Listing struct {
	ID                int64           `gorm:"column:id;primaryKey"`
	TenantID          int64           `gorm:"column:tenant_id;not null"`
	ShopID            int64           `gorm:"column:shop_id;not null"`
	PlatformID        int64           `gorm:"column:platform_id;not null"`
	SPUID             *int64          `gorm:"column:spu_id"`
	ExternalProductID string          `gorm:"column:external_product_id;not null"`
	Title             string          `gorm:"column:title;not null"`
	PriceCents        int64           `gorm:"column:price_cents;not null"`
	Currency          string          `gorm:"column:currency;type:char(3);not null"`
	Status            string          `gorm:"column:status;not null"`
	StockTotal        int             `gorm:"column:stock_total;not null"`
	RawID             *int64          `gorm:"column:raw_id"`
	SyncedAt          time.Time       `gorm:"column:synced_at;not null"`
	Ext               json.RawMessage `gorm:"column:ext;type:jsonb;not null"`
	CreatedAt         time.Time       `gorm:"column:created_at;not null"`
	UpdatedAt         time.Time       `gorm:"column:updated_at;not null"`
}

// TableName returns the listing table.
func (Listing) TableName() string { return "listing" }

// ListingSKU is a child projection. Replays never delete rows from this table.
type ListingSKU struct {
	ID            int64           `gorm:"column:id;primaryKey"`
	TenantID      int64           `gorm:"column:tenant_id;not null"`
	ListingID     int64           `gorm:"column:listing_id;not null"`
	SKUID         *int64          `gorm:"column:sku_id"`
	ExternalSKUID string          `gorm:"column:external_sku_id;not null"`
	Title         string          `gorm:"column:title;not null"`
	PriceCents    int64           `gorm:"column:price_cents;not null"`
	Stock         int             `gorm:"column:stock;not null"`
	Ext           json.RawMessage `gorm:"column:ext;type:jsonb;not null"`
	SyncedAt      time.Time       `gorm:"column:synced_at;not null"`
}

// TableName returns the listing_sku table.
func (ListingSKU) TableName() string { return "listing_sku" }

// Upserter implements ingestion.TargetUpserter for product/listing records.
type Upserter struct{}

// Upsert writes each parent and its children in the supplied transaction.
func (Upserter) Upsert(ctx context.Context, tx *gorm.DB, resource ingestion.ResourceType, records []ingestion.Mapped) (ingestion.UpsertStats, error) {
	if tx == nil {
		return ingestion.UpsertStats{}, errors.New("listing transaction is required")
	}
	if resource != ingestion.ResourceProduct {
		return ingestion.UpsertStats{}, fmt.Errorf("catalog resource %q is unsupported", resource)
	}
	var stats ingestion.UpsertStats
	syncedAt := time.Now().UTC()
	for _, record := range records {
		if err := ctx.Err(); err != nil {
			return stats, err
		}
		result, err := upsertListing(tx.WithContext(ctx), record, syncedAt)
		if err != nil {
			return stats, err
		}
		stats.Inserted += result.inserted
		stats.Updated += result.updated
		stats.Skipped += result.skipped
		stats.Quarantined += result.quarantined
		stats.SkippedStale += result.stale
		stats.SkippedSame += result.same
		if result.reason != "" {
			stats.Reason = result.reason
		}
		if result.spuID > 0 {
			if err := recomputeSPUSummary(ctx, tx, record.TenantID, result.spuID); err != nil {
				return stats, err
			}
		}
	}
	return stats, nil
}

type listingResult struct {
	inserted, updated, skipped, quarantined int
	spuID                                   int64
	stale, same                             int
	reason                                  string
}

func upsertListing(tx *gorm.DB, record ingestion.Mapped, syncedAt time.Time) (listingResult, error) {
	values := record.Values
	externalID, err := stringValue(values["external_product_id"])
	if err != nil || externalID == "" {
		externalID = record.ExternalID
	}
	if externalID == "" {
		return listingResult{quarantined: 1}, nil
	}
	if externalID != record.ExternalID {
		return listingResult{quarantined: 1, reason: "source_identity_ambiguous"}, nil
	}
	status, _ := stringValue(values["status"])
	if status == "" {
		status = "online"
	}
	if status != "online" && status != "offline" && status != "deleted" {
		return listingResult{quarantined: 1}, nil
	}
	row := Listing{TenantID: record.TenantID, ShopID: record.ShopID, PlatformID: record.PlatformID, ExternalProductID: externalID, Title: stringOr(values["title"], ""), PriceCents: integerOr(values["price_cents"], 0), Currency: stringOr(values["currency"], ""), Status: status, StockTotal: int(integerOr(values["stock_total"], 0)), RawID: rawIDPointer(record.RawID), SyncedAt: syncedAt, Ext: mergeExt(values, record)}
	var existing Listing
	query := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Select("id, tenant_id, shop_id, platform_id, spu_id, external_product_id, title, price_cents, currency, status, stock_total, raw_id, synced_at, ext, created_at, updated_at").Where("tenant_id = ? AND shop_id = ? AND external_product_id = ?", record.TenantID, record.ShopID, externalID).First(&existing)
	if query.Error != nil && !errors.Is(query.Error, gorm.ErrRecordNotFound) {
		return listingResult{}, query.Error
	}
	if query.Error == nil {
		outcome, reason, err := arbitrateSource(tx, existing, record)
		if err != nil {
			return listingResult{}, err
		}
		if reason != "" {
			return listingResult{quarantined: 1, reason: reason}, nil
		}
		if outcome == "skipped_stale" {
			return listingResult{skipped: 1, stale: 1}, nil
		}
		if outcome == "skipped_same" {
			return listingResult{skipped: 1, same: 1}, nil
		}
		updates := map[string]any{"platform_id": row.PlatformID, "title": row.Title, "price_cents": row.PriceCents, "currency": row.Currency, "status": row.Status, "stock_total": row.StockTotal, "raw_id": nullableID(record.RawID), "synced_at": row.SyncedAt, "ext": row.Ext, "updated_at": time.Now().UTC()}
		if err := tx.Model(&Listing{}).Where("id = ?", existing.ID).Updates(updates).Error; err != nil {
			return listingResult{}, err
		}
		if err := upsertSKUs(tx, existing.ID, record, row.SyncedAt); err != nil {
			return listingResult{}, err
		}
		result := listingResult{updated: 1}
		if existing.SPUID != nil {
			result.spuID = *existing.SPUID
		}
		return result, nil
	}
	if record.RawID <= 0 || record.SourceOrder != record.RawID || record.SourceFetchedAt.IsZero() || record.MappingChecksum == "" {
		return listingResult{quarantined: 1, reason: "source_metadata_unknown"}, nil
	}
	if reason, err := verifySourceDirectory(tx, record, record.RawID, record.SourceFetchedAt); err != nil {
		return listingResult{}, err
	} else if reason != "" {
		return listingResult{quarantined: 1, reason: reason}, nil
	}
	if err := tx.Create(&row).Error; err != nil {
		return listingResult{}, err
	}
	if err := upsertSKUs(tx, row.ID, record, row.SyncedAt); err != nil {
		return listingResult{}, err
	}
	return listingResult{inserted: 1}, nil
}

func recomputeSPUSummary(ctx context.Context, tx *gorm.DB, tenantID, spuID int64) error {
	return tx.WithContext(ctx).Exec(`UPDATE product_spu p SET
price_min_cents = COALESCE((SELECT MIN(ls.price_cents) FROM listing l JOIN listing_sku ls ON ls.tenant_id=l.tenant_id AND ls.listing_id=l.id WHERE l.tenant_id=p.tenant_id AND l.spu_id=p.id AND l.status='online'), 0),
price_max_cents = COALESCE((SELECT MAX(ls.price_cents) FROM listing l JOIN listing_sku ls ON ls.tenant_id=l.tenant_id AND ls.listing_id=l.id WHERE l.tenant_id=p.tenant_id AND l.spu_id=p.id AND l.status='online'), 0),
sku_count = (SELECT COUNT(DISTINCT ps.id) FROM product_sku ps WHERE ps.tenant_id=p.tenant_id AND ps.spu_id=p.id AND ps.deleted_at IS NULL),
updated_at = now()
WHERE p.tenant_id=? AND p.id=? AND p.deleted_at IS NULL`, tenantID, spuID).Error
}

// RecomputeSPUSummary updates denormalized SPU fields inside the caller's transaction.
func RecomputeSPUSummary(ctx context.Context, tx *gorm.DB, tenantID, spuID int64) error {
	if tx == nil || tenantID <= 0 || spuID <= 0 {
		return errors.New("SPU summary transaction and identity are required")
	}
	return recomputeSPUSummary(ctx, tx, tenantID, spuID)
}

func upsertSKUs(tx *gorm.DB, listingID int64, record ingestion.Mapped, syncedAt time.Time) error {
	rows := record.Collections["skus"]
	if len(rows) == 0 {
		if raw, ok := record.Values["skus"].([]map[string]any); ok {
			rows = raw
		}
	}
	for _, values := range rows {
		externalID := stringOr(values["external_sku_id"], "")
		if externalID == "" {
			continue
		}
		row := ListingSKU{TenantID: record.TenantID, ListingID: listingID, ExternalSKUID: externalID, Title: stringOr(values["title"], ""), PriceCents: integerOr(values["price_cents"], 0), Stock: int(integerOr(values["stock"], 0)), SyncedAt: syncedAt, Ext: marshalMap(values["ext"])}
		var existing ListingSKU
		query := tx.Select("id").Where("listing_id = ? AND external_sku_id = ?", listingID, externalID).First(&existing)
		if errors.Is(query.Error, gorm.ErrRecordNotFound) {
			if err := tx.Create(&row).Error; err != nil {
				return err
			}
		} else if query.Error != nil {
			return query.Error
		} else {
			row.ID = existing.ID
			if err := tx.Model(&ListingSKU{}).Where("id = ?", existing.ID).Updates(map[string]any{"tenant_id": row.TenantID, "title": row.Title, "price_cents": row.PriceCents, "stock": row.Stock, "ext": row.Ext, "synced_at": row.SyncedAt}).Error; err != nil {
				return err
			}
		}
		if err := tx.Create(&InventorySnapshot{TenantID: record.TenantID, ShopID: record.ShopID,
			ListingSKUID: row.ID, Available: row.Stock, Reserved: skuReserved(values), SyncedAt: syncedAt}).Error; err != nil {
			return err
		}
	}
	return nil
}

func skuReserved(values map[string]any) int {
	ext, _ := values["ext"].(map[string]any)
	return int(integerOr(ext["reserved"], 0))
}

func mergeExt(values map[string]any, record ingestion.Mapped) json.RawMessage {
	ext, _ := values["ext"].(map[string]any)
	if ext == nil {
		ext = make(map[string]any)
	}
	if categoryID := stringOr(values["category_id"], ""); categoryID != "" {
		ext["category_id"] = categoryID
	}
	ext[ReservedExtKey] = map[string]any{"source_order": record.SourceOrder, "source_fetched_at": record.SourceFetchedAt.UTC().Format(time.RFC3339Nano), "mapping_checksum": record.MappingChecksum}
	return marshalMap(ext)
}

func marshalMap(value any) json.RawMessage {
	if value == nil {
		return json.RawMessage(`{}`)
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return raw
}
func nullableID(id int64) any {
	if id <= 0 {
		return nil
	}
	return id
}

func rawIDPointer(id int64) *int64 {
	if id <= 0 {
		return nil
	}
	return &id
}
func stringOr(value any, fallback string) string {
	text, err := stringValue(value)
	if err != nil {
		return fallback
	}
	return text
}
func stringValue(value any) (string, error) {
	if value == nil {
		return "", errors.New("value is null")
	}
	if text, ok := value.(string); ok {
		return text, nil
	}
	if number, ok := value.(json.Number); ok {
		return number.String(), nil
	}
	return fmt.Sprint(value), nil
}
func integerOr(value any, fallback int64) int64 {
	raw, err := stringValue(value)
	if err != nil {
		return fallback
	}
	number, err := decimal.NewFromString(raw)
	if err != nil || !number.IsInteger() {
		return fallback
	}
	const maxInt64 = int64(1<<63 - 1)
	const minInt64 = -1 << 63
	if number.Cmp(decimal.NewFromInt(maxInt64)) > 0 || number.Cmp(decimal.NewFromInt(minInt64)) < 0 {
		return fallback
	}
	return number.IntPart()
}

var _ ingestion.TargetUpserter = Upserter{}
