package bootstrap

import (
	"context"
	"testing"

	"github.com/omwk/go-admin-api/internal/modules/identity/scope"
	"github.com/stretchr/testify/require"
)

func TestAdminF2ReadQueriesAreUnlockedAndDepartmentsUseOneQuery(t *testing.T) {
	// F2 §3: the entire department projection is one SELECT; no read acquires locks.
	for _, kind := range []string{"all", "dept", "dept_and_child", "self", "custom"} {
		for _, name := range []string{"ReadUser", "ReadRole", "ReadMenu", "ReadLinks", "Departments"} {
			t.Run(kind+"/"+name, func(t *testing.T) {
				store, capture := dryAdminStore(t)
				ctx := scope.WithAccess(context.Background(), matrixAccess(kind))
				require.NoError(t, adminDryCases()[name].run(ctx, store))
				require.NotEmpty(t, capture.statements)
				for _, sql := range capture.statements {
					require.NotContains(t, sql, "FOR UPDATE")
					require.NotContains(t, sql, "pg_advisory")
					require.Contains(t, sql, "SELECT")
				}
				if name == "Departments" {
					require.Len(t, capture.statements, 1)
					require.Contains(t, capture.statements[0], "COUNT(*)")
					require.Contains(t, capture.statements[0], "dept_member.tenant_id = sys_dept.tenant_id")
					require.Contains(t, capture.statements[0], "dept_member.deleted_at IS NULL")
				}
			})
		}
	}
}
