package db

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

type scopedTestModel struct {
	ID       int64
	TenantID int64
}

func (scopedTestModel) TableName() string { return "scoped_test_model" }

func dryRunDB(t *testing.T) *DB {
	t.Helper()
	handle, err := gorm.Open(postgres.Open(""), &gorm.Config{
		DryRun: true, DisableAutomaticPing: true, SkipDefaultTransaction: true,
	})
	require.NoError(t, err)
	return &DB{handle: handle}
}

func scopedTestContext() context.Context {
	return WithScope(context.Background(), func(_ context.Context, _ any, handle *gorm.DB) (*gorm.DB, error) {
		return handle.Where("tenant_id = ?", 1), nil
	})
}

func TestScopedQueryUsesBoundModelAndPredicates(t *testing.T) {
	query := dryRunDB(t).Scoped(scopedTestContext(), scopedTestModel{}).Select("tenant_id")
	defer query.Close()

	var rows []scopedTestModel
	require.NoError(t, query.Find(&rows))
	sql := query.handle.Statement.SQL.String()
	require.Contains(t, sql, `FROM "scoped_test_model"`)
	require.Contains(t, sql, `WHERE tenant_id = $1`)
	require.Equal(t, []any{1}, query.handle.Statement.Vars)
}

func TestScopedQueryRejectsMissingModel(t *testing.T) {
	query := dryRunDB(t).Scoped(context.Background(), nil)
	defer query.Close()
	require.ErrorContains(t, query.Find(&[]scopedTestModel{}), "model")
}

func TestScopedQueryChainsJoinsOrderingAndPaging(t *testing.T) {
	query := dryRunDB(t).Scoped(scopedTestContext(), scopedTestModel{}).
		Select("scoped_test_model.id", "scoped_test_model.tenant_id").
		Joins("JOIN related ON related.owner_id = scoped_test_model.id").
		Order("scoped_test_model.id DESC").Limit(5).Offset(2)
	defer query.Close()

	var rows []scopedTestModel
	require.NoError(t, query.Find(&rows))
	sql := query.handle.Statement.SQL.String()
	require.Contains(t, sql, "JOIN related")
	require.Contains(t, sql, "ORDER BY scoped_test_model.id DESC")
	require.Contains(t, sql, "LIMIT $2 OFFSET $3")
}

func TestScopedQuerySupportsReadAndWriteOperations(t *testing.T) {
	newQuery := func() *ScopedQuery {
		return dryRunDB(t).Scoped(scopedTestContext(), scopedTestModel{}).Select("id", "tenant_id")
	}

	first := newQuery()
	var row scopedTestModel
	require.NoError(t, first.First(&row))
	require.Contains(t, first.handle.Statement.SQL.String(), "SELECT")
	first.Close()

	countQuery := newQuery()
	var count int64
	require.NoError(t, countQuery.Count(&count))
	require.Contains(t, countQuery.handle.Statement.SQL.String(), "count(*)")
	countQuery.Close()

	createQuery := newQuery()
	require.NoError(t, createQuery.Create(&scopedTestModel{ID: 1, TenantID: 1}))
	require.Contains(t, createQuery.handle.Statement.SQL.String(), "INSERT INTO")
	createQuery.Close()

	updateQuery := newQuery().Where("id = ?", 1)
	require.NoError(t, updateQuery.Updates(map[string]any{"id": 2}))
	require.Contains(t, updateQuery.handle.Statement.SQL.String(), "UPDATE")
	updateQuery.Close()

	deleteQuery := newQuery().Where("id = ?", 1)
	require.NoError(t, deleteQuery.Delete(&scopedTestModel{}))
	require.Contains(t, deleteQuery.handle.Statement.SQL.String(), "DELETE FROM")
	deleteQuery.Close()
}

func TestScopedQueryCloseCancelsItsContext(t *testing.T) {
	query := dryRunDB(t).Scoped(scopedTestContext(), scopedTestModel{})
	ctx := query.handle.Statement.Context
	query.Close()
	require.ErrorIs(t, ctx.Err(), context.Canceled)
}

func TestScopedQueryRejectsMissingPolicy(t *testing.T) {
	query := dryRunDB(t).Scoped(context.Background(), scopedTestModel{})
	defer query.Close()
	require.ErrorContains(t, query.Find(&[]scopedTestModel{}), "scope is required")
	require.Empty(t, query.handle.Statement.SQL.String())
}

func TestScopedQueryRejectsImplicitColumnSelection(t *testing.T) {
	query := dryRunDB(t).Scoped(scopedTestContext(), scopedTestModel{})
	defer query.Close()
	require.ErrorContains(t, query.Find(&[]scopedTestModel{}), "selected columns")
	require.ErrorContains(t, query.First(&scopedTestModel{}), "selected columns")
	require.Empty(t, query.handle.Statement.SQL.String())
}
