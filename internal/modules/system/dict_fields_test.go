package system

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/omwk/go-admin-api/internal/kernel/audit"
	"github.com/omwk/go-admin-api/internal/kernel/errs"
	"github.com/stretchr/testify/require"
)

func TestSystemDictionaryWritableFields(t *testing.T) {
	// BE-IDN-008 §2.5/3.8: the centralized whitelist rejects every other field.
	for _, tc := range []struct {
		name     string
		row      any
		writable map[string]struct{}
		allowed  []string
	}{
		{"dictionary", Dict{Type: "product_status", Scope: DictScopeSystem, Builtin: true}, systemDictWritableFields, []string{"name", "status"}},
		{"item", DictItem{Value: "active", Builtin: true}, systemDictItemWritableFields, []string{"label", "sort", "color", "status", "extra"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.ElementsMatch(t, tc.allowed, dictWritableNames(tc.writable))
			source := reflect.ValueOf(tc.row)
			for i := 0; i < source.NumField(); i++ {
				field := source.Type().Field(i)
				t.Run(field.Tag.Get("json"), func(t *testing.T) {
					changed := reflect.New(source.Type()).Elem()
					changed.Set(source)
					mutateDictTestField(t, changed.Field(i))
					err := validateDictFields(tc.row, changed.Interface(), tc.writable)
					if _, allowed := tc.writable[field.Tag.Get("json")]; allowed {
						require.NoError(t, err)
						return
					}
					require.Equal(t, errs.ErrConflict, errs.From(err).Code)
					t.Log("rejected unlisted field")
				})
			}
		})
	}
	t.Run("future_field_defaults_to_rejected", func(t *testing.T) {
		type futureDict struct {
			Name   string `json:"name"`
			Future string `json:"future"`
		}
		err := validateDictFields(futureDict{Name: "before"}, futureDict{Name: "after", Future: "injected"}, systemDictWritableFields)
		require.Equal(t, errs.ErrConflict, errs.From(err).Code)
	})
}

func dictWritableNames(fields map[string]struct{}) []string {
	names := []string{}
	for name := range fields {
		names = append(names, name)
	}
	return names
}

func mutateDictTestField(t *testing.T, field reflect.Value) {
	t.Helper()
	switch field.Kind() {
	case reflect.String:
		field.SetString("changed")
	case reflect.Int, reflect.Int64:
		field.SetInt(field.Int() + 1)
	case reflect.Bool:
		field.SetBool(!field.Bool())
	case reflect.Slice:
		field.Set(reflect.ValueOf(json.RawMessage(`{"display":"custom"}`)))
	case reflect.Struct:
		field.Set(reflect.ValueOf(field.Interface().(time.Time).Add(time.Second)))
	default:
		t.Fatalf("add a mutation for new dictionary field %s", field.Type())
	}
}

type dictWriteStore struct {
	DictStore
	dict   Dict
	item   DictItem
	writes int
}

func (s *dictWriteStore) Transaction(ctx context.Context, run func(context.Context) error) error {
	return run(ctx)
}
func (s *dictWriteStore) LockDicts(context.Context, int64) error { return nil }
func (s *dictWriteStore) Dictionary(context.Context, int64, int64, bool) (Dict, error) {
	return s.dict, nil
}
func (s *dictWriteStore) DictionaryItem(context.Context, int64, int64) (DictItem, error) {
	return s.item, nil
}
func (s *dictWriteStore) SaveDictionary(_ context.Context, row *Dict, _ bool) error {
	s.dict = *row
	s.writes++
	return nil
}
func (s *dictWriteStore) SaveDictionaryItem(_ context.Context, row *DictItem, _ bool) error {
	s.item = *row
	s.writes++
	return nil
}

func TestSystemDictionaryServiceUpdateWhitelist(t *testing.T) {
	// BE-IDN-008 §3.8: callers can edit all display fields without changing machine identities.
	ctx := context.Background()
	store := &dictWriteStore{
		dict: Dict{ID: 1, TenantID: 1, Type: "product_status", Name: "Product", Status: "active", Scope: DictScopeSystem},
		item: DictItem{ID: 2, TenantID: 1, DictID: 1, Label: "Active", Value: "active", Status: "active", Extra: json.RawMessage("{}")},
	}
	service, err := NewDictService(store, func(context.Context, audit.Event) error { return nil })
	require.NoError(t, err)
	actor := Actor{TenantID: 1, UserID: 1}
	request := DictRequest{Type: "product_status", Name: "Products", Status: "disabled"}
	row, err := service.Update(ctx, actor, 1, request)
	require.NoError(t, err)
	require.Equal(t, request.Name, row.Name)
	require.Equal(t, request.Status, row.Status)
	itemRequest := DictItemRequest{Label: "Enabled", Value: "active", Sort: 31, Color: "purple", Status: "disabled", Extra: json.RawMessage(`{"icon":"check"}`)}
	item, err := service.UpdateItem(ctx, actor, 2, itemRequest)
	require.NoError(t, err)
	require.Equal(t, itemRequest, DictItemRequest{Label: item.Label, Value: item.Value, Sort: item.Sort, Color: item.Color, Status: item.Status, Extra: item.Extra})
	request.Type = "renamed"
	_, err = service.Update(ctx, actor, 1, request)
	require.Equal(t, errs.ErrConflict, errs.From(err).Code)
	itemRequest.Value = "changed"
	_, err = service.UpdateItem(ctx, actor, 2, itemRequest)
	require.Equal(t, errs.ErrConflict, errs.From(err).Code)
	require.Equal(t, 2, store.writes)
}
