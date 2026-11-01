package bootstrap

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/omwk/go-admin-api/internal/kernel/db"
	"github.com/omwk/go-admin-api/internal/modules/identity/model"
	"github.com/omwk/go-admin-api/internal/modules/identity/repo"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const permissionReleaseGroup = "permission_release"

type permissionReleaseStore struct{ database *db.DB }

// NewPermissionReleaseStore exposes the internal release persistence port to the CLI.
func NewPermissionReleaseStore(database *db.DB) repo.PermissionReleaseStore {
	return &permissionReleaseStore{database: database}
}

func (s *permissionReleaseStore) raw(ctx context.Context) (*gorm.DB, context.CancelFunc) {
	return s.database.WithContext(ctx)
}

func (s *permissionReleaseStore) Transaction(ctx context.Context, run func(context.Context) error) error {
	return s.database.Transaction(ctx, run)
}

func (s *permissionReleaseStore) LockPermissionRelease(ctx context.Context, tenant int64) error {
	if tenant <= 0 {
		return errors.New("release tenant is required")
	}
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	return q.Exec("SELECT pg_advisory_xact_lock(hashtextextended(?, 0))", fmt.Sprintf("identity:management:%d", tenant)).Error
}

func (s *permissionReleaseStore) RoleByCode(ctx context.Context, tenant int64, code string) (model.Role, error) {
	q, cancel := s.raw(ctx)
	defer cancel()
	var row model.Role
	err := q.Select(adminRoleColumns).Where("tenant_id = ? AND code = ?", tenant, code).Clauses(clause.Locking{Strength: "UPDATE"}).Take(&row).Error
	return row, adminDBError(err)
}

func (s *permissionReleaseStore) RolePermissionCodes(ctx context.Context, tenant, role int64) ([]string, error) {
	q, cancel := s.raw(ctx)
	defer cancel()
	var codes []string
	err := q.Table("sys_permission p").Select("p.code").Joins("JOIN sys_role_permission rp ON rp.tenant_id = p.tenant_id AND rp.permission_id = p.id").Where("p.tenant_id = ? AND rp.role_id = ?", tenant, role).Order("p.code").Pluck("p.code", &codes).Error
	return codes, err
}

func (s *permissionReleaseStore) PermissionIDs(ctx context.Context, tenant int64, codes []string) (map[string]int64, error) {
	result := make(map[string]int64, len(codes))
	if len(codes) == 0 {
		return result, nil
	}
	q, cancel := s.raw(ctx)
	defer cancel()
	var rows []model.Permission
	if err := q.Select("id, code").Where("tenant_id = ? AND code IN ?", tenant, codes).Find(&rows).Error; err != nil {
		return nil, err
	}
	for _, row := range rows {
		result[row.Code] = row.ID
	}
	return result, nil
}

func (s *permissionReleaseStore) InsertRolePermissions(ctx context.Context, tenant, role int64, ids []int64) error {
	if len(ids) == 0 {
		return nil
	}
	rows := make([]model.RolePermission, 0, len(ids))
	for _, id := range ids {
		if id <= 0 {
			return errors.New("permission id is required")
		}
		rows = append(rows, model.RolePermission{TenantID: tenant, RoleID: role, PermissionID: id})
	}
	q, cancel := s.raw(ctx)
	defer cancel()
	return adminDBError(q.Clauses(clause.OnConflict{DoNothing: true}).CreateInBatches(rows, 500).Error)
}

func (s *permissionReleaseStore) PermissionReleaseMarker(ctx context.Context, tenant int64, release string) (string, bool, error) {
	q, cancel := s.raw(ctx)
	defer cancel()
	var row struct {
		Value json.RawMessage `gorm:"column:value"`
	}
	err := q.Table("sys_setting").Select("value").Where("tenant_id = ? AND group_key = ? AND item_key = ?", tenant, permissionReleaseGroup, release).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	if len(row.Value) == 0 {
		return "", false, errors.New("permission release marker is empty")
	}
	var marker struct {
		Summary string `json:"summary"`
	}
	if err := json.Unmarshal(row.Value, &marker); err != nil || marker.Summary == "" {
		return "", false, errors.New("permission release marker is invalid")
	}
	return marker.Summary, true, nil
}

func (s *permissionReleaseStore) SavePermissionReleaseMarker(ctx context.Context, tenant int64, release, summary string) error {
	value, err := json.Marshal(map[string]string{"release": release, "summary": summary})
	if err != nil {
		return err
	}
	q, cancel := s.raw(ctx)
	defer cancel()
	return adminDBError(q.Exec(`INSERT INTO sys_setting (tenant_id, group_key, item_key, value)
		VALUES (?, ?, ?, ?::jsonb)`, tenant, permissionReleaseGroup, release, string(value)).Error)
}

func (s *permissionReleaseStore) PermissionReleaseUsers(ctx context.Context, tenant, role int64) ([]int64, error) {
	q, cancel := s.raw(ctx)
	defer cancel()
	var ids []int64
	err := q.Table("sys_user_role").Where("tenant_id = ? AND role_id = ?", tenant, role).Distinct().Order("user_id").Pluck("user_id", &ids).Error
	return ids, err
}

var _ interface {
	LockPermissionRelease(context.Context, int64) error
	RoleByCode(context.Context, int64, string) (model.Role, error)
	RolePermissionCodes(context.Context, int64, int64) ([]string, error)
	PermissionIDs(context.Context, int64, []string) (map[string]int64, error)
	InsertRolePermissions(context.Context, int64, int64, []int64) error
	PermissionReleaseMarker(context.Context, int64, string) (string, bool, error)
	SavePermissionReleaseMarker(context.Context, int64, string, string) error
	PermissionReleaseUsers(context.Context, int64, int64) ([]int64, error)
} = (*permissionReleaseStore)(nil)
