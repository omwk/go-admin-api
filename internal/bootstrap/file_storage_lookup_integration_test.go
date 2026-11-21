package bootstrap

import (
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestFileStorageIntegrationPathStyleConfiguration(t *testing.T) {
	// BE-IDN-022: omitted input defaults to Path; explicit false survives all DB/API layers.
	f := newFileStorageFixture(t)
	port, secure := 443, true
	f.input.Endpoint, f.input.Port, f.input.UseSSL = "s3.us-east-1.amazonaws.com", &port, &secure
	first := f.save(t, f.input, 0, "")
	require.True(t, first.PathStyle)
	selected, err := f.resolver.Default(f.ctx)
	require.NoError(t, err)
	bound, err := f.resolver.Resolve(f.ctx, &first.ID)
	require.NoError(t, err)
	require.Same(t, selected.Object, bound.Object)
	exports := &resolvedExportObjects{database: f.database, resolver: f.resolver}
	object, _, _, err := exports.BindExport(f.ctx)
	require.NoError(t, err)
	require.Same(t, selected.Object, object)
	signed, err := object.PresignGet(f.ctx, "tenants/1/shop/lookup.csv", time.Minute)
	require.NoError(t, err)
	u, err := url.Parse(signed)
	require.NoError(t, err)
	require.Equal(t, "s3.us-east-1.amazonaws.com", u.Hostname())
	require.Equal(t, "/storage-test/tenants/1/shop/lookup.csv", u.Path)
	auto := false
	f.input.PathStyle = &auto
	updated := f.save(t, f.input, first.ID, "")
	require.False(t, updated.PathStyle)
	persisted, err := f.store.Provider(f.ctx, first.ID)
	require.NoError(t, err)
	require.False(t, persisted.PathStyle)
	selected, err = f.resolver.Default(f.ctx)
	require.NoError(t, err)
	require.NotSame(t, bound.Object, selected.Object)
	signed, err = selected.Object.PresignGet(f.ctx, "tenants/1/shop/lookup.csv", time.Minute)
	require.NoError(t, err)
	u, err = url.Parse(signed)
	require.NoError(t, err)
	require.Equal(t, "storage-test.s3.dualstack.us-east-1.amazonaws.com", u.Hostname())
	require.Equal(t, "/tenants/1/shop/lookup.csv", u.Path)
	t.Log("BE-IDN-022 #3/#4/#6: live DB default=true; explicit false persisted; file/export bindings share cached client; AWS Auto presigns virtual-host URL")
}

func TestFileStorageIntegrationPathStyleMigration(t *testing.T) {
	f := newFileStorageFixture(t)
	p := f.save(t, f.input, 0, "")
	latest, err := f.database.Migrate(f.ctx, "version")
	require.NoError(t, err)
	state := latest
	for state.Version > 37 {
		previous := state.Version
		state, err = f.database.Migrate(f.ctx, "down")
		require.NoError(t, err)
		require.Equal(t, previous-1, state.Version)
	}
	require.EqualValues(t, 37, state.Version)
	state, err = f.database.Migrate(f.ctx, "up")
	require.NoError(t, err)
	require.Equal(t, latest.Version, state.Version)
	require.False(t, state.Dirty)
	stored, err := f.store.Provider(f.ctx, p.ID)
	require.NoError(t, err)
	require.True(t, stored.PathStyle, "existing providers must receive the database default")
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	var column struct{ DataType, IsNullable, ColumnDefault string }
	require.NoError(t, q.Raw(`SELECT data_type,is_nullable,column_default FROM information_schema.columns
		WHERE table_schema=current_schema() AND table_name='storage_provider' AND column_name='path_style'`).Scan(&column).Error)
	require.Equal(t, "boolean", column.DataType)
	require.Equal(t, "NO", column.IsNullable)
	require.Equal(t, "true", column.ColumnDefault)
	t.Log("BE-IDN-022 #5: down=37, up=38 dirty=false; existing row path_style=true; boolean NOT NULL DEFAULT true")
}
