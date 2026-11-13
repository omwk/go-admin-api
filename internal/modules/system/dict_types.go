package system

import (
	"context"
	"encoding/json"
	"time"
)

// DictScopeSystem identifies code-defined dictionary values.
const DictScopeSystem = "system"

// DictScopeBusiness identifies tenant-managed dictionary values.
const DictScopeBusiness = "business"

// Dict is a tenant-owned dictionary type; Builtin is server managed.
type Dict struct {
	ID        int64     `json:"id"`
	TenantID  int64     `json:"tenant_id"`
	Type      string    `json:"type"`
	Name      string    `json:"name"`
	Status    string    `json:"status"`
	Scope     string    `json:"scope" gorm:"default:business"`
	Builtin   bool      `json:"builtin"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// TableName implements the persistence mapping.
func (Dict) TableName() string { return "sys_dict" }

// DictItem is one tenant-owned dictionary value.
type DictItem struct {
	ID       int64           `json:"id"`
	TenantID int64           `json:"tenant_id"`
	DictID   int64           `json:"dict_id"`
	Label    string          `json:"label"`
	Value    string          `json:"value"`
	Sort     int             `json:"sort"`
	Status   string          `json:"status"`
	Color    string          `json:"color"`
	Extra    json.RawMessage `json:"extra"`
	Builtin  bool            `json:"builtin"`
}

// TableName implements the persistence mapping.
func (DictItem) TableName() string { return "sys_dict_item" }

// DictRequest contains all editable dictionary fields.
type DictRequest struct {
	Type   string `json:"type"`
	Name   string `json:"name"`
	Status string `json:"status"`
}

// DictItemRequest excludes tenant, parent and builtin ownership fields.
type DictItemRequest struct {
	Label  string          `json:"label"`
	Value  string          `json:"value"`
	Sort   int             `json:"sort"`
	Status string          `json:"status"`
	Color  string          `json:"color"`
	Extra  json.RawMessage `json:"extra"`
}

// MetaItem is the safe dropdown projection.
type MetaItem struct {
	Scope string          `json:"scope"`
	Label string          `json:"label"`
	Value string          `json:"value"`
	Sort  int             `json:"sort"`
	Color string          `json:"color"`
	Extra json.RawMessage `json:"extra"`
}

// DictStore keeps tenant predicates and transaction state inside the persistence adapter.
type DictStore interface {
	Transaction(context.Context, func(context.Context) error) error
	LockDicts(context.Context, int64) error
	Dictionary(context.Context, int64, int64, bool) (Dict, error)
	DictionaryByType(context.Context, int64, string) (Dict, error)
	Dictionaries(context.Context, int64, string, int, int) (Page[Dict], error)
	SaveDictionary(context.Context, *Dict, bool) error
	DeleteDictionary(context.Context, int64, int64) error
	DictionaryItems(context.Context, int64, int64, int, int, bool) (Page[DictItem], error)
	DictionaryItem(context.Context, int64, int64) (DictItem, error)
	SaveDictionaryItem(context.Context, *DictItem, bool) error
	DeleteDictionaryItem(context.Context, int64, int64) error
}
