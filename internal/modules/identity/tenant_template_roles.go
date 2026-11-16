package identity

import (
	"encoding/json"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/omwk/go-admin-api/internal/kernel/errs"
	"github.com/omwk/go-admin-api/internal/modules/identity/model"
	"github.com/omwk/go-admin-api/internal/modules/identity/permission"
)

var templateRoleCode = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)

func validTemplateMetadata(code, name, description string) bool {
	return tenantCode.MatchString(code) && validTemplateFields(name, description)
}

func validTemplateFields(name, description string) bool {
	return strings.TrimSpace(name) != "" &&
		utf8.RuneCountInString(name) <= 128 && utf8.RuneCountInString(description) <= 1000
}

func replaceTemplateRoles(row *model.TenantTemplate, roles []model.TemplateRole) error {
	if len(roles) > 50 {
		return invalidAdmin()
	}
	snap, err := decodeTemplate(*row)
	if err != nil {
		return err
	}
	menus := map[string]bool{}
	for _, menu := range snap.Menus {
		menus[menu.Key] = true
	}
	codes := map[string]bool{}
	for i := range roles {
		role := &roles[i]
		if codes[role.Code] || !templateRoleCode.MatchString(role.Code) ||
			strings.TrimSpace(role.Name) == "" || utf8.RuneCountInString(role.Name) > 128 {
			return invalidAdmin()
		}
		codes[role.Code] = true
		if err := normalizeTemplateRole(role, menus); err != nil {
			return err
		}
	}
	row.Roles, err = json.Marshal(roles)
	if err != nil {
		return err
	}
	// Old snapshots may predate a newly registered permission selected by an editor.
	return addTemplatePermissions(row, snap.Permissions, roles)
}

func normalizeTemplateRole(role *model.TemplateRole, menus map[string]bool) error {
	switch role.DataScope {
	case "all", "dept", "dept_and_child", "self", "custom":
	default:
		return invalidAdmin()
	}
	if role.Status == "" {
		role.Status = "active"
	}
	if role.Status != "active" && role.Status != "disabled" {
		return invalidAdmin()
	}
	for _, code := range role.Permissions {
		if err := permission.Registered(code); err != nil {
			return &errs.AppError{Code: errs.ErrInvalidParam, HTTPStatus: 400,
				Message: "权限码未注册", Details: map[string]any{"permission": code}}
		}
	}
	for _, key := range role.Menus {
		if key == "" || !menus[key] {
			return invalidAdmin()
		}
	}
	role.Permissions, role.Menus = templateUnique(role.Permissions), templateUnique(role.Menus)
	return nil
}

func templateUnique(values []string) []string {
	result, seen := []string{}, map[string]bool{}
	for _, value := range values {
		if !seen[value] {
			seen[value] = true
			result = append(result, value)
		}
	}
	return result
}

func addTemplatePermissions(row *model.TenantTemplate, points []model.Permission, roles []model.TemplateRole) error {
	if points == nil {
		points = []model.Permission{}
	}
	present, needed := map[string]bool{}, map[string]bool{}
	for _, point := range points {
		present[point.Code] = true
	}
	for _, role := range roles {
		for _, code := range role.Permissions {
			needed[code] = true
		}
	}
	for _, point := range permission.All() {
		if needed[point.Code] && !present[point.Code] {
			points = append(points, model.Permission{Code: point.Code, Name: point.Name, Module: point.Module, Type: point.Type})
		}
	}
	var err error
	row.Permissions, err = json.Marshal(points)
	return err
}
