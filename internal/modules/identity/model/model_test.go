package model

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestIdentityModelsUseDocumentedTableNames(t *testing.T) {
	models := map[string]interface{ TableName() string }{
		"sys_dept": Department{}, "sys_user": User{}, "sys_role": Role{},
		"sys_permission": Permission{}, "sys_menu": Menu{}, "sys_user_role": UserRole{},
		"sys_role_menu": RoleMenu{}, "sys_role_permission": RolePermission{},
		"sys_role_dept": RoleDepartment{}, "sys_refresh_token": RefreshToken{},
	}
	for expected, model := range models {
		require.Equal(t, expected, model.TableName())
	}
}
