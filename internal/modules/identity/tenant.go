package identity

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"time"

	kernelcrypto "github.com/omwk/go-admin-api/internal/kernel/crypto"
	"github.com/omwk/go-admin-api/internal/kernel/errs"
	"github.com/omwk/go-admin-api/internal/modules/identity/model"
	"github.com/omwk/go-admin-api/internal/modules/identity/repo"
)

// TenantService owns platform directory boundaries and immutable framework templates.
type TenantService struct {
	store      repo.Tenant
	admin      *AdminService
	platformID int64
	publicURL  string
	now        func() time.Time
	sealer     kernelcrypto.Sealer
}

// NewTenantService requires an explicit platform identity and response encryption key.
func NewTenantService(store repo.Tenant, admin *AdminService, platformID int64, publicURL string, now func() time.Time, sealer kernelcrypto.Sealer) (*TenantService, error) {
	if store == nil || admin == nil || platformID <= 0 || now == nil || sealer == nil {
		return nil, errors.New("tenant management dependencies are required")
	}
	return &TenantService{store: store, admin: admin, platformID: platformID, publicURL: publicURL, now: now, sealer: sealer}, nil
}

func (s *TenantService) platform(actor Overview) bool {
	return actor.User.IsSuper && actor.User.TenantID == s.platformID
}

func (s *TenantService) target(actor Overview, id int64) error {
	if id <= 0 || id != actor.User.TenantID && !s.platform(actor) {
		return adminNotFound()
	}
	return nil
}

func (s *TenantService) global(actor Overview) error {
	if !s.platform(actor) {
		return adminNotFound()
	}
	return nil
}

func (s *TenantService) record(ctx context.Context, actor Overview, action, resource string, id, target int64, before, after map[string]any, removed bool) error {
	if before == nil {
		before = map[string]any{}
	}
	before["actor_tenant"], before["target_tenant"] = actor.User.TenantID, target
	if after == nil {
		after = map[string]any{}
	}
	after["actor_tenant"], after["target_tenant"] = actor.User.TenantID, target
	return s.admin.record(ctx, actor, action, resource, id, before, after, removed)
}

// Tenants lists only the actor's own row unless it is the configured platform super user.
func (s *TenantService) Tenants(ctx context.Context, actor Overview, page repo.Page) (AdminPage[model.TenantSummary], error) {
	owner := actor.User.TenantID
	if s.platform(actor) {
		owner = 0
	}
	rows, total, err := s.store.Tenants(ctx, owner, page)
	if err != nil {
		return AdminPage[model.TenantSummary]{}, err
	}
	for _, row := range rows {
		if row.ID != actor.User.TenantID {
			if err := s.record(ctx, actor, "system.tenant.list", "tenant", row.ID, row.ID, nil, nil, false); err != nil {
				return AdminPage[model.TenantSummary]{}, err
			}
		}
	}
	return AdminPage[model.TenantSummary]{List: rows, Total: total, Page: page.Number, PageSize: page.Size}, nil
}

// Tenant checks scope before looking up the target, making foreign and absent targets identical.
func (s *TenantService) Tenant(ctx context.Context, actor Overview, id int64) (model.Tenant, error) {
	if err := s.target(actor, id); err != nil {
		return model.Tenant{}, err
	}
	row, err := s.store.Tenant(ctx, id, false)
	if err == nil && id != actor.User.TenantID {
		err = s.record(ctx, actor, "system.tenant.read", "tenant", id, id, nil, nil, false)
	}
	return row, err
}

// RenameTenant changes the display name without accepting immutable code or settings.
func (s *TenantService) RenameTenant(ctx context.Context, actor Overview, id int64, name string) (model.Tenant, error) {
	if err := s.target(actor, id); err != nil {
		return model.Tenant{}, err
	}
	if strings.TrimSpace(name) == "" {
		return model.Tenant{}, invalidAdmin()
	}
	before, err := s.store.Tenant(ctx, id, true)
	if err != nil {
		return before, err
	}
	if err := s.store.UpdateTenant(ctx, id, name, before.Status); err != nil {
		return model.Tenant{}, err
	}
	row, err := s.store.Tenant(ctx, id, false)
	if err != nil {
		return row, err
	}
	return row, s.record(ctx, actor, "system.tenant.rename", "tenant", id, id, map[string]any{"name": before.Name}, map[string]any{"name": row.Name}, false)
}

// SetTenantEnabled retains all accounts and data and protects the configured platform tenant.
func (s *TenantService) SetTenantEnabled(ctx context.Context, actor Overview, id int64, enabled bool) (model.Tenant, error) {
	if err := s.target(actor, id); err != nil {
		return model.Tenant{}, err
	}
	if !enabled && id == s.platformID {
		return model.Tenant{}, errs.New(errs.ErrConflict, 409, "平台租户不可停用")
	}
	before, err := s.store.Tenant(ctx, id, true)
	if err != nil {
		return before, err
	}
	status, action := "suspended", "system.tenant.disable"
	if enabled {
		status, action = "active", "system.tenant.enable"
	}
	if err := s.store.UpdateTenant(ctx, id, before.Name, status); err != nil {
		return model.Tenant{}, err
	}
	row, err := s.store.Tenant(ctx, id, false)
	if err != nil {
		return row, err
	}
	return row, s.record(ctx, actor, action, "tenant", id, id, map[string]any{"status": before.Status}, map[string]any{"status": status}, false)
}

var tenantCode = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,63}$`)

func encodeTemplate(snapshot model.TenantSnapshot, row *model.TenantTemplate) error {
	fields := []struct {
		value  any
		target *json.RawMessage
	}{{snapshot.Roles, &row.Roles}, {snapshot.Menus, &row.Menus}, {snapshot.Permissions, &row.Permissions}}
	for _, field := range fields {
		data, err := json.Marshal(field.value)
		if err != nil {
			return err
		}
		*field.target = data
	}
	row.Settings = json.RawMessage(`{}`)
	return nil
}
