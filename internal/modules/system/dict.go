package system

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"strconv"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/omwk/go-admin-api/internal/kernel/audit"
	"github.com/omwk/go-admin-api/internal/kernel/errs"
)

// DictService owns dictionary transactions and the bounded tenant dropdown cache.
type DictService struct {
	store  DictStore
	record func(context.Context, audit.Event) error
	mu     sync.Mutex
	cache  map[string]dictCache
	now    func() time.Time
}

type dictCache struct {
	tenant  int64
	items   []MetaItem
	expires time.Time
}

var dictType = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)

// NewDictService requires storage and a persistent transactional audit sink.
func NewDictService(store DictStore, record func(context.Context, audit.Event) error) (*DictService, error) {
	if store == nil || record == nil {
		return nil, errors.New("dictionary dependencies are required")
	}
	return &DictService{store: store, record: record, cache: map[string]dictCache{}, now: time.Now}, nil
}

// List returns the tenant's dictionary types with standard pagination.
func (s *DictService) List(ctx context.Context, actor Actor, page, size int) (Page[Dict], error) {
	if actor.TenantID <= 0 {
		return Page[Dict]{}, notFound()
	}
	return s.store.Dictionaries(ctx, actor.TenantID, page, size)
}

// Items verifies parent ownership before listing its values.
func (s *DictService) Items(ctx context.Context, actor Actor, id int64, page, size int) (Page[DictItem], error) {
	if _, err := s.store.Dictionary(ctx, actor.TenantID, id, false); err != nil {
		return Page[DictItem]{}, err
	}
	return s.store.DictionaryItems(ctx, actor.TenantID, id, page, size, false)
}

func validDict(r DictRequest) bool {
	return dictType.MatchString(r.Type) && validText(r.Name, 1, 256) && validStatus(r.Status)
}
func validStatus(status string) bool { return status == "active" || status == "disabled" }
func validText(s string, minimum, maximum int) bool {
	n := utf8.RuneCountInString(s)
	return utf8.ValidString(s) && n >= minimum && n <= maximum
}

func normalizeItem(r *DictItemRequest) error {
	if !validText(r.Label, 1, 256) || !validText(r.Value, 1, 128) || !validStatus(r.Status) || !validText(r.Color, 0, 64) || r.Sort < -2147483648 || r.Sort > 2147483647 {
		return invalidDict()
	}
	if len(r.Extra) == 0 {
		r.Extra = json.RawMessage("{}")
	}
	var extra map[string]any
	if len(r.Extra) > 65536 || json.Unmarshal(r.Extra, &extra) != nil || extra == nil {
		return invalidDict()
	}
	return nil
}

func invalidDict() error                { return errs.New(errs.ErrInvalidParam, 400, "字典参数校验失败") }
func dictConflict(message string) error { return errs.New(errs.ErrConflict, 409, message) }

func (s *DictService) write(ctx context.Context, actor Actor, run func(context.Context) error) error {
	if actor.TenantID <= 0 || actor.UserID <= 0 {
		return notFound()
	}
	err := s.store.Transaction(ctx, func(tx context.Context) error {
		if err := s.store.LockDicts(tx, actor.TenantID); err != nil {
			return err
		}
		return run(tx)
	})
	if err == nil {
		s.Invalidate(actor.TenantID)
	}
	return err
}

func (s *DictService) audit(ctx context.Context, actor Actor, action, resource string, id int64, before, after any, removed bool) error {
	old, err := dictSnapshot(before)
	if err != nil {
		return err
	}
	next, err := dictSnapshot(after)
	if err != nil {
		return err
	}
	return s.record(ctx, audit.Event{TenantID: actor.TenantID, ActorType: "user", ActorID: &actor.UserID, Action: action, ResourceType: resource, ResourceID: strconv.FormatInt(id, 10), Before: old, After: next, AfterNull: removed, Result: "success"})
}

func dictSnapshot(value any) (map[string]any, error) {
	if value == nil {
		return nil, nil
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var out map[string]any
	if err := json.Unmarshal(encoded, &out); err != nil {
		return nil, err
	}
	if extra, ok := out["extra"]; ok {
		raw, err := json.Marshal(extra)
		if err != nil {
			return nil, err
		}
		out["extra"] = map[string]any{"bytes": len(raw)}
	}
	return out, nil
}
