package identity

import (
	"github.com/omwk/go-admin-api/internal/kernel/errs"
	"github.com/omwk/go-admin-api/internal/modules/identity/model"
)

// ValidateMenuMove validates the full tenant graph read under the tree transaction lock.
// id=0 denotes a new node; existing identifiers must be present in rows.
func ValidateMenuMove(rows []model.Menu, id, parent int64) error {
	parents := make(map[int64]int64, len(rows)+1)
	for _, row := range rows {
		parents[row.ID] = row.ParentID
	}
	if _, ok := parents[id]; id != 0 && !ok {
		return adminNotFound()
	}
	if _, ok := parents[parent]; parent != 0 && !ok {
		return adminNotFound()
	}
	if id == parent && id != 0 {
		return errs.New(20106, 400, "菜单父子成环")
	}
	// A negative temporary identifier cannot collide with generated database identifiers.
	if id == 0 {
		id = -1
	}
	parents[id] = parent
	graph := menuGraph{parents: parents, depths: map[int64]int{}, visiting: map[int64]bool{}}
	// Compute all depths before checking the limit so cycles always report 20106.
	for node := range parents {
		if _, err := graph.depth(node); err != nil {
			return err
		}
	}
	for _, d := range graph.depths {
		if d > 5 {
			return errs.New(errs.ErrInvalidParam, 400, "菜单深度不能超过五层")
		}
	}
	return nil
}

type menuGraph struct {
	parents  map[int64]int64
	depths   map[int64]int
	visiting map[int64]bool
}

func (g menuGraph) depth(node int64) (int, error) {
	if node == 0 {
		return 0, nil
	}
	if g.visiting[node] {
		return 0, errs.New(20106, 400, "菜单父子成环")
	}
	if d, ok := g.depths[node]; ok {
		return d, nil
	}
	parent, exists := g.parents[node]
	if !exists {
		return 0, adminNotFound()
	}
	g.visiting[node] = true
	d, err := g.depth(parent)
	if err != nil {
		return 0, err
	}
	g.visiting[node] = false
	g.depths[node] = d + 1
	return d + 1, nil
}

func adminNotFound() error { return errs.New(errs.ErrNotFound, 404, "资源不存在") }
