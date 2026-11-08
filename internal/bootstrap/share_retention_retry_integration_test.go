package bootstrap

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestSharePrivacyIntegrationPartialRunPreservesRetryCounts(t *testing.T) {
	f := newShareAccessFixture(t)
	id := submitInquiryID(t, f)
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	require.NoError(t, q.Table("inquiry").Where("id=?", id).Update("created_at", f.now.Add(-181*24*time.Hour)).Error)
	require.NoError(t, q.Table("share_visit").Where("tenant_id=1").Update("created_at", f.now.Add(-91*24*time.Hour)).Error)
	require.NoError(t, f.database.Exec(f.ctx, `CREATE FUNCTION reject_visit_cleanup() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'retention unavailable'; END $$`))
	require.NoError(t, f.database.Exec(f.ctx, `CREATE TRIGGER reject_visit_cleanup BEFORE DELETE ON share_visit FOR EACH ROW EXECUTE FUNCTION reject_visit_cleanup()`))
	require.Error(t, cleanupSharePrivacy(f.ctx, f.database, f.now))
	var progress struct {
		Inquiries, Visits int64
		CompletedAt       *time.Time
	}
	require.NoError(t, q.Table("share_privacy_run").Take(&progress).Error)
	require.EqualValues(t, 1, progress.Inquiries)
	require.Nil(t, progress.CompletedAt)
	require.NoError(t, q.Exec("DROP TRIGGER reject_visit_cleanup ON share_visit").Error)
	require.NoError(t, cleanupSharePrivacy(f.ctx, f.database, f.now))
	require.NoError(t, q.Table("share_privacy_run").Take(&progress).Error)
	require.EqualValues(t, 1, progress.Inquiries)
	require.EqualValues(t, 1, progress.Visits)
	require.NotNil(t, progress.CompletedAt)
	t.Log("partial cleanup retries retain cumulative counts; only a fully completed day gets a completion watermark")
}
