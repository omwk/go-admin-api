package identity

import (
	"context"
	"strings"

	"github.com/omwk/go-admin-api/internal/modules/identity/model"
	"github.com/omwk/go-admin-api/internal/modules/identity/repo"
)

// CreateTemplateInput captures framework objects from one authorized existing tenant.
type CreateTemplateInput struct {
	SourceTenantID int64  `json:"source_tenant_id" binding:"required,gt=0"`
	Code           string `json:"code" binding:"required,max=64"`
	Name           string `json:"name" binding:"required,max=128"`
	Description    string `json:"description" binding:"max=1000"`
}

// TemplateFields permits only mutable display metadata.
type TemplateFields struct {
	Name        string `json:"name" binding:"required,max=128"`
	Description string `json:"description" binding:"max=1000"`
}

// CreateTemplate takes a complete snapshot; later source changes cannot affect provisioning.
func (s *TenantService) CreateTemplate(ctx context.Context, actor Overview, in CreateTemplateInput) (model.TenantTemplate, error) {
	if err := s.global(actor); err != nil {
		return model.TenantTemplate{}, err
	}
	if !tenantCode.MatchString(in.Code) || strings.TrimSpace(in.Name) == "" {
		return model.TenantTemplate{}, invalidAdmin()
	}
	if _, err := s.store.Tenant(ctx, in.SourceTenantID, false); err != nil {
		return model.TenantTemplate{}, err
	}
	snap, err := s.store.Snapshot(ctx, in.SourceTenantID)
	if err != nil {
		return model.TenantTemplate{}, err
	}
	row := model.TenantTemplate{Code: in.Code, Name: in.Name, Description: in.Description}
	if err := encodeTemplate(snap, &row); err != nil {
		return row, err
	}
	if err := s.store.CreateTemplate(ctx, &row); err != nil {
		return model.TenantTemplate{}, err
	}
	return row, s.record(ctx, actor, "system.tenant_template.create", "tenant_template", row.ID, in.SourceTenantID, nil, map[string]any{"id": row.ID, "code": row.Code}, false)
}

// Templates exposes global framework templates only to platform administrators.
func (s *TenantService) Templates(ctx context.Context, actor Overview, page repo.Page) (AdminPage[model.TenantTemplate], error) {
	if err := s.global(actor); err != nil {
		return AdminPage[model.TenantTemplate]{}, err
	}
	rows, total, err := s.store.Templates(ctx, page)
	return AdminPage[model.TenantTemplate]{List: rows, Total: total, Page: page.Number, PageSize: page.Size}, err
}

// Template returns one immutable snapshot.
func (s *TenantService) Template(ctx context.Context, actor Overview, id int64) (model.TenantTemplate, error) {
	if err := s.global(actor); err != nil {
		return model.TenantTemplate{}, err
	}
	return s.store.Template(ctx, id, false)
}

// UpdateTemplate preserves all snapshot data and code.
func (s *TenantService) UpdateTemplate(ctx context.Context, actor Overview, id int64, in TemplateFields) (model.TenantTemplate, error) {
	if err := s.global(actor); err != nil {
		return model.TenantTemplate{}, err
	}
	if strings.TrimSpace(in.Name) == "" {
		return model.TenantTemplate{}, invalidAdmin()
	}
	before, err := s.store.Template(ctx, id, true)
	if err != nil {
		return before, err
	}
	if err := s.store.UpdateTemplate(ctx, id, in.Name, in.Description); err != nil {
		return model.TenantTemplate{}, err
	}
	row, err := s.store.Template(ctx, id, false)
	if err != nil {
		return row, err
	}
	return row, s.record(ctx, actor, "system.tenant_template.update", "tenant_template", id, actor.User.TenantID, map[string]any{"name": before.Name, "description": before.Description}, map[string]any{"name": in.Name, "description": in.Description}, false)
}

// DeleteTemplate records the entire safe framework snapshot before physical deletion.
func (s *TenantService) DeleteTemplate(ctx context.Context, actor Overview, id int64) error {
	if err := s.global(actor); err != nil {
		return err
	}
	row, err := s.store.Template(ctx, id, true)
	if err != nil {
		return err
	}
	used, err := s.store.TemplateUsed(ctx, id)
	if err != nil {
		return err
	}
	if used {
		return conflictAdmin()
	}
	before, err := snapshot(row)
	if err != nil {
		return err
	}
	if err := s.store.DeleteTemplate(ctx, id); err != nil {
		return err
	}
	return s.record(ctx, actor, "system.tenant_template.delete", "tenant_template", id, actor.User.TenantID, before, nil, true)
}
