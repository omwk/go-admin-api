package bootstrap

import (
	"testing"

	"github.com/omwk/go-admin-api/internal/kernel/crypto"
	"github.com/omwk/go-admin-api/internal/modules/shop/model"
	"github.com/omwk/go-admin-api/internal/modules/shop/refresh"
	"github.com/stretchr/testify/require"
)

func TestCredentialRefreshIntegrationKeyringOldReadNewWriteAndReferenceInventory(t *testing.T) {
	f := newCredentialRefreshFixture(t)
	row, v1 := f.readStored(t, f.target.CredentialID, 1)
	original, err := f.sealer.Open(v1.CipherText, v1.Nonce, v1.AAD, v1.KeyVersion)
	require.NoError(t, err)
	defer clear(original)
	keys := map[int][]byte{1: shopRandomBytes(t, 32), 2: shopRandomBytes(t, 32)}
	old, err := crypto.NewKeyring(keys, 1)
	require.NoError(t, err)
	cipher, nonce, key, err := old.Seal(original, v1.AAD)
	require.NoError(t, err)
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	require.NoError(t, q.Model(&model.CredentialVersion{}).Where("id=?", v1.ID).
		Updates(map[string]any{"cipher_text": cipher, "nonce": nonce, "key_version": key}).Error)
	f.sealer, err = crypto.NewKeyring(keys, 2)
	require.NoError(t, err)
	f.executor = f.newExecutor(t, &credentialRefreshFaults{Store: f.refreshStore, failPublish: true})
	run := f.prepare(t)
	run, err = f.executor.Execute(f.ctx, f.target, run.ID)
	require.Error(t, err)
	refs, err := f.refreshStore.KeyReferences(f.ctx, 1)
	require.NoError(t, err)
	require.Equal(t, []refresh.KeyReference{{KeyVersion: 1, Versions: 1}, {KeyVersion: 2, Stages: 1}}, refs)
	f.executor = f.newExecutor(t, f.refreshStore)
	run, err = f.executor.Recover(f.ctx, f.target, run.ID)
	require.NoError(t, err)
	require.Equal(t, refresh.PhasePublished, run.Phase)
	_, v1 = f.readStored(t, row.ID, 1)
	_, v2 := f.readStored(t, row.ID, 2)
	require.Equal(t, 1, v1.KeyVersion)
	require.Equal(t, 2, v2.KeyVersion)
	for _, v := range []model.CredentialVersion{v1, v2} {
		plain, err := f.sealer.Open(v.CipherText, v.Nonce, v.AAD, v.KeyVersion)
		require.NoError(t, err)
		clear(plain)
	}
	refs, err = f.refreshStore.KeyReferences(f.ctx, 1)
	require.NoError(t, err)
	require.Equal(t, []refresh.KeyReference{{KeyVersion: 1, Versions: 1}, {KeyVersion: 2, Versions: 1}}, refs)
	f.assertSafe(t)
}
