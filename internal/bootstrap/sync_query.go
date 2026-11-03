package bootstrap

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/omwk/go-admin-api/internal/kernel/db"
	"github.com/omwk/go-admin-api/internal/kernel/scope"
	"github.com/omwk/go-admin-api/internal/modules/sync/repo"
	"gorm.io/gorm"
)

type syncStore struct{ database *db.DB }

// NewSyncStore exposes only the verified task resolution port.
func NewSyncStore(database *db.DB) repo.Store { return &syncStore{database: database} }

type resolvedTaskRow struct {
	TenantID      int64           `gorm:"column:tenant_id"`
	JobID         int64           `gorm:"column:job_id"`
	RunID         int64           `gorm:"column:run_id"`
	ShopID        int64           `gorm:"column:shop_id"`
	Resource      string          `gorm:"column:resource"`
	JobEnabled    bool            `gorm:"column:job_enabled"`
	ShopStatus    string          `gorm:"column:shop_status"`
	ShopDeletedAt *time.Time      `gorm:"column:shop_deleted_at"`
	PlatformState string          `gorm:"column:platform_state"`
	Capabilities  json.RawMessage `gorm:"column:capabilities"`
	Options       json.RawMessage `gorm:"column:options"`
}

func (s *syncStore) ResolveTask(ctx context.Context, tenantID, jobID, runID int64) (repo.ResolvedTask, error) {
	if s == nil || s.database == nil || tenantID <= 0 || jobID <= 0 || runID <= 0 {
		return repo.ResolvedTask{}, errors.New("sync task identifiers are required")
	}
	if _, err := scopeRegistryQuery("Sync.ResolveTask"); err != nil {
		return repo.ResolvedTask{}, err
	}
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	var row resolvedTaskRow
	err := q.Table("sync_job j").
		Select("j.tenant_id, j.id AS job_id, r.id AS run_id, j.shop_id, j.resource_type AS resource, j.enabled AS job_enabled, s.status AS shop_status, s.deleted_at AS shop_deleted_at, p.status AS platform_state, p.capabilities, j.options").
		Joins("JOIN sync_run r ON r.tenant_id = j.tenant_id AND r.job_id = j.id AND r.shop_id = j.shop_id AND r.resource_type = j.resource_type").
		Joins("JOIN shop s ON s.tenant_id = j.tenant_id AND s.id = j.shop_id").
		Joins("JOIN platform p ON p.id = s.platform_id").
		Where("j.tenant_id = ? AND j.id = ? AND r.id = ?", tenantID, jobID, runID).
		Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return repo.ResolvedTask{}, errors.New("sync task not found")
	}
	if err != nil {
		return repo.ResolvedTask{}, err
	}
	if !row.JobEnabled || row.ShopStatus != "active" || row.ShopDeletedAt != nil || row.PlatformState != "active" || !scope.SupportedResource(row.Resource) {
		return repo.ResolvedTask{}, errors.New("sync task is not active")
	}
	if !capabilityHasResource(row.Capabilities, row.Resource) {
		return repo.ResolvedTask{}, errors.New("sync resource is unsupported by platform")
	}
	mappingVersion, generation := taskOptions(row.Options)
	if mappingVersion <= 0 || generation <= 0 {
		return repo.ResolvedTask{}, errors.New("sync task authorization facts are incomplete")
	}
	return repo.ResolvedTask{TenantID: row.TenantID, JobID: row.JobID, RunID: row.RunID, ShopID: row.ShopID, Resource: row.Resource, MappingVersion: mappingVersion, Generation: generation}, nil
}

func capabilityHasResource(raw json.RawMessage, resource string) bool {
	var value struct {
		Resources []string `json:"resources"`
	}
	if json.Unmarshal(raw, &value) != nil {
		return false
	}
	for _, item := range value.Resources {
		if item == resource {
			return true
		}
	}
	return false
}

func taskOptions(raw json.RawMessage) (int64, int64) {
	var value struct {
		MappingVersion    int64 `json:"mapping_version_id"`
		MappingVersionAlt int64 `json:"mapping_version"`
		Generation        int64 `json:"authorization_generation"`
	}
	if json.Unmarshal(raw, &value) != nil {
		return 0, 0
	}
	if value.MappingVersion <= 0 {
		value.MappingVersion = value.MappingVersionAlt
	}
	return value.MappingVersion, value.Generation
}

// IssueWorkAccess re-reads and verifies the task before issuing a capability.
func (s *syncStore) IssueWorkAccess(ctx context.Context, tenantID, jobID, runID int64, methods []string) (scope.WorkAccess, error) {
	task, err := s.ResolveTask(ctx, tenantID, jobID, runID)
	if err != nil {
		return scope.WorkAccess{}, err
	}
	access, err := scope.IssueWorkAccess(scope.BootstrapIssuer{}, task.TenantID, task.JobID, task.RunID, task.ShopID, task.Resource, task.MappingVersion, task.Generation, methods)
	if err != nil {
		return scope.WorkAccess{}, fmt.Errorf("issue work access: %w", err)
	}
	return access, nil
}

var _ repo.Store = (*syncStore)(nil)
