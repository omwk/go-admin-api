// Package setting owns the registered tenant configuration schema and live policy reader.
package setting

import (
	"encoding/json"
	"errors"
	"math"
	"slices"
	"sort"
	"time"
	_ "time/tzdata" // Configuration validation must work on hosts without zoneinfo.
	"unicode/utf8"

	"github.com/omwk/go-admin-api/internal/kernel/errs"
)

// Item describes one supported configuration key. Bounds apply to numbers or string lengths.
type Item struct {
	Group     string   `json:"group"`
	Key       string   `json:"key"`
	Type      string   `json:"type"`
	Default   any      `json:"default"`
	Min       *float64 `json:"min,omitempty"`
	Max       *float64 `json:"max,omitempty"`
	Enum      []string `json:"enum,omitempty"`
	ReadOnly  bool     `json:"read_only"`
	Desc      string   `json:"desc"`
	HotReload bool     `json:"hot_reload"`
}

// Registry keeps immutable schemas, independent of stored tenant overrides.
type Registry struct{ items []Item }

// NewRegistry validates and owns its supplied schemas.
func NewRegistry(items []Item) (*Registry, error) {
	r := &Registry{}
	seen := map[string]bool{}
	for _, item := range items {
		key := item.Group + "/" + item.Key
		if item.Group == "" || item.Key == "" || seen[key] {
			return nil, errors.New("invalid or duplicate setting schema")
		}
		seen[key] = true
		if _, err := decode(item, mustJSON(item.Default)); err != nil {
			return nil, err
		}
		r.items = append(r.items, clone(item))
	}
	return r, nil
}

func mustJSON(value any) json.RawMessage {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil
	}
	return encoded
}

func clone(item Item) Item {
	item.Enum = slices.Clone(item.Enum)
	if item.Min != nil {
		value := *item.Min
		item.Min = &value
	}
	if item.Max != nil {
		value := *item.Max
		item.Max = &value
	}
	if item.Type == "json" {
		var owned any
		if json.Unmarshal(mustJSON(item.Default), &owned) == nil {
			item.Default = owned
		}
	}
	return item
}

// Groups returns a stable list of registered groups.
func (r *Registry) Groups() []string {
	groups := []string{}
	for _, item := range r.items {
		if !slices.Contains(groups, item.Group) {
			groups = append(groups, item.Group)
		}
	}
	sort.Strings(groups)
	return groups
}

// Items returns owned schemas for a group.
func (r *Registry) Items(group string) ([]Item, bool) {
	items := []Item{}
	for _, item := range r.items {
		if item.Group == group {
			items = append(items, clone(item))
		}
	}
	return items, len(items) > 0
}

// Validate rejects the whole update if any key or value is invalid.
func (r *Registry) Validate(group string, updates map[string]json.RawMessage) (map[string]any, error) {
	items, found := r.Items(group)
	if !found {
		return nil, errs.New(errs.ErrNotFound, 404, "配置分组不存在")
	}
	if len(updates) == 0 {
		return nil, Invalid(group, "", "配置项不能为空")
	}
	schemas := map[string]Item{}
	for _, item := range items {
		schemas[item.Key] = item
	}
	keys := make([]string, 0, len(updates))
	for key := range updates {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	values := map[string]any{}
	for _, key := range keys {
		item, ok := schemas[key]
		if !ok {
			return nil, Invalid(group, key, "未注册的配置项")
		}
		if item.ReadOnly {
			return nil, Invalid(group, key, "配置项为只读")
		}
		value, err := decode(item, updates[key])
		if err != nil {
			return nil, err
		}
		values[key] = value
	}
	return values, nil
}

// Decode validates a persisted value without rejecting read-only schemas.
func Decode(item Item, raw json.RawMessage) (any, error) { return decode(item, raw) }

func decode(item Item, raw json.RawMessage) (any, error) {
	var value any
	if json.Unmarshal(raw, &value) != nil || value == nil {
		return nil, Invalid(item.Group, item.Key, "配置值类型不正确")
	}
	valid := false
	switch item.Type {
	case "number":
		number, ok := value.(float64)
		valid = ok && math.Trunc(number) == number && within(item, number)
	case "boolean":
		_, valid = value.(bool)
	case "string":
		valid = validString(item, value)
	case "json":
		valid = true
	}
	if !valid {
		return nil, Invalid(item.Group, item.Key, "配置值类型或范围不正确")
	}
	return value, nil
}

func validString(item Item, value any) bool {
	text, ok := value.(string)
	valid := ok && within(item, float64(utf8.RuneCountInString(text))) && (len(item.Enum) == 0 || slices.Contains(item.Enum, text))
	if valid && item.Group == "system" && item.Key == "timezone" {
		_, err := time.LoadLocation(text)
		valid = text != "" && text != "Local" && err == nil
	}
	return valid
}

func within(item Item, n float64) bool {
	return (item.Min == nil || n >= *item.Min) && (item.Max == nil || n <= *item.Max)
}

// Invalid names the failing key without copying its submitted value into errors or logs.
func Invalid(group, key, message string) error {
	return &errs.AppError{Code: errs.ErrInvalidParam, HTTPStatus: 400, Message: message, Details: map[string]any{"group": group, "key": key}}
}
