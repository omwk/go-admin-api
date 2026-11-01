package bootstrap

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/oklog/ulid/v2"
	"github.com/omwk/go-admin-api/internal/kernel/db"
	"github.com/omwk/go-admin-api/internal/modules/ingestion"
	"github.com/stretchr/testify/require"
)

func TestRawPartitionIntegrationApplicationCannotPerformDDL(t *testing.T) {
	database, ctx := isolatedIdentityDB(t)
	q, cancel := database.WithContext(ctx)
	defer cancel()
	var canCreateRole bool
	require.NoError(t, q.Raw("SELECT rolsuper OR rolcreaterole FROM pg_roles WHERE rolname = current_user").Scan(&canCreateRole).Error)
	if !canCreateRole {
		t.Skip("restricted-role verification requires TEST_DATABASE_URL with CREATEROLE; ACL verification runs separately")
	}
	role := "raw_app_" + strings.ToLower(ulid.Make().String())
	rollback := errors.New("rollback temporary role and grants")
	err := database.Transaction(ctx, func(tx context.Context) error {
		require.NoError(t, database.Exec(tx, "CREATE ROLE "+role+" NOLOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT"))
		require.NoError(t, database.Exec(tx, "GRANT "+role+" TO CURRENT_USER"))
		q, cancel := database.WithContext(tx)
		defer cancel()
		var schema string
		require.NoError(t, q.Raw("SELECT current_schema()").Scan(&schema).Error)
		require.NoError(t, database.Exec(tx, "GRANT USAGE ON SCHEMA "+schema+" TO "+role))
		require.NoError(t, database.Exec(tx, "GRANT SELECT, INSERT ON raw_payload, raw_dedup TO "+role))
		require.NoError(t, database.Exec(tx, "GRANT USAGE ON SEQUENCE raw_payload_id_seq TO "+role))
		require.NoError(t, database.Exec(tx, "SET LOCAL ROLE "+role))
		var canCreate bool
		require.NoError(t, q.Raw("SELECT has_schema_privilege(current_user, current_schema(), 'CREATE')").Scan(&canCreate).Error)
		require.False(t, canCreate)
		assertRawDDLRejected(tx, t, database, "SELECT ensure_raw_partition('2040-01')")
		assertRawDDLRejected(tx, t, database, "CREATE TABLE raw_app_forbidden (id bigint)")
		// Only the owner/maintenance login can create coverage.
		require.NoError(t, database.Exec(tx, "RESET ROLE"))
		created, err := NewRawPartitionStore(database).Ensure(tx, rawMonth(t, "2040-01"))
		require.NoError(t, err)
		require.True(t, created)
		require.NoError(t, database.Exec(tx, "SET LOCAL ROLE "+role))
		refs, err := rawServiceFor(t, database, 1, 1).SaveRaw(tx, 1, "product", []ingestion.RawItem{
			{ExternalID: "permission-fixture", Payload: json.RawMessage(`{"n":1}`), FetchedAt: rawTime(t, "2040-01-01T00:00:00Z")},
		})
		require.NoError(t, err)
		require.Len(t, refs, 1)
		require.True(t, refs[0].IsNew)
		return rollback // PostgreSQL rolls back CREATE ROLE too; no global test roles remain.
	})
	require.ErrorIs(t, err, rollback)
}

func TestRawPartitionIntegrationPublicPrivilegesRevoked(t *testing.T) {
	database, ctx := isolatedIdentityDB(t)
	q, cancel := database.WithContext(ctx)
	defer cancel()
	var grants []string
	err := q.Raw(`SELECT a.privilege_type FROM pg_namespace n,
		LATERAL aclexplode(COALESCE(n.nspacl, acldefault('n', n.nspowner))) a
		WHERE n.nspname = current_schema() AND a.grantee = 0 AND a.privilege_type = 'CREATE'
		UNION ALL SELECT a.privilege_type FROM pg_proc p JOIN pg_namespace n ON n.oid = p.pronamespace,
		LATERAL aclexplode(COALESCE(p.proacl, acldefault('f', p.proowner))) a
		WHERE n.nspname = current_schema() AND p.proname = 'ensure_raw_partition'
		AND a.grantee = 0 AND a.privilege_type = 'EXECUTE'`).Scan(&grants).Error
	require.NoError(t, err)
	require.Empty(t, grants)
}

func assertRawDDLRejected(ctx context.Context, t *testing.T, database *db.DB, statement string) {
	t.Helper()
	require.NoError(t, database.Exec(ctx, "SAVEPOINT raw_permission_check"))
	require.ErrorContains(t, database.Exec(ctx, statement), "permission denied")
	require.NoError(t, database.Exec(ctx, "ROLLBACK TO SAVEPOINT raw_permission_check"))
	require.NoError(t, database.Exec(ctx, "RELEASE SAVEPOINT raw_permission_check"))
}
