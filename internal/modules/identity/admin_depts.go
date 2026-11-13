package identity

import (
	"context"
	"strconv"
	"unicode/utf8"

	"github.com/omwk/go-admin-api/internal/kernel/errs"
	"github.com/omwk/go-admin-api/internal/modules/identity/model"
	"github.com/omwk/go-admin-api/internal/modules/identity/permission"
	"github.com/omwk/go-admin-api/internal/modules/identity/repo"
)

// DepartmentRequest contains editable department fields; path is always server managed.
type DepartmentRequest struct {
	ParentID int64  `json:"parent_id" binding:"gte=0"`
	Name     string `json:"name" binding:"required,min=1,max=64"`
	Sort     int    `json:"sort" binding:"gte=-2147483648,lte=2147483647"`
	Status   string `json:"status" binding:"required,oneof=active disabled"`
}

// CreateDepartment inserts and audits a department inside Execute's management transaction.
func (s *AdminService) CreateDepartment(ctx context.Context, actor Overview, request DepartmentRequest) (repo.DepartmentRow, error) {
	rows, err := s.store.LockDepartments(ctx, actor.User.TenantID)
	if err != nil {
		return repo.DepartmentRow{}, err
	}
	if request.ParentID == 0 {
		return repo.DepartmentRow{}, errs.New(errs.ErrInvalidParam, 400, "已存在根部门，请选择上级部门")
	}
	parent, err := validateDepartment(rows, 0, request)
	if err != nil {
		return repo.DepartmentRow{}, err
	}
	row := model.Department{TenantID: actor.User.TenantID, ParentID: request.ParentID, Name: request.Name, Sort: request.Sort, Status: request.Status}
	if err := s.store.CreateDepartment(ctx, &row); err != nil {
		return repo.DepartmentRow{}, err
	}
	row.Path = departmentPath(parent.Path, row.ID)
	if err := s.store.UpdateDepartment(ctx, row.TenantID, row.ID, map[string]any{"path": row.Path}); err != nil {
		return repo.DepartmentRow{}, err
	}
	err = s.record(ctx, actor, permission.SystemDeptCreate, "dept", row.ID, nil, departmentSnapshot(row), false)
	return departmentRow(row), err
}

// UpdateDepartment validates the locked graph and recomputes every descendant path atomically.
func (s *AdminService) UpdateDepartment(ctx context.Context, actor Overview, id int64, request DepartmentRequest) (repo.DepartmentRow, error) {
	rows, err := s.store.LockDepartments(ctx, actor.User.TenantID)
	if err != nil {
		return repo.DepartmentRow{}, err
	}
	row, found := findDepartment(rows, id)
	if !found {
		return repo.DepartmentRow{}, adminNotFound()
	}
	if request.ParentID == 0 && row.ParentID != 0 {
		return repo.DepartmentRow{}, errs.New(errs.ErrInvalidParam, 400, "根部门不可移动")
	}
	parent, err := validateDepartment(rows, id, request)
	if err != nil {
		return repo.DepartmentRow{}, err
	}
	updated := row
	updated.ParentID, updated.Name, updated.Sort, updated.Status = request.ParentID, request.Name, request.Sort, request.Status
	if row.ParentID != 0 {
		updated.Path = departmentPath(parent.Path, id)
	}
	values := map[string]any{"parent_id": updated.ParentID, "name": updated.Name, "sort": updated.Sort, "status": updated.Status, "path": updated.Path}
	if err := s.store.UpdateDepartment(ctx, row.TenantID, id, values); err != nil {
		return repo.DepartmentRow{}, err
	}
	if row.ParentID != updated.ParentID {
		if err := s.moveDepartmentChildren(ctx, actor, rows, updated); err != nil {
			return repo.DepartmentRow{}, err
		}
	}
	if err := s.record(ctx, actor, permission.SystemDeptUpdate, "dept", id, departmentSnapshot(row), departmentSnapshot(updated), false); err != nil {
		return repo.DepartmentRow{}, err
	}
	return s.Department(ctx, actor, id)
}

// DeleteDepartment checks tree/member references and audits the complete physically removed row.
func (s *AdminService) DeleteDepartment(ctx context.Context, actor Overview, id int64) error {
	rows, err := s.store.LockDepartments(ctx, actor.User.TenantID)
	if err != nil {
		return err
	}
	row, found := findDepartment(rows, id)
	if !found {
		return adminNotFound()
	}
	if row.ParentID == 0 {
		return errs.New(errs.ErrInvalidParam, 400, "根部门不可删除")
	}
	refs, err := s.store.References(ctx, row.TenantID, "department", id)
	if err != nil {
		return err
	}
	for _, check := range []struct{ key, message string }{
		{"children", "请先删除或移走子部门"}, {"users", "请先调整用户部门，保留的用户记录仍会阻止删除"}, {"roles", "部门仍被角色数据范围引用"},
	} {
		if refs[check.key] > 0 {
			return &errs.AppError{Code: errs.ErrConflict, HTTPStatus: 409, Message: check.message, Details: map[string]any{check.key: refs[check.key]}}
		}
	}
	if err := s.store.DeleteDepartment(ctx, row.TenantID, id); err != nil {
		return err
	}
	return s.record(ctx, actor, permission.SystemDeptDelete, "dept", id, departmentSnapshot(row), nil, true)
}

func validateDepartment(rows []model.Department, id int64, request DepartmentRequest) (model.Department, error) {
	if !request.valid() {
		return model.Department{}, invalidAdmin()
	}
	var parent model.Department
	if request.ParentID > 0 {
		var found bool
		parent, found = findDepartment(rows, request.ParentID)
		if !found {
			return parent, adminNotFound()
		}
	}
	// Follow parent IDs rather than trusting a path prefix when detecting cycles.
	seen := map[int64]bool{}
	for ancestor := parent; ancestor.ID > 0; {
		if ancestor.ID == id || seen[ancestor.ID] {
			return parent, invalidAdmin()
		}
		seen[ancestor.ID] = true
		ancestor, _ = findDepartment(rows, ancestor.ParentID)
	}
	for _, row := range rows {
		if row.ID != id && row.ParentID == request.ParentID && row.Name == request.Name {
			return parent, errs.New(errs.ErrInvalidParam, 400, "同一父部门下名称不可重复")
		}
	}
	return parent, nil
}

func (r DepartmentRequest) valid() bool {
	length := utf8.RuneCountInString(r.Name)
	return r.ParentID >= 0 && length >= 1 && length <= 64 && r.Sort >= -2147483648 && r.Sort <= 2147483647 && (r.Status == "active" || r.Status == "disabled")
}

func (s *AdminService) moveDepartmentChildren(ctx context.Context, actor Overview, rows []model.Department, root model.Department) error {
	children := map[int64][]model.Department{}
	for _, row := range rows {
		children[row.ParentID] = append(children[row.ParentID], row)
	}
	queue := []model.Department{root}
	for len(queue) > 0 {
		parent := queue[0]
		queue = queue[1:]
		for _, row := range children[parent.ID] {
			updated := row
			updated.Path = departmentPath(parent.Path, row.ID)
			if err := s.store.UpdateDepartment(ctx, row.TenantID, row.ID, map[string]any{"path": updated.Path}); err != nil {
				return err
			}
			if err := s.record(ctx, actor, permission.SystemDeptUpdate, "dept", row.ID, departmentSnapshot(row), departmentSnapshot(updated), false); err != nil {
				return err
			}
			queue = append(queue, updated)
		}
	}
	return nil
}

func findDepartment(rows []model.Department, id int64) (model.Department, bool) {
	for _, row := range rows {
		if row.ID == id {
			return row, true
		}
	}
	return model.Department{}, false
}

func departmentPath(parent string, id int64) string {
	path := strconv.FormatInt(id, 10)
	if parent != "" {
		path = parent + "." + path
	}
	return path
}

func departmentRow(row model.Department) repo.DepartmentRow {
	return repo.DepartmentRow{ID: row.ID, ParentID: row.ParentID, Name: row.Name, Path: row.Path, Sort: row.Sort, Status: row.Status}
}

func departmentSnapshot(row model.Department) map[string]any {
	return map[string]any{"id": row.ID, "tenant_id": row.TenantID, "parent_id": row.ParentID, "name": row.Name, "path": row.Path, "sort": row.Sort, "status": row.Status}
}
