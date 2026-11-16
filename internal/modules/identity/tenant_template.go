package identity

import (
	"context"

	"github.com/omwk/go-admin-api/internal/modules/identity/model"
	"github.com/omwk/go-admin-api/internal/modules/identity/repo"
)

// CreateTemplateInput optionally captures framework objects from an existing tenant.
type CreateTemplateInput struct {
	SourceTenantID *int64 `json:"source_tenant_id" binding:"omitempty,gt=0"`
	Code           string `json:"code" binding:"required,max=64"`
	Name           string `json:"name" binding:"required,max=128"`
	Description    string `json:"description" binding:"max=1000"`
}

// TemplateFields replaces roles when supplied and preserves the framework menu tree.
type TemplateFields struct {
	Name        string               `json:"name" binding:"required,max=128"`
	Description string               `json:"description" binding:"max=1000"`
	Roles       []model.TemplateRole `json:"roles" binding:"omitempty,max=50"`
}

// CreateTemplate takes a complete snapshot; later source changes cannot affect provisioning.
func (s *TenantService) CreateTemplate(ctx context.Context, actor Overview, in CreateTemplateInput) (model.TenantTemplate, error) {
	if err := s.global(actor); err != nil {
		return model.TenantTemplate{}, err
	}
	if !validTemplateMetadata(in.Code, in.Name, in.Description) {
		return model.TenantTemplate{}, invalidAdmin()
	}
	source := actor.User.TenantID
	if in.SourceTenantID != nil {
		source = *in.SourceTenantID
	}
	if _, err := s.store.Tenant(ctx, source, false); err != nil {
		return model.TenantTemplate{}, err
	}
	snap, err := s.store.Snapshot(ctx, source)
	if err != nil {
		return model.TenantTemplate{}, err
	}
	if in.SourceTenantID == nil {
		snap.Roles = []model.TemplateRole{}
	}
	row := model.TenantTemplate{Code: in.Code, Name: in.Name, Description: in.Description}
	if err := encodeTemplate(snap, &row); err != nil {
		return row, err
	}
	if err := s.store.CreateTemplate(ctx, &row); err != nil {
		return model.TenantTemplate{}, err
	}
	return row, s.record(ctx, actor, "system.tenant.template.create", "tenant_template", row.ID, source, nil, map[string]any{"id": row.ID, "code": row.Code, "name": row.Name}, false)
}

// Templates exposes global framework templates only to platform administrators.
func (s *TenantService) Templates(ctx context.Context, actor Overview, page repo.Page) (AdminPage[model.TenantTemplate], error) {
	if err := s.global(actor); err != nil {
		return AdminPage[model.TenantTemplate]{}, err
	}
	rows, total, err := s.store.Templates(ctx, page)
	return AdminPage[model.TenantTemplate]{List: rows, Total: total, Page: page.Number, PageSize: page.Size}, err
}

// Template returns editable roles and the fixed framework menu options.
func (s *TenantService) Template(ctx context.Context, actor Overview, id int64) (model.TenantTemplate, error) {
	if err := s.global(actor); err != nil {
		return model.TenantTemplate{}, err
	}
	return s.store.Template(ctx, id, false)
}

// UpdateTemplate replaces roles atomically while preserving code and menus.
func (s *TenantService) UpdateTemplate(ctx context.Context, actor Overview, id int64, in TemplateFields) (model.TenantTemplate, error) {
	if err := s.global(actor); err != nil {
		return model.TenantTemplate{}, err
	}
	if !validTemplateFields(in.Name, in.Description) {
		return model.TenantTemplate{}, invalidAdmin()
	}
	before, err := s.store.Template(ctx, id, true)
	if err != nil {
		return before, err
	}
	row := before
	row.Name, row.Description = in.Name, in.Description
	if in.Roles != nil {
		if err := replaceTemplateRoles(&row, in.Roles); err != nil {
			return model.TenantTemplate{}, err
		}
	}
	if err := s.store.UpdateTemplate(ctx, &row); err != nil {
		return model.TenantTemplate{}, err
	}
	row, err = s.store.Template(ctx, id, false)
	if err != nil {
		return row, err
	}
	return row, s.record(ctx, actor, "system.tenant.template.update", "tenant_template", id, actor.User.TenantID, templateAuditFields(before), templateAuditFields(row), false)
}

// CloneTemplateInput supplies a new immutable code and display name for a copy.
type CloneTemplateInput struct {
	Code string `json:"code" binding:"required,max=64"`
	Name string `json:"name" binding:"required,max=128"`
}

// CloneTemplate copies the complete persisted template into an independent row.
func (s *TenantService) CloneTemplate(ctx context.Context, actor Overview, id int64, in CloneTemplateInput) (model.TenantTemplate, error) {
	if err := s.global(actor); err != nil {
		return model.TenantTemplate{}, err
	}
	if !validTemplateMetadata(in.Code, in.Name, "") {
		return model.TenantTemplate{}, invalidAdmin()
	}
	source, err := s.store.Template(ctx, id, false)
	if err != nil {
		return model.TenantTemplate{}, err
	}
	row := model.TenantTemplate{Code: in.Code, Name: in.Name, Description: source.Description,
		Roles: source.Roles, Menus: source.Menus, Permissions: source.Permissions, Settings: source.Settings}
	if err := s.store.CreateTemplate(ctx, &row); err != nil {
		return model.TenantTemplate{}, err
	}
	return row, s.record(ctx, actor, "system.tenant.template.clone", "tenant_template", row.ID, actor.User.TenantID, nil,
		map[string]any{"id": row.ID, "code": row.Code, "name": row.Name, "source_template_id": id}, false)
}

func templateAuditFields(row model.TenantTemplate) map[string]any {
	return map[string]any{"name": row.Name, "description": row.Description, "roles": row.Roles}
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
	return s.record(ctx, actor, "system.tenant.template.delete", "tenant_template", id, actor.User.TenantID, before, nil, true)
}
