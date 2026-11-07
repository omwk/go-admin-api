package sync

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/omwk/go-admin-api/internal/modules/ingestion"
	"github.com/omwk/go-admin-api/internal/modules/sync/repo"
	"github.com/shopspring/decimal"
)

func interpret(work repo.Work, ref ingestion.RawRef, raw json.RawMessage, handler ResourceHandler) repo.Disposition {
	item := repo.Disposition{Raw: ref}
	metadata := ingestion.Mapped{TenantID: work.Job.TenantID, ShopID: work.Job.ShopID, PlatformID: work.Job.PlatformID,
		Replay:     work.Run.Stats.ReplayID > 0,
		ExternalID: ref.ExternalID, RawID: ref.ID, SourceFetchedAt: ref.FetchedAt, SourceOrder: ref.ID, MappingChecksum: work.Mapping.Checksum}
	mapped, fields, err := work.Mapping.ApplyMapped(raw, metadata)
	if err != nil {
		item.Dead, item.Reason, item.Mapped = true, "mapping_error", metadata
		return item
	}
	item.Mapped = mapped
	deleted, _ := mapped.Values["deleted"].(bool)
	for _, field := range fields {
		if work.Job.Resource == "order" && deleted && field.Kind == "missing_required" {
			continue
		}
		if !field.Fatal {
			continue
		}
		switch field.Kind {
		case "missing_required", "type_mismatch", "invalid_value":
			item.Reason = field.Kind
		default:
			item.Dead, item.Reason = true, "mapping_error"
		}
		return item
	}
	item.Reason = handler.Validate(mapped)
	return item
}

// InterpretReplay reuses the normal mapping and resource validation pipeline.
func InterpretReplay(tenant, platform, shop int64, mapping *ingestion.Compiled, ref ingestion.RawRef, raw json.RawMessage, handler ResourceHandler) repo.Disposition {
	return interpret(repo.Work{Job: repo.Job{TenantID: tenant, PlatformID: platform, ShopID: shop, Resource: mapping.Resource()}, Run: repo.Run{Stats: repo.Stats{ReplayID: 1}}, Mapping: mapping}, ref, raw, handler)
}

// InterpretFetched applies the same frozen mapping and validation to a normal observation.
func InterpretFetched(work repo.Work, ref ingestion.RawRef, raw json.RawMessage, handler ResourceHandler) repo.Disposition {
	return interpret(work, ref, raw, handler)
}

// Check canonical types before the target writer's permissive conversions.
func validateProduct(values map[string]any) string {
	for _, key := range []string{"title", "currency", "status"} {
		value, exists := values[key]
		if !exists || value == nil {
			return "missing_required"
		}
		text, ok := value.(string)
		if !ok {
			return "type_mismatch"
		}
		if strings.TrimSpace(text) == "" {
			return "missing_required"
		}
	}
	status := values["status"].(string)
	if status != "online" && status != "offline" && status != "deleted" {
		return "invalid_value"
	}
	if len(values["currency"].(string)) != 3 {
		return "invalid_value"
	}
	for _, key := range []string{"price_cents", "stock_total"} {
		value, exists := values[key]
		if !exists {
			if key == "price_cents" {
				return "missing_required"
			}
			continue
		}
		if !canonicalInteger(value) {
			return "type_mismatch"
		}
	}
	return ""
}

func validateChildren(children []map[string]any) string {
	for _, child := range children {
		value := child["external_sku_id"]
		if value == nil {
			return "missing_required"
		}
		id, ok := value.(string)
		if !ok {
			return "type_mismatch"
		}
		if strings.TrimSpace(id) == "" {
			return "missing_required"
		}
		for _, key := range []string{"price_cents", "stock"} {
			if v, exists := child[key]; exists && !canonicalInteger(v) {
				return "type_mismatch"
			}
		}
	}
	return ""
}

func canonicalInteger(value any) bool {
	switch value.(type) {
	case int, int32, int64, json.Number, decimal.Decimal:
	default:
		return false
	}
	n, err := decimal.NewFromString(fmt.Sprint(value))
	return err == nil && n.IsInteger() && n.GreaterThanOrEqual(decimal.NewFromInt(-1<<63)) && n.LessThanOrEqual(decimal.NewFromInt(1<<63-1))
}
