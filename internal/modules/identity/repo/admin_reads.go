package repo

import (
	"context"

	"github.com/omwk/go-admin-api/internal/modules/identity/model"
)

// DepartmentRow contains department metadata and its direct, non-deleted member count.
type DepartmentRow struct {
	ID          int64  `json:"id"`
	ParentID    int64  `json:"parent_id"`
	Name        string `json:"name"`
	Path        string `json:"path"`
	Sort        int    `json:"sort"`
	Status      string `json:"status"`
	MemberCount int64  `json:"member_count"`
}

// AdminReader provides scope-protected reads without write locks or transactions.
type AdminReader interface {
	ReadUser(context.Context, int64, int64) (model.User, error)
	ReadRole(context.Context, int64, int64) (model.Role, error)
	ReadMenu(context.Context, int64, int64) (model.Menu, error)
	ReadLinks(context.Context, int64, LinkKind, int64) ([]int64, error)
	Departments(context.Context, int64, int64) ([]DepartmentRow, error)
}
