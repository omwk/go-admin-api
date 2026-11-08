package bootstrap

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/omwk/go-admin-api/internal/modules/share"
	"github.com/stretchr/testify/require"
)

type shareFailedStore struct{ share.LinkStore }

func (s shareFailedStore) ResolveLink(context.Context, int64, string) (share.Link, error) {
	return share.Link{}, errors.New("injected primary database failure")
}

type shareFailedCache struct{ share.AccessCache }

func (s shareFailedCache) TokenBucket(context.Context, string, float64, int) (bool, int, time.Duration, error) {
	return false, 0, 0, errors.New("injected cache failure")
}

type shareFailedContentCache struct{ share.AccessCache }

func (s shareFailedContentCache) Get(context.Context, string) (string, bool, error) {
	return "", false, errors.New("injected content cache failure")
}

type shareFailedAdmission struct{ share.LinkStore }

func (s shareFailedAdmission) Admit(context.Context, share.Link, bool, time.Time) error {
	return errors.New("injected primary admission failure")
}

func TestShareIntegrationWarmCacheDependencyFailuresDenyContent(t *testing.T) {
	f := newShareAccessFixture(t)
	link := f.issue(t, f.publication())
	w := f.anonymous(t, "GET", link.URL, "192.0.2.1", nil, nil)
	require.Equal(t, 200, w.Code)
	cookie := shareCookie(t, w)
	for _, dependency := range []string{"database", "admission", "redis", "content_cache"} {
		t.Run(dependency, func(t *testing.T) {
			var store share.LinkStore = &shareStore{database: f.database}
			var cached share.AccessCache = f.redis.cache
			if dependency == "database" {
				store = shareFailedStore{store}
			} else if dependency == "admission" {
				store = shareFailedAdmission{store}
			} else if dependency == "content_cache" {
				cached = shareFailedContentCache{cached}
			} else {
				cached = shareFailedCache{cached}
			}
			svc, err := share.NewLinkService(share.LinkDependencies{Store: store, Cache: cached, Sealer: f.sealer, Idempotency: newIdempotencyStore(f.database), Now: func() time.Time { return f.now }})
			require.NoError(t, err)
			f.router = gin.New()
			share.RegisterPublicRoutes(f.router, svc, 1)
			for _, path := range []string{link.URL, fmt.Sprintf("%s/items/%d", link.URL, f.spu), link.URL + "/verify"} {
				method := "GET"
				if path == link.URL+"/verify" {
					method = "POST"
				}
				response := f.anonymous(t, method, path, "192.0.2.1", map[string]string{"password": ""}, cookie)
				require.Equal(t, 503, response.Code)
				require.Contains(t, response.Body.String(), `"data":null`)
				require.NotContains(t, response.Body.String(), "Pricing product")
			}
		})
	}
}

func TestShareIntegrationTenantCacheIsolationAndCookieBinding(t *testing.T) {
	// REQ-SHR-003/006: token prefixes never participate in authorization or cache keys.
	f := newShareAccessFixture(t)
	a := f.issue(t, f.publication())
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	var row share.Link
	require.NoError(t, q.Where("id=?", a.ID).Take(&row).Error)
	b, second := f.samePrefixTenantLink(t, a, row)
	first := f.anonymous(t, "GET", a.URL, "192.0.2.1", nil, nil)
	require.Equal(t, 200, first.Code)
	cookie := shareCookie(t, first)
	f.router = gin.New()
	share.RegisterPublicRoutes(f.router, f.links, 2)
	other := f.anonymous(t, "GET", b.URL, "192.0.2.1", nil, nil)
	require.Equal(t, "OTHER-TENANT-CANARY", sharePublicData(t, other).Title)
	require.Equal(t, 401, f.anonymous(t, "GET", fmt.Sprintf("%s/items/%d", b.URL, f.spu), "192.0.2.1", nil, cookie).Code)
	keyA := fmt.Sprintf("share:content:v1:1:%d:1", row.ID)
	keyB := fmt.Sprintf("share:content:v1:2:%d:1", second.ID)
	for _, key := range []string{keyA, keyB} {
		_, found, err := f.redis.cache.Get(f.ctx, key)
		require.NoError(t, err)
		require.True(t, found)
	}
	f.router = gin.New()
	share.RegisterPublicRoutes(f.router, f.links, 1)
	w := f.anonymous(t, "GET", a.URL, "192.0.2.1", nil, cookie)
	require.Equal(t, 200, w.Code)
	require.NotContains(t, w.Body.String(), "OTHER-TENANT-CANARY")
	// Use the same opaque token in another server-selected tenant; the old Cookie is rejected.
	require.NoError(t, q.Table("share_link").Where("id=?", row.ID).Update("token_hash", fmt.Sprintf("%064x", row.ID+200)).Error)
	require.NoError(t, q.Table("share_link").Where("id=?", second.ID).Update("token_hash", row.TokenHash).Error)
	f.router = gin.New()
	share.RegisterPublicRoutes(f.router, f.links, 2)
	require.Equal(t, 401, f.anonymous(t, "GET", fmt.Sprintf("%s/items/%d", a.URL, f.spu), "192.0.2.1", nil, cookie).Code)
	require.Equal(t, row.TokenPrefix, second.TokenPrefix)
}

func (f *shareAccessFixture) samePrefixTenantLink(t *testing.T, a share.LinkReceipt, row share.Link) (share.LinkReceipt, share.Link) {
	t.Helper()
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	token := row.TokenPrefix + randomShareProbe(t)[len(row.TokenPrefix):]
	sum := sha256.Sum256([]byte(token))
	second := row
	second.ID, second.TenantID, second.TokenHash = 0, 2, hex.EncodeToString(sum[:])
	second.Title = "OTHER-TENANT-CANARY"
	require.NoError(t, q.Create(&second).Error)
	frozen, err := (&shareStore{database: f.database}).Frozen(f.ctx, 1, a.ID)
	require.NoError(t, err)
	frozen.Content.Title = second.Title
	sum = sha256.Sum256([]byte(randomShareProbe(t)))
	frozen.Content.ContentVersion = hex.EncodeToString(sum[:])
	raw, err := json.Marshal(frozen)
	require.NoError(t, err)
	require.NoError(t, q.Exec("INSERT INTO share_snapshot(tenant_id,share_link_id,payload,checksum,item_count) VALUES(2,?,?::jsonb,?,1)", second.ID, string(raw), frozen.Content.ContentVersion).Error)
	return share.LinkReceipt{ID: second.ID, URL: "/api/v1/share/" + token}, second
}
