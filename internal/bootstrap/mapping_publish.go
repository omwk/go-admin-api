package bootstrap

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/omwk/go-admin-api/internal/kernel/db"
	identityscope "github.com/omwk/go-admin-api/internal/modules/identity/scope"
	"github.com/omwk/go-admin-api/internal/modules/ingestion"
	shopmodel "github.com/omwk/go-admin-api/internal/modules/shop/model"
)

type mappingPublishBackend struct {
	database                  *db.DB
	profileID, expectedActive int64
	draft                     json.RawMessage
}

// PublishMapping persists a release and CASes its profile in one transaction.
// expectedActive is zero for a profile with no active version. The caller must
// supply verified human access; this composition port exposes no raw database.
func PublishMapping(ctx context.Context, database *db.DB, profileID, expectedActive int64, request ingestion.PublishRequest) (*ingestion.Compiled, ingestion.DryRunResult, error) {
	if _, err := identityscope.From(ctx); err != nil {
		return nil, ingestion.DryRunResult{}, err
	}
	if database == nil || profileID <= 0 || expectedActive < 0 {
		return nil, ingestion.DryRunResult{}, errors.New("mapping profile and expected active version are required")
	}
	backend := &mappingPublishBackend{database: database, profileID: profileID, expectedActive: expectedActive, draft: append(json.RawMessage(nil), request.Spec...)}
	return (ingestion.PublishGate{Backend: backend}).Publish(ctx, request)
}

func (s *mappingPublishBackend) StatusSnapshot(ctx context.Context) (map[string]map[string]string, error) {
	if err := engineQuery("MappingPublish.StatusSnapshot"); err != nil {
		return nil, err
	}
	a, err := identityscope.From(ctx)
	if err != nil {
		return nil, err
	}
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	var rows []struct {
		ItemKey string
		Value   json.RawMessage
	}
	if err := q.Table("sys_setting").Select("item_key,value").Where("tenant_id=? AND group_key=?", a.Tenant, "status_map").Find(&rows).Error; err != nil {
		return nil, err
	}
	tables := make(map[string]map[string]string, len(rows))
	for _, row := range rows {
		var table map[string]string
		if err := json.Unmarshal(row.Value, &table); err != nil {
			return nil, errors.New("invalid status table setting")
		}
		if table == nil {
			return nil, errors.New("status table setting must be an object")
		}
		tables[row.ItemKey] = table
	}
	return tables, nil
}

func (s *mappingPublishBackend) DryRunSamples(ctx context.Context, limit int) ([]json.RawMessage, error) {
	if err := engineQuery("MappingPublish.DryRunSamples"); err != nil {
		return nil, err
	}
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	q, err := shopScope(ctx, shopmodel.Shop{}, q.Table("shop"))
	if err != nil {
		return nil, err
	}
	var rows []struct{ Payload json.RawMessage }
	err = q.Select("r.payload").Joins("JOIN mapping_profile p ON p.tenant_id=shop.tenant_id AND p.platform_id=shop.platform_id").
		Joins("JOIN raw_payload r ON r.tenant_id=shop.tenant_id AND r.shop_id=shop.id AND r.resource_type=p.resource_type").
		Where("p.id=? AND p.status='active'", s.profileID).Order("r.fetched_at DESC,r.id DESC").Limit(min(limit, 20)).Find(&rows).Error
	if err != nil {
		return nil, err
	}
	samples := make([]json.RawMessage, len(rows))
	for i := range rows {
		samples[i] = rows[i].Payload
	}
	return samples, nil
}

func (s *mappingPublishBackend) CASActive(ctx context.Context, id int64, spec []byte, checksum string) error {
	if err := engineQuery("MappingPublish.CASActive"); err != nil {
		return err
	}
	a, err := identityscope.From(ctx)
	if err != nil {
		return err
	}
	program, err := ingestion.CompileFromVersion(spec)
	if err != nil {
		return err
	}
	if program.Checksum != checksum {
		return errors.New("mapping release checksum mismatch")
	}
	var final ingestion.Spec
	if err := json.Unmarshal(spec, &final); err != nil {
		return err
	}
	return s.database.Transaction(ctx, func(ctx context.Context) error {
		q, cancel := s.database.WithContext(ctx)
		defer cancel()
		// Recheck the draft itself so an edit during preflight cannot be lost.
		written := q.Table("mapping_version").Where("id=? AND tenant_id=? AND profile_id=? AND status='draft' AND spec=?::jsonb", id, a.Tenant, s.profileID, string(s.draft)).
			Updates(map[string]any{"spec": json.RawMessage(spec), "checksum": checksum, "status": "published", "published_by": a.User, "published_at": time.Now().UTC()})
		if written.Error != nil {
			return written.Error
		}
		if written.RowsAffected != 1 {
			return errors.New("mapping draft changed or is unavailable")
		}
		active := q.Table("mapping_profile").Where("id=? AND tenant_id=? AND status='active' AND resource_type=? AND COALESCE(active_version_id,0)=?", s.profileID, a.Tenant, final.Resource, s.expectedActive).
			Updates(map[string]any{"active_version_id": id, "updated_at": time.Now().UTC()})
		if active.Error != nil {
			return active.Error
		}
		if active.RowsAffected != 1 {
			return errors.New("mapping active version changed or is unavailable")
		}
		return nil
	})
}

var _ ingestion.PublishBackend = (*mappingPublishBackend)(nil)
