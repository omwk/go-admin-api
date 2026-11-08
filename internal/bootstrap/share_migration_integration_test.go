package bootstrap

import (
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/omwk/go-admin-api/internal/modules/share"
	"github.com/stretchr/testify/require"
)

func TestShareMigrationIntegrationThirteenTwelveThirteen(t *testing.T) {
	// REQ-SHR-001/002: M6a creates pricing and reserves share tables without routes.
	database, ctx := isolatedIdentityDB(t)
	state, err := database.Migrate(ctx, "version")
	require.NoError(t, err)
	require.EqualValues(t, 13, state.Version)
	state, err = database.Migrate(ctx, "down")
	require.NoError(t, err)
	require.EqualValues(t, 12, state.Version)
	q, cancel := database.WithContext(ctx)
	defer cancel()
	var absent bool
	require.NoError(t, q.Raw("SELECT to_regclass('customer') IS NULL AND to_regclass('inquiry') IS NULL").Scan(&absent).Error)
	require.True(t, absent)
	state, err = database.Migrate(ctx, "up")
	require.NoError(t, err)
	require.EqualValues(t, 13, state.Version)
	require.False(t, state.Dirty)
	var plain, foreignKeys int64
	require.NoError(t, q.Raw(`SELECT count(*) FROM information_schema.columns WHERE table_schema=current_schema()
		AND ((table_name='customer' AND column_name IN ('contact_name','phone','email'))
		OR (table_name='price_list_item' AND column_name='note')
		OR (table_name='inquiry' AND column_name IN ('contact_name','contact_phone','contact_email','message')))`).Scan(&plain).Error)
	require.Zero(t, plain, "contact and free-text fields must have no cleartext columns")
	require.NoError(t, q.Raw(`SELECT count(*) FROM pg_constraint WHERE contype='f' AND conrelid IN
		('customer'::regclass,'price_list'::regclass,'price_list_item'::regclass,'share_link'::regclass,
		'share_snapshot'::regclass,'share_visit'::regclass,'inquiry'::regclass)`).Scan(&foreignKeys).Error)
	require.Zero(t, foreignKeys, "business relationships are checked by the application")
	t.Log("migration 13 -> 12 -> 13; dirty=false; no cleartext contact/note columns or business foreign keys")
}

func TestShareMigrationIntegrationPricingConstraints(t *testing.T) {
	f := newShareFixture(t)
	customer := f.customer(t, "constraints")
	list := f.list(t, &customer.ID)
	f.batch(t, &list, share.ItemInput{SPUID: f.spu, PriceCents: 100})
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	for _, query := range []string{
		"UPDATE price_list_item SET price_cents=-1",
		"UPDATE price_list_item SET moq=0",
		"UPDATE price_list_item SET sku_id=-1",
		"UPDATE price_list SET type='default'",
		"UPDATE price_list SET customer_id=NULL",
		"UPDATE price_list SET currency='cny'",
		"UPDATE price_list SET valid_from=now(),valid_to=now()",
		"UPDATE customer SET phone_nonce=decode('','hex')",
	} {
		err := q.Exec(query).Error
		var pgErr *pgconn.PgError
		require.ErrorAs(t, err, &pgErr, query)
		require.Equal(t, "23514", pgErr.Code, query)
	}
	duplicate := q.Exec(`INSERT INTO price_list_item (tenant_id,price_list_id,spu_id,sku_id,price_cents,moq,note_cipher,note_nonce,key_version)
		SELECT tenant_id,price_list_id,spu_id,sku_id,price_cents,moq,note_cipher,note_nonce,key_version FROM price_list_item`).Error
	var pgErr *pgconn.PgError
	require.ErrorAs(t, duplicate, &pgErr)
	require.Equal(t, "23505", pgErr.Code, "SPU-level SKU 0 keys must also be unique")
	var count int64
	require.NoError(t, q.Raw("SELECT count(*) FROM pg_indexes WHERE schemaname=current_schema() AND indexname='idx_customer_name_trgm'").Scan(&count).Error)
	require.EqualValues(t, 1, count)
}
