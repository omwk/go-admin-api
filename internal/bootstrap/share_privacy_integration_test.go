package bootstrap

import (
	"fmt"
	"testing"
	"time"

	"github.com/omwk/go-admin-api/internal/modules/share"
	"github.com/stretchr/testify/require"
)

func TestSharePrivacyIntegrationRetentionBoundariesAndDailyRun(t *testing.T) {
	// ADR-020 §5/6: edits never renew immutable retention deadlines.
	f := newShareAccessFixture(t)
	id := submitInquiryID(t, f)
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	var original share.Inquiry
	require.NoError(t, q.Table("inquiry").Where("id=?", id).Take(&original).Error)
	ids := []int64{}
	for i, converted := range []bool{false, false, true, true} {
		row := original
		row.ID = 0
		row.CreatedAt = f.now.Add(-180 * 24 * time.Hour)
		if i == 1 {
			row.CreatedAt = row.CreatedAt.Add(time.Second)
		}
		if converted {
			at := f.now.Add(-30 * 24 * time.Hour)
			if i == 3 {
				at = at.Add(time.Second)
			}
			row.ConvertedAt, row.Status = &at, "won"
		}
		require.NoError(t, q.Create(&row).Error)
		ids = append(ids, row.ID)
	}
	customers := []share.CustomerView{f.customer(t, "expired"), f.customer(t, "fresh")}
	for i, customer := range customers {
		at := f.now.Add(-180*24*time.Hour + time.Duration(i)*time.Second)
		require.NoError(t, q.Table("customer").Where("id=?", customer.ID).Updates(map[string]any{"status": "disabled", "terminated_at": at}).Error)
	}
	for i := range 2 {
		require.NoError(t, q.Table("share_visit").Create(map[string]any{"tenant_id": 1, "share_link_id": original.ShareLinkID, "result": "admitted", "created_at": f.now.Add(-90*24*time.Hour + time.Duration(i)*time.Second)}).Error)
	}
	require.NoError(t, cleanupSharePrivacy(f.ctx, f.database, f.now))
	for i, record := range ids {
		var purged bool
		require.NoError(t, q.Raw("SELECT pii_purged_at IS NOT NULL FROM inquiry WHERE id=?", record).Scan(&purged).Error)
		require.Equal(t, i == 0 || i == 2, purged, fmt.Sprint(i))
		var cipher []byte
		require.NoError(t, q.Table("inquiry").Select("contact_name_cipher").Where("id=?", record).Row().Scan(&cipher))
		if purged {
			require.Equal(t, make([]byte, 16), cipher)
		} else {
			require.Equal(t, original.ContactName.Cipher, cipher)
		}
	}
	var purgedCustomers, oldVisits, runs int64
	require.NoError(t, q.Table("customer").Where("pii_purged_at IS NOT NULL").Count(&purgedCustomers).Error)
	require.EqualValues(t, 1, purgedCustomers)
	require.NoError(t, q.Table("share_visit").Where("created_at<=?", f.now.Add(-90*24*time.Hour)).Count(&oldVisits).Error)
	require.Zero(t, oldVisits)
	require.NoError(t, cleanupSharePrivacy(f.ctx, f.database, f.now))
	require.NoError(t, q.Table("share_privacy_run").Count(&runs).Error)
	require.EqualValues(t, 1, runs)
	t.Log("unconverted=180d and converted=30d erased; one-second-fresh rows retained; terminated customer=180d erased; 90d visit removed; one daily completion record")
}

func TestSharePrivacyIntegrationCleanupAuditFailureRetriesAndThreeYearAudit(t *testing.T) {
	f := newShareAccessFixture(t)
	id := submitInquiryID(t, f)
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	require.NoError(t, q.Table("inquiry").Where("id=?", id).Update("created_at", f.now.Add(-181*24*time.Hour)).Error)
	require.NoError(t, f.database.Exec(f.ctx, `CREATE FUNCTION reject_privacy_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.action='share.privacy.cleanup' THEN RAISE EXCEPTION 'audit unavailable'; END IF; RETURN NEW; END $$`))
	require.NoError(t, f.database.Exec(f.ctx, `CREATE TRIGGER reject_privacy_audit BEFORE INSERT ON audit_log FOR EACH ROW EXECUTE FUNCTION reject_privacy_audit()`))
	cleanup := sharePrivacyDailyCleanup(f.database)
	require.Error(t, cleanup(f.ctx, f.now))
	var cleared int64
	require.NoError(t, q.Table("inquiry").Where("pii_purged_at IS NOT NULL").Count(&cleared).Error)
	require.Zero(t, cleared)
	rawCount(f.ctx, t, f.database, "share_privacy_run", 0)
	require.NoError(t, q.Exec("DROP TRIGGER reject_privacy_audit ON audit_log").Error)
	var auditNow time.Time
	require.NoError(t, q.Raw("SELECT CURRENT_TIMESTAMP").Scan(&auditNow).Error)
	auditCutoff := auditNow.UTC().AddDate(-3, 0, 0)
	require.NoError(t, q.Exec(`INSERT INTO audit_log(tenant_id,actor_type,action,resource_type,resource_id,trace_id,result,created_at)
		VALUES(1,'system','share.inquiry.submit','inquiry',?,'retention-fixture','success',?)`, fmt.Sprint(id), auditCutoff).Error)
	require.NoError(t, cleanup(f.ctx, f.now))
	require.NoError(t, q.Table("inquiry").Where("pii_purged_at IS NOT NULL").Count(&cleared).Error)
	require.EqualValues(t, 1, cleared)
	var old int64
	require.NoError(t, q.Table("audit_log").Where("action='share.inquiry.submit' AND created_at<=?", auditCutoff).Count(&old).Error)
	require.Zero(t, old)
	t.Log("cleanup audit failure rolls erasure back and leaves no completed watermark; retry succeeds; share security audit expires at three years")
}
