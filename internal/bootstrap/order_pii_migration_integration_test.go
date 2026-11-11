package bootstrap

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOrderPIIMigrationIntegrationTwelveElevenTwelvePreservesCiphertext(t *testing.T) {
	f := newOrderQueryFixture(t)
	// Authenticate against the current schema before testing historical DDL.
	orderPIIReauth(t, f)
	migrationAt(f.ctx, t, f.database, 12)
	id := f.add(t, "pii-migration", f.now)
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	var before, after string
	projection := "tenant_id,order_id,receiver_name_cipher,receiver_name_nonce,phone_cipher,phone_nonce,address_cipher,address_nonce,key_version,revision"
	require.NoError(t, q.Raw("SELECT row_to_json(a)::text FROM (SELECT "+projection+" FROM order_address WHERE order_id=?) a", id).Scan(&before).Error)
	state, err := f.database.Migrate(f.ctx, "down")
	require.NoError(t, err)
	require.EqualValues(t, 11, state.Version)
	var columns int64
	require.NoError(t, q.Raw("SELECT count(*) FROM information_schema.columns WHERE table_schema=current_schema() AND ((table_name='sys_refresh_token' AND column_name='reauth_at') OR (table_name='order_address' AND column_name IN ('receiver_name_mask','phone_mask')))").Scan(&columns).Error)
	require.Zero(t, columns)
	state, err = f.database.Migrate(f.ctx, "up")
	require.NoError(t, err)
	require.EqualValues(t, latestBootstrapMigration(t), state.Version)
	require.False(t, state.Dirty)
	require.NoError(t, q.Raw("SELECT row_to_json(a)::text FROM (SELECT "+projection+" FROM order_address WHERE order_id=?) a", id).Scan(&after).Error)
	require.Equal(t, before, after)
	response, envelope := f.piiRequest(t, id)
	require.Equal(t, 403, response.Code)
	require.Equal(t, 20108, envelope.Code, "rollback must invalidate step-up timestamps")
	for _, update := range []map[string]any{{"phone_mask": "synthetic-cleartext"}, {"receiver_name_mask": "unmasked"}} {
		require.Error(t, q.Table("order_address").Where("order_id=?", id).Updates(update).Error)
	}
	t.Log("migration 12 -> 11 -> 12; existing ciphers preserved; session step-up reset; mask constraints reject cleartext")
}
