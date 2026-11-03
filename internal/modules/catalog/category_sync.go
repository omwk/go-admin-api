package catalog

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/omwk/go-admin-api/internal/modules/ingestion"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// CategoryMappingCandidate is a recommendation that has not been applied.
type CategoryMappingCandidate struct {
	CategoryID int64   `json:"category_id"`
	Name       string  `json:"name"`
	Path       string  `json:"path"`
	Confidence float64 `json:"confidence"`
}

// MappingPoolItem combines an unmatched platform category with current suggestions.
type MappingPoolItem struct {
	Mapping    CategoryMapping            `json:"mapping"`
	Platform   PlatformCategory           `json:"platform_category"`
	Candidates []CategoryMappingCandidate `json:"candidates"`
}

// CategoryUpserter persists platform category projections and preserves existing mappings.
type CategoryUpserter struct{}

// Upsert stores mapped categories in the caller's transaction.
func (CategoryUpserter) Upsert(ctx context.Context, tx *gorm.DB, record ingestion.Mapped) (ingestion.UpsertStats, error) {
	if tx == nil {
		return ingestion.UpsertStats{}, errors.New("category transaction is required")
	}
	if err := ctx.Err(); err != nil {
		return ingestion.UpsertStats{}, err
	}
	if _, err := upsertPlatformCategory(ctx, tx, record); err != nil {
		return ingestion.UpsertStats{}, err
	}
	return ingestion.UpsertStats{Updated: 1}, nil
}

func upsertPlatformCategory(ctx context.Context, tx *gorm.DB, record ingestion.Mapped) (int, error) {
	name := mappedString(record.Values["name"])
	if name == "" || record.ExternalID == "" {
		return 0, errors.New("category external id and name are required")
	}
	parentExternalID := mappedExtString(record.Values, "parent_external_id")
	var parent PlatformCategory
	path := "/" + name
	if parentExternalID != "" {
		if err := tx.WithContext(ctx).Select("id, path").Where("tenant_id=? AND platform_id=? AND external_id=?", record.TenantID, record.PlatformID, parentExternalID).Take(&parent).Error; err != nil {
			return 0, fmt.Errorf("resolve category parent: %w", err)
		}
		path = strings.TrimRight(parent.Path, "/") + "/" + name
		if err := tx.WithContext(ctx).Model(&PlatformCategory{}).Where("id=?", parent.ID).Update("is_leaf", false).Error; err != nil {
			return 0, err
		}
	}
	row := PlatformCategory{TenantID: record.TenantID, PlatformID: record.PlatformID, ExternalID: record.ExternalID,
		ParentExternalID: parentExternalID, Name: name, Path: path, IsLeaf: true, SyncedAt: time.Now().UTC()}
	result := tx.WithContext(ctx).Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "tenant_id"}, {Name: "platform_id"}, {Name: "external_id"}}, DoUpdates: clause.Assignments(map[string]any{
		"parent_external_id": row.ParentExternalID, "name": row.Name, "path": row.Path, "is_leaf": row.IsLeaf, "synced_at": row.SyncedAt,
	})}).Create(&row)
	if result.Error != nil {
		return 0, result.Error
	}
	var stored PlatformCategory
	if err := tx.WithContext(ctx).Select("id").Where("tenant_id=? AND platform_id=? AND external_id=?", record.TenantID, record.PlatformID, record.ExternalID).Take(&stored).Error; err != nil {
		return 0, err
	}
	if err := tx.WithContext(ctx).Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "tenant_id"}, {Name: "platform_category_id"}}, DoNothing: true}).Create(&CategoryMapping{
		TenantID: record.TenantID, PlatformID: record.PlatformID, PlatformCategoryID: stored.ID, Confidence: 0, Source: "default",
	}).Error; err != nil {
		return 0, err
	}
	if err := applyExactCategoryMapping(ctx, tx, record.TenantID, stored.ID, record.PlatformID, path, name); err != nil {
		return 0, err
	}
	return 1, nil
}

func applyExactCategoryMapping(ctx context.Context, tx *gorm.DB, tenantID, platformCategoryID, platformID int64, path, name string) error {
	var matches []Category
	if err := tx.WithContext(ctx).Select("id").Where("tenant_id=? AND path=? AND name=? AND status='active'", tenantID, path, name).Find(&matches).Error; err != nil {
		return err
	}
	if len(matches) != 1 {
		return nil
	}
	return tx.WithContext(ctx).Model(&CategoryMapping{}).Where("tenant_id=? AND platform_id=? AND platform_category_id=? AND source <> 'manual' AND category_id IS NULL", tenantID, platformID, platformCategoryID).
		Updates(map[string]any{"category_id": matches[0].ID, "confidence": 0.9, "source": "rule", "updated_at": time.Now().UTC()}).Error
}

func mappedExtString(values map[string]any, key string) string {
	ext, _ := values["ext"].(map[string]any)
	return mappedString(ext[key])
}

func mappedString(value any) string {
	text, _ := value.(string)
	return strings.TrimSpace(text)
}

// AutoApplyExactMappings applies only unique path-and-name matches. Parent suggestions remain candidates.
func AutoApplyExactMappings(ctx context.Context, tx *gorm.DB, tenantID, platformID int64) (int64, error) {
	var rows []struct {
		MappingID          int64
		PlatformCategoryID int64
		Path               string
		Name               string
	}
	err := tx.WithContext(ctx).Table("category_mapping m").Select("m.id AS mapping_id, p.id AS platform_category_id, p.path, p.name").
		Joins("JOIN platform_category p ON p.id=m.platform_category_id AND p.tenant_id=m.tenant_id").
		Where("m.tenant_id=? AND m.platform_id=? AND m.category_id IS NULL AND m.source <> 'manual'", tenantID, platformID).Order("p.id").Find(&rows).Error
	if err != nil {
		return 0, err
	}
	var changed int64
	for _, row := range rows {
		var matches []Category
		if err := tx.WithContext(ctx).Select("id").Where("tenant_id=? AND path=? AND name=? AND status='active'", tenantID, row.Path, row.Name).Find(&matches).Error; err != nil {
			return changed, err
		}
		if len(matches) != 1 {
			continue
		}
		result := tx.WithContext(ctx).Model(&CategoryMapping{}).Where("id=? AND tenant_id=? AND source <> 'manual' AND category_id IS NULL", row.MappingID, tenantID).
			Updates(map[string]any{"category_id": matches[0].ID, "confidence": 0.9, "source": "rule", "updated_at": time.Now().UTC()})
		if result.Error != nil {
			return changed, result.Error
		}
		changed += result.RowsAffected
	}
	return changed, nil
}

// ParentCandidates returns mapped internal children with a 0.6 recommendation, without applying it.
func ParentCandidates(ctx context.Context, tx *gorm.DB, tenantID int64, platform PlatformCategory) ([]CategoryMappingCandidate, error) {
	if platform.ParentExternalID == "" {
		return []CategoryMappingCandidate{}, nil
	}
	var parent struct{ CategoryID *int64 }
	err := tx.WithContext(ctx).Table("category_mapping m").Select("m.category_id").
		Joins("JOIN platform_category p ON p.id=m.platform_category_id AND p.tenant_id=m.tenant_id").
		Where("m.tenant_id=? AND m.platform_id=? AND p.external_id=? AND m.category_id IS NOT NULL", tenantID, platform.PlatformID, platform.ParentExternalID).Take(&parent).Error
	if errors.Is(err, gorm.ErrRecordNotFound) || (err == nil && parent.CategoryID == nil) {
		return []CategoryMappingCandidate{}, nil
	}
	if err != nil {
		return nil, err
	}
	var rows []Category
	err = tx.WithContext(ctx).Select("id, name, path").Where("tenant_id=? AND parent_id=? AND name=? AND status='active'", tenantID, *parent.CategoryID, platform.Name).Order("id").Find(&rows).Error
	if err != nil {
		return nil, err
	}
	out := make([]CategoryMappingCandidate, 0, len(rows))
	for _, row := range rows {
		out = append(out, CategoryMappingCandidate{CategoryID: row.ID, Name: row.Name, Path: row.Path, Confidence: 0.6})
	}
	return out, nil
}
