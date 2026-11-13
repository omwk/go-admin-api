package system

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"

	"github.com/omwk/go-admin-api/internal/kernel/audit"
	"github.com/omwk/go-admin-api/internal/kernel/errs"
	"github.com/omwk/go-admin-api/internal/kernel/setting"
)

// SettingStore persists overrides inside the service-owned transaction.
type SettingStore interface {
	Transaction(context.Context, func(context.Context) error) error
	LockSettings(context.Context, int64, string) error
	ReadSettings(context.Context, int64, string) (map[string]json.RawMessage, error)
	SaveSetting(context.Context, int64, int64, string, string, json.RawMessage) error
}

// SettingValue pairs a registered schema with its resolved tenant value.
type SettingValue struct {
	Schema setting.Item `json:"schema"`
	Value  any          `json:"value"`
}

// SettingGroup contains the complete registered schema and resolved values for one group.
type SettingGroup struct {
	Group string         `json:"group"`
	Items []SettingValue `json:"items"`
}

// SettingService validates every write against its immutable registry.
type SettingService struct {
	store    SettingStore
	registry *setting.Registry
	record   func(context.Context, audit.Event) error
}

// NewSettingService requires both transactional storage and a persistent audit recorder.
func NewSettingService(store SettingStore, registry *setting.Registry, record func(context.Context, audit.Event) error) (*SettingService, error) {
	if store == nil || registry == nil || record == nil {
		return nil, errors.New("setting dependencies are required")
	}
	return &SettingService{store: store, registry: registry, record: record}, nil
}

// Groups returns all registered groups without exposing unregistered persisted keys.
func (s *SettingService) Groups(ctx context.Context, actor Actor) ([]SettingGroup, error) {
	groups := []SettingGroup{}
	for _, name := range s.registry.Groups() {
		group, err := s.Group(ctx, actor, name)
		if err != nil {
			return nil, err
		}
		groups = append(groups, group)
	}
	return groups, nil
}

// Group overlays this tenant's rows on code defaults.
func (s *SettingService) Group(ctx context.Context, actor Actor, group string) (SettingGroup, error) {
	result := SettingGroup{Group: group, Items: []SettingValue{}}
	if actor.TenantID <= 0 {
		return result, notFound()
	}
	items, ok := s.registry.Items(group)
	if !ok {
		return result, notFound()
	}
	stored, err := s.store.ReadSettings(ctx, actor.TenantID, group)
	if err != nil {
		return result, err
	}
	for _, item := range items {
		value := item.Default
		if raw, ok := stored[item.Key]; ok {
			value, err = setting.Decode(item, raw)
			if err != nil {
				return result, err
			}
		}
		result.Items = append(result.Items, SettingValue{Schema: item, Value: value})
	}
	return result, nil
}

// Values implements setting.Reader for live consumers, using the same registry as HTTP writes.
func (s *SettingService) Values(ctx context.Context, tenant int64, group string) (map[string]any, error) {
	resolved, err := s.Group(ctx, Actor{TenantID: tenant}, group)
	if err != nil {
		return nil, err
	}
	values := map[string]any{}
	for _, item := range resolved.Items {
		values[item.Schema.Key] = item.Value
	}
	return values, nil
}

// Update validates the complete batch before changing any row and audits only value digests.
func (s *SettingService) Update(ctx context.Context, actor Actor, group string, updates map[string]json.RawMessage) (SettingGroup, error) {
	if actor.TenantID <= 0 || actor.UserID <= 0 {
		return SettingGroup{}, notFound()
	}
	values, err := s.registry.Validate(group, updates)
	if err != nil {
		return SettingGroup{}, err
	}
	var result SettingGroup
	err = s.store.Transaction(ctx, func(tx context.Context) error {
		if err := s.store.LockSettings(tx, actor.TenantID, group); err != nil {
			return err
		}
		before, err := s.Group(tx, actor, group)
		if err != nil {
			return err
		}
		keys := make([]string, 0, len(values))
		for key := range values {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			encoded, err := json.Marshal(values[key])
			if err != nil {
				return err
			}
			if err := s.store.SaveSetting(tx, actor.TenantID, actor.UserID, group, key, encoded); err != nil {
				return err
			}
		}
		result, err = s.Group(tx, actor, group)
		if err != nil {
			return err
		}
		old, err := settingSummary(before, keys)
		if err != nil {
			return err
		}
		next, err := settingSummary(result, keys)
		if err != nil {
			return err
		}
		return s.record(tx, audit.Event{TenantID: actor.TenantID, ActorType: "user", ActorID: &actor.UserID, Action: "system:setting:update", ResourceType: "setting", ResourceID: group, Before: old, After: next, Result: "success"})
	})
	return result, err
}

func settingSummary(group SettingGroup, keys []string) (map[string]any, error) {
	values := map[string]any{}
	for _, item := range group.Items {
		if !containsKey(keys, item.Schema.Key) {
			continue
		}
		encoded, err := json.Marshal(item.Value)
		if err != nil {
			return nil, err
		}
		digest := sha256.Sum256(encoded)
		values[item.Schema.Key] = map[string]any{"type": item.Schema.Type, "digest": hex.EncodeToString(digest[:])}
	}
	return map[string]any{"group": group.Group, "items": values}, nil
}

func containsKey(keys []string, key string) bool {
	_, ok := sort.Find(len(keys), func(i int) int {
		if keys[i] < key {
			return 1
		}
		if keys[i] > key {
			return -1
		}
		return 0
	})
	return ok
}
func notFound() error { return errs.New(errs.ErrNotFound, 404, "资源不存在") }
