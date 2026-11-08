package bootstrap

import (
	"testing"

	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// recordShareSQLTemplates observes the real share adapter's executed statements.
// It matches production's parameterized logging policy and never interpolates Vars.
func recordShareSQLTemplates(t *testing.T, f *shareAccessFixture) {
	t.Helper()
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	record := func(query *gorm.DB) {
		if query.Statement == nil {
			return
		}
		_, err := f.sqlLogs.Write([]byte(query.Statement.SQL.String() + "\n"))
		require.NoError(t, err)
	}
	callbacks := q.Callback()
	require.NoError(t, callbacks.Create().After("gorm:create").Register("share:sql-canary", record))
	require.NoError(t, callbacks.Query().After("gorm:query").Register("share:sql-canary", record))
	require.NoError(t, callbacks.Update().After("gorm:update").Register("share:sql-canary", record))
	require.NoError(t, callbacks.Delete().After("gorm:delete").Register("share:sql-canary", record))
	require.NoError(t, callbacks.Raw().After("gorm:raw").Register("share:sql-canary", record))
}
