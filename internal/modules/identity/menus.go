package identity

import (
	"encoding/json"
	"sort"

	"github.com/omwk/go-admin-api/internal/modules/identity/model"
)

// MenuNode is a navigation item; button and inaccessible ancestor branches are omitted.
type MenuNode struct {
	ID             int64           `json:"id"`
	ParentID       int64           `json:"parent_id"`
	Name           string          `json:"name"`
	Type           string          `json:"type"`
	Path           *string         `json:"path,omitempty"`
	Component      *string         `json:"component,omitempty"`
	Redirect       *string         `json:"redirect,omitempty"`
	Icon           *string         `json:"icon,omitempty"`
	Sort           int             `json:"sort"`
	KeepAlive      bool            `json:"keep_alive"`
	IsExternal     bool            `json:"is_external"`
	PermissionCode *string         `json:"permission_code,omitempty"`
	Meta           json.RawMessage `json:"meta"`
	Children       []MenuNode      `json:"children"`
}

// BuildMenuTree assembles a stable tree with a maximum depth of five entirely in memory.
func BuildMenuTree(rows []model.Menu, grants []int64, super bool) []MenuNode {
	allowed := make(map[int64]bool, len(grants))
	for _, id := range grants {
		allowed[id] = true
	}
	children := map[int64][]model.Menu{}
	for _, row := range rows {
		if row.Status != "active" || !row.Visible || row.Type == "button" || (!super && !allowed[row.ID]) {
			continue
		}
		children[row.ParentID] = append(children[row.ParentID], row)
	}
	for parent := range children {
		sort.Slice(children[parent], func(i, j int) bool {
			a, b := children[parent][i], children[parent][j]
			if a.Sort == b.Sort {
				return a.ID < b.ID
			}
			return a.Sort < b.Sort
		})
	}
	var walk func(int64, int) []MenuNode
	walk = func(parent int64, depth int) []MenuNode {
		nodes := []MenuNode{}
		if depth > 5 {
			return nodes
		}
		for _, row := range children[parent] {
			meta := row.Meta
			if len(meta) == 0 {
				meta = json.RawMessage("{}")
			}
			nodes = append(nodes, MenuNode{ID: row.ID, ParentID: row.ParentID, Name: row.Name, Type: row.Type, Path: row.Path, Component: row.Component, Redirect: row.Redirect, Icon: row.Icon, Sort: row.Sort, KeepAlive: row.KeepAlive, IsExternal: row.IsExternal, PermissionCode: row.PermissionCode, Meta: meta, Children: walk(row.ID, depth+1)})
		}
		return nodes
	}
	return walk(0, 1)
}
