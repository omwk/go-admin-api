package db

import (
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/require"
)

func TestMigrationIntegrationPartitionInvariants(t *testing.T) {
	database, ctx := integrationDB(t)
	q := database.handle.WithContext(ctx)
	var partitioned bool
	require.NoError(t, q.Raw(`SELECT EXISTS (SELECT 1 FROM pg_partitioned_table
		WHERE partrelid='raw_payload'::regclass AND partstrat='r')`).Scan(&partitioned).Error)
	require.True(t, partitioned)
	var initial int64
	require.NoError(t, q.Raw("SELECT count(*) FROM pg_inherits WHERE inhparent='raw_payload'::regclass").Scan(&initial).Error)
	require.EqualValues(t, 2, initial)
	// There is deliberately no default partition: uncovered months must fail closed.
	insert := `INSERT INTO raw_payload (platform_id,shop_id,resource_type,external_id,payload,payload_sha256,payload_size,fetched_at)
		VALUES (1,1,'product','partition-test','{}','digest',2,?)`
	var pgErr *pgconn.PgError
	require.ErrorAs(t, database.Exec(ctx, insert, "2042-01-31T23:59:59Z"), &pgErr)
	require.Equal(t, "23514", pgErr.Code)
	for _, month := range []string{"2042-01", "2042-02"} {
		var created bool
		require.NoError(t, q.Raw("SELECT ensure_raw_partition(?)", month).Scan(&created).Error)
		require.True(t, created)
		require.NoError(t, q.Raw("SELECT ensure_raw_partition(?)", month).Scan(&created).Error)
		require.False(t, created)
	}
	for _, at := range []string{"2042-01-31T23:59:59Z", "2042-02-01T00:00:00Z"} {
		require.NoError(t, database.Exec(ctx, insert, at))
	}
	var months int64
	require.NoError(t, q.Raw("SELECT count(DISTINCT tableoid) FROM raw_payload").Scan(&months).Error)
	require.EqualValues(t, 2, months)
}

func TestMigrationIntegrationTenantAndIdempotencyUniqueness(t *testing.T) {
	database, ctx := integrationDB(t)
	for _, tc := range []struct{ name, statement string }{
		{"role", "INSERT INTO sys_role (tenant_id,code,name,data_scope,status) VALUES (?,'unique-test','role','all','active')"},
		{"idempotency", "INSERT INTO idempotency_key (tenant_id,scope,key,request_hash) VALUES (?,'unique-test','request','digest')"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.NoError(t, database.Exec(ctx, tc.statement, 1))
			var pgErr *pgconn.PgError
			require.ErrorAs(t, database.Exec(ctx, tc.statement, 1), &pgErr)
			require.Equal(t, "23505", pgErr.Code)
			require.NoError(t, database.Exec(ctx, tc.statement, 2))
		})
	}
}

func TestMigrationIntegrationVersionDefaultsAndCompositeKeys(t *testing.T) {
	database, ctx := integrationDB(t)
	q := database.handle.WithContext(ctx)
	// Keep the pre-existing optimistic-lock defaults and composite-key invariants.
	for _, table := range []string{"shop", "product_spu"} {
		var column struct{ DataType, IsNullable, ColumnDefault string }
		require.NoError(t, q.Raw(`SELECT data_type,is_nullable,column_default FROM information_schema.columns
			WHERE table_schema=current_schema() AND table_name=? AND column_name='version'`, table).Scan(&column).Error)
		require.Equal(t, "integer", column.DataType)
		require.Equal(t, "NO", column.IsNullable)
		require.Equal(t, "0", column.ColumnDefault)
	}
	for _, key := range []struct{ table, columns string }{
		{"replay_run_item", "PRIMARY KEY (run_id, fetched_at, id)"},
		{"export_job_row", "PRIMARY KEY (export_id, attempt, ordinal)"},
	} {
		var definition string
		require.NoError(t, q.Raw(`SELECT pg_get_constraintdef(oid) FROM pg_constraint
			WHERE conrelid=to_regclass(?) AND contype='p'`, key.table).Scan(&definition).Error)
		require.Equal(t, key.columns, definition)
	}
}
