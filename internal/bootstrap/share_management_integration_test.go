package bootstrap

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/omwk/go-admin-api/internal/kernel/audit"
	"github.com/omwk/go-admin-api/internal/modules/identity"
	"github.com/omwk/go-admin-api/internal/modules/identity/permission"
	"github.com/omwk/go-admin-api/internal/modules/share"
	"github.com/stretchr/testify/require"
)

func TestShareIntegrationManagementScopePermissionAndReplay(t *testing.T) {
	f := newShareAccessFixture(t)
	c := f.customer(t, "link-scope")
	in := f.publication()
	in.CustomerID, in.Password = c.ID, f.randomValue(t)
	r, e := f.keyed(t, "POST", "/share-links", "scoped-link", in)
	require.Equal(t, 201, r.Code)
	var hidden share.LinkReceipt
	require.NoError(t, json.Unmarshal(e.Data, &hidden))
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	require.NoError(t, q.Table("customer").Where("id=?", c.ID).Update("owner_user_id", f.user.ID).Error)
	ordinaryScopeActor(t, f.identityFixture, f.actor.UserID)
	require.NoError(t, q.Table("sys_role").Where("tenant_id=1 AND code=?", identity.SuperAdminRoleCode).Update("data_scope", "self").Error)
	r, _ = f.keyed(t, "POST", "/share-links", "scoped-link", in)
	require.Equal(t, 404, r.Code)
	revoke := false
	in.RevokeOld = &revoke
	for _, action := range []string{"regenerate", "revoke"} {
		r, _ = f.request(t, "POST", fmt.Sprintf("/share-links/%d/%s", hidden.ID, action), in)
		require.Equal(t, 404, r.Code)
	}
	visible := f.issue(t, f.publication())
	r, e = f.request(t, "GET", "/share-links", nil)
	require.Equal(t, 200, r.Code)
	var page share.Page[share.LinkView]
	require.NoError(t, json.Unmarshal(e.Data, &page))
	require.EqualValues(t, 1, page.Total)
	require.Equal(t, visible.ID, page.List[0].ID)
	require.NoError(t, q.Table("sys_user").Where("id=?", f.actor.UserID).Update("is_super", false).Error)
	require.NoError(t, q.Exec(`DELETE FROM sys_role_permission WHERE permission_id IN (SELECT id FROM sys_permission WHERE tenant_id=1 AND code LIKE 'share:%')`).Error)
	for _, tc := range []struct{ method, path string }{{"GET", "/share-links"}, {"POST", "/share-links"},
		{"POST", fmt.Sprintf("/share-links/%d/regenerate", visible.ID)}, {"POST", fmt.Sprintf("/share-links/%d/revoke", visible.ID)}} {
		r, e = f.request(t, tc.method, tc.path, f.publication())
		require.Equal(t, 403, r.Code)
		require.Equal(t, 20107, e.Code)
	}
}

type shareFailedAudit struct{ share.LinkStore }

func (s shareFailedAudit) Audit(context.Context, audit.Event) error {
	return errors.New("injected share audit failure")
}

func TestShareIntegrationPublicationAndRevocationAuditRollback(t *testing.T) {
	f := newShareAccessFixture(t)
	old := f.issue(t, f.publication())
	store := shareFailedAudit{&shareStore{database: f.database}}
	svc, err := share.NewLinkService(share.LinkDependencies{Store: store, Idempotency: newIdempotencyStore(f.database),
		Sealer: f.sealer, Cache: f.redis.cache, Now: func() time.Time { return f.now }})
	require.NoError(t, err)
	a := share.Actor{TenantID: f.actor.TenantID, UserID: f.actor.UserID}
	ctx, err := store.Authorize(f.ctx, a, permission.ShareLinkCreate)
	require.NoError(t, err)
	in := f.publication()
	revoke := true
	in.RevokeOld = &revoke
	out, err := svc.Issue(ctx, a, old.ID, in, nil)
	require.Error(t, err)
	require.Empty(t, out.URL)
	require.Error(t, svc.Revoke(ctx, a, old.ID, nil))
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	for _, table := range []string{"share_link", "share_snapshot"} {
		var count int64
		require.NoError(t, q.Table(table).Count(&count).Error)
		require.EqualValues(t, 1, count)
	}
	require.Equal(t, 200, f.anonymous(t, "GET", old.URL, "192.0.2.1", nil, nil).Code)
}
