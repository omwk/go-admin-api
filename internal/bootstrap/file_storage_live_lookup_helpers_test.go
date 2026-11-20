package bootstrap

import (
	"context"
	"encoding/base64"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/omwk/go-admin-api/internal/infra/storage"
	"github.com/omwk/go-admin-api/internal/kernel/config"
	"github.com/omwk/go-admin-api/internal/kernel/crypto"
	"github.com/omwk/go-admin-api/internal/kernel/db"
	filemodule "github.com/omwk/go-admin-api/internal/modules/file"
	"github.com/stretchr/testify/require"
)

func liveLookupProvider(t *testing.T, path string) storage.Provider {
	t.Helper()
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	settings := map[string]string{}
	for _, line := range strings.Split(string(raw), "\n") {
		name, value, found := strings.Cut(strings.TrimSpace(line), "=")
		if found && !strings.HasPrefix(name, "#") {
			settings[strings.TrimSpace(name)] = strings.Trim(strings.TrimSpace(value), `"'`)
		}
	}
	clear(raw)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	database, err := db.Open(ctx, config.DB{DSN: settings["TEST_DATABASE_URL"], MaxOpen: 2, MaxIdle: 1, ConnMaxLifetime: time.Minute})
	require.NoError(t, err)
	defer func() { require.NoError(t, database.Close()) }()
	q, cancelQuery := database.WithContext(ctx)
	defer cancelQuery()
	var p storage.Provider
	// Read only the existing provider fields so acceptance also works before migration 38.
	require.NoError(t, q.Select("id,tenant_id,endpoint,port,bucket,use_ssl,region,access_key,secret_enc").Where("tenant_id=? AND is_default AND enabled", 1).Take(&p).Error)
	key, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(settings["CH_CRYPTO_MASTER_KEY"], "base64:"))
	require.NoError(t, err)
	defer clear(key)
	version := 1
	if configured := settings["CH_CRYPTO_KEY_VERSION"]; configured != "" {
		version, err = strconv.Atoi(configured)
		require.NoError(t, err)
	}
	sealer, err := crypto.NewKeyring(map[int][]byte{version: key}, version)
	require.NoError(t, err)
	p.SecretKey, err = storage.OpenSecret(sealer, p)
	require.NoError(t, err)
	return p
}

func assertLiveFileBinding(t *testing.T, binding filemodule.Binding, object *storage.S3) {
	t.Helper()
	file, ok := binding.Object.(storage.FileObject)
	require.True(t, ok)
	require.Same(t, object, file.S3)
}

func cleanupLiveLookup(t *testing.T, f *fileStorageFixture, id int64) {
	t.Helper()
	row, err := newFileStore(f.database).File(f.ctx, 1, id)
	require.NoError(t, err)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(f.ctx), 30*time.Second)
		defer cancel()
		selected, err := f.resolver.Resolve(ctx, row.ProviderID)
		require.NoError(t, err)
		require.NoError(t, selected.Object.WithBucket(row.Bucket).Delete(ctx, row.ObjectKey))
		_, err = selected.Object.WithBucket(row.Bucket).Get(ctx, row.ObjectKey)
		require.Error(t, err, "acceptance object must have been removed")
		w, _ := f.request(t, "DELETE", "/files/"+strconv.FormatInt(id, 10), nil, "")
		require.Equal(t, 204, w.Code)
		t.Log("BE-IDN-022 cleanup: remote object deleted and confirmed absent; metadata deleted via API; isolated database and audits removed by fixture")
	})
}
