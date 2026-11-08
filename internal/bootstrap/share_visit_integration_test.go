package bootstrap

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestShareVisitIntegrationMinimalDataScopeAndAudit(t *testing.T) {
	f := newShareAccessFixture(t)
	link := f.issue(t, f.publication())
	root := f.anonymous(t, "GET", link.URL, "192.0.2.54", nil, nil)
	require.Equal(t, 200, root.Code)
	require.Equal(t, 200, f.anonymous(t, "GET", fmt.Sprintf("%s/items/%d", link.URL, f.spu), "192.0.2.54", nil, shareCookie(t, root)).Code)
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	var records []string
	require.NoError(t, q.Raw("SELECT row_to_json(v)::text FROM share_visit v").Scan(&records).Error)
	require.Len(t, records, 2)
	require.NotContains(t, strings.Join(records, ""), "192.0.2.54")
	var hashes int64
	require.NoError(t, q.Table("share_visit").Where("ip_hash IS NOT NULL").Count(&hashes).Error)
	require.Zero(t, hashes)
	path := fmt.Sprintf("/share-links/%d/visits", link.ID)
	r, _ := f.request(t, "GET", path, nil)
	require.Equal(t, 403, r.Code)
	reauthInquiry(t, f)
	r, _ = f.request(t, "GET", path, nil)
	require.Equal(t, 200, r.Code)
	for _, forbidden := range []string{"ip_hash", "user_agent", "referer", "spu_id", "sku_id"} {
		require.NotContains(t, r.Body.String(), forbidden)
	}
	require.NoError(t, q.Table("sys_role").Where("tenant_id=1").Update("data_scope", "self").Error)
	require.NoError(t, q.Table("share_link").Where("id=?", link.ID).Update("created_by", f.user.ID).Error)
	r, _ = f.request(t, "GET", path, nil)
	require.Equal(t, 404, r.Code)
	require.NoError(t, q.Table("sys_user").Where("id=?", f.actor.UserID).Update("is_super", false).Error)
	require.NoError(t, q.Exec("DELETE FROM sys_role_permission WHERE permission_id IN (SELECT id FROM sys_permission WHERE tenant_id=1 AND code=?)", "share:visit:list").Error)
	r, _ = f.request(t, "GET", path, nil)
	require.Equal(t, 403, r.Code)
	var audits int64
	require.NoError(t, q.Table("audit_log").Where("action='share.visit.read'").Count(&audits).Error)
	require.EqualValues(t, 4, audits)
	t.Log("visit stores time/result/link only, IP hash=NULL and no UA; detail requires independent permission+scope+reauth; each access audited")
}

func TestShareVisitIntegrationFailureRollsBackRootCount(t *testing.T) {
	f := newShareAccessFixture(t)
	link := f.issue(t, f.publication())
	require.NoError(t, f.database.Exec(f.ctx, `CREATE FUNCTION reject_share_visit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'visit unavailable'; END $$`))
	require.NoError(t, f.database.Exec(f.ctx, `CREATE TRIGGER reject_share_visit BEFORE INSERT ON share_visit FOR EACH ROW EXECUTE FUNCTION reject_share_visit()`))
	require.Equal(t, 503, f.anonymous(t, "GET", link.URL, "192.0.2.55", nil, nil).Code)
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	var views int
	require.NoError(t, q.Table("share_link").Select("view_count").Where("id=?", link.ID).Take(&views).Error)
	require.Zero(t, views)
	t.Log("visit write failure=503; atomic view_count remains zero")
}
