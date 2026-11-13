package identity

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/omwk/go-admin-api/internal/modules/identity/model"
	"github.com/omwk/go-admin-api/internal/modules/identity/refresh"
)

// ProvisionTenantInput contains no initial password or unrestricted user attributes.
type ProvisionTenantInput struct {
	Code          string  `json:"code" binding:"required,max=64"`
	Name          string  `json:"name" binding:"required,max=128"`
	TemplateID    int64   `json:"template_id" binding:"required,gt=0"`
	AdminUsername string  `json:"admin_username" binding:"required,max=256"`
	AdminEmail    *string `json:"admin_email" binding:"omitempty,max=256"`
}

// ProvisionTenantResult exposes a one-use invitation only in the authorized creation response.
type ProvisionTenantResult struct {
	TenantID  int64  `json:"tenant_id"`
	InviteURL string `json:"invite_url"`
}

// Provision builds only framework entities within the caller's management transaction.
func (s *TenantService) Provision(ctx context.Context, actor Overview, in ProvisionTenantInput) (ProvisionTenantResult, error) {
	if err := s.global(actor); err != nil {
		return ProvisionTenantResult{}, err
	}
	if !tenantCode.MatchString(in.Code) || strings.TrimSpace(in.Name) == "" {
		return ProvisionTenantResult{}, invalidAdmin()
	}
	_, username, err := AccountIdentifier(in.AdminUsername)
	if err != nil {
		return ProvisionTenantResult{}, invalidAdmin()
	}
	email, err := tenantAdminEmail(in.AdminEmail)
	if err != nil {
		return ProvisionTenantResult{}, err
	}
	template, err := s.store.Template(ctx, in.TemplateID, false)
	if err != nil {
		return ProvisionTenantResult{}, err
	}
	snap, err := decodeTemplate(template)
	if err != nil {
		return ProvisionTenantResult{}, err
	}
	row := model.Tenant{Code: in.Code, Name: in.Name, Status: "active", Settings: template.Settings, ProvisionedFrom: &template.ID}
	if err := s.store.CreateTenant(ctx, &row); err != nil {
		return ProvisionTenantResult{}, err
	}
	dept, roles, err := s.store.Materialize(ctx, row.ID, snap)
	if err != nil {
		return ProvisionTenantResult{}, err
	}
	raw, err := refresh.Generate()
	if err != nil {
		return ProvisionTenantResult{}, err
	}
	user, err := invitedUser(model.AuthToken{TenantID: row.ID, DepartmentID: &dept, Username: &username}, AcceptInviteInput{Email: email}, "")
	if err != nil {
		return ProvisionTenantResult{}, err
	}
	user.IsSuper, user.Status = true, "disabled"
	now := s.now().UTC()
	token := model.AuthToken{TenantID: row.ID, Purpose: "invite", TokenHash: refresh.Hash(raw), DepartmentID: &dept, Username: &username, MaxUses: 1, RoleIDs: json.RawMessage(`[]`), CreatedAt: now, ExpiresAt: now.AddDate(0, 0, 7)}
	if err := s.store.CreateTenantAdmin(ctx, &user, roles, &token); err != nil {
		return ProvisionTenantResult{}, err
	}
	if err := s.record(ctx, actor, "system.tenant.provision", "tenant", row.ID, row.ID, nil, map[string]any{"code": row.Code, "template_id": template.ID, "admin_id": user.ID}, false); err != nil {
		return ProvisionTenantResult{}, err
	}
	return ProvisionTenantResult{TenantID: row.ID, InviteURL: AccountLink(s.publicURL, "invite", row.ID, raw)}, nil
}

func tenantAdminEmail(input *string) (*string, error) {
	if input == nil {
		return nil, nil
	}
	column, value, err := AccountIdentifier(*input)
	if err != nil || column != "email" {
		return nil, invalidAdmin()
	}
	return &value, nil
}

func decodeTemplate(row model.TenantTemplate) (model.TenantSnapshot, error) {
	snap := model.TenantSnapshot{}
	fields := []struct {
		data   json.RawMessage
		target any
	}{{row.Roles, &snap.Roles}, {row.Menus, &snap.Menus}, {row.Permissions, &snap.Permissions}}
	for _, field := range fields {
		if err := json.Unmarshal(field.data, field.target); err != nil {
			return snap, invalidAdmin()
		}
	}
	return snap, nil
}
