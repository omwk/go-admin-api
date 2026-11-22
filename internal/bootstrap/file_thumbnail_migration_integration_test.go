package bootstrap

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFileThumbnailIntegrationMigration(t *testing.T) {
	// BE-IDN-028 §4.9: reversible 000040, defaults and existing thumbnails remain usable.
	f := newFileStorageFixture(t)
	state, err := f.database.Migrate(f.ctx, "down")
	require.NoError(t, err)
	require.EqualValues(t, 39, state.Version)
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	require.NoError(t, q.Exec(`INSERT INTO file_object (tenant_id,bucket,object_key,original_name,mime,size_bytes,sha256,purpose,thumb_key)
		VALUES (1,'migration','legacy','legacy.jpg','image/jpeg',10,'legacy-hash','attachment','legacy.thumb.jpg'),
		(1,'migration','plain','plain.txt','text/plain',10,'plain-hash','attachment',NULL)`).Error)
	state, err = f.database.Migrate(f.ctx, "up")
	require.NoError(t, err)
	require.EqualValues(t, 40, state.Version)
	require.False(t, state.Dirty)
	var rows []struct {
		ObjectKey     string
		ThumbStatus   string
		ThumbAttempts int
	}
	require.NoError(t, q.Table("file_object").Select("object_key,thumb_status,thumb_attempts").Order("id").Find(&rows).Error)
	require.Len(t, rows, 2)
	require.Equal(t, "ready", rows[0].ThumbStatus)
	require.Equal(t, "", rows[1].ThumbStatus)
	require.Zero(t, rows[0].ThumbAttempts)
	require.Zero(t, rows[1].ThumbAttempts)
	for _, statement := range []string{"UPDATE file_object SET thumb_status='unknown'", "UPDATE file_object SET thumb_attempts=4"} {
		require.Error(t, q.Exec(statement).Error)
	}
	t.Log("#9 database.Migrate(down)=39; database.Migrate(up)=40 dirty=false; legacy ready; defaults empty/0; invalid status and retry=4 rejected")
}
