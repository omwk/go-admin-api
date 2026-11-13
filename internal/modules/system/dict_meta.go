package system

import (
	"context"
	"encoding/json"
	"strconv"
	"time"
)

// Invalidate discards a tenant's cached dictionaries after a committed write.
func (s *DictService) Invalidate(tenant int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for key, entry := range s.cache {
		if entry.tenant == tenant {
			delete(s.cache, key)
		}
	}
}

// Meta returns only enabled dropdown values; its cache is tenant-scoped and bounded.
func (s *DictService) Meta(ctx context.Context, actor Actor, kind string) ([]MetaItem, error) {
	if actor.TenantID <= 0 {
		return nil, notFound()
	}
	if !dictType.MatchString(kind) {
		return nil, invalidDict()
	}
	key := strconv.FormatInt(actor.TenantID, 10) + ":" + kind
	// Holding the mutex through a cache fill prevents a pre-commit read from repopulating after invalidation.
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	if entry, ok := s.cache[key]; ok && now.Before(entry.expires) {
		return cloneMeta(entry.items), nil
	}
	dict, err := s.store.DictionaryByType(ctx, actor.TenantID, kind)
	if err != nil {
		return nil, err
	}
	items := []MetaItem{}
	if dict.Status == "active" {
		rows, err := s.store.DictionaryItems(ctx, actor.TenantID, dict.ID, 1, 0, true)
		if err != nil {
			return nil, err
		}
		for _, row := range rows.List {
			items = append(items, MetaItem{Label: row.Label, Value: row.Value, Sort: row.Sort, Color: row.Color, Extra: row.Extra})
		}
	}
	for cached, entry := range s.cache {
		if !now.Before(entry.expires) {
			delete(s.cache, cached)
		}
	}
	if len(s.cache) >= 256 {
		clear(s.cache)
	}
	s.cache[key] = dictCache{tenant: actor.TenantID, items: cloneMeta(items), expires: now.Add(60 * time.Second)}
	return items, nil
}

func cloneMeta(items []MetaItem) []MetaItem {
	result := make([]MetaItem, len(items))
	copy(result, items)
	for i := range result {
		result[i].Extra = append(json.RawMessage(nil), items[i].Extra...)
	}
	return result
}
