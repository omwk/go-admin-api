package bootstrap

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSyncResolveTaskReturnsExistingRunForValidJob(t *testing.T) {
	f := newSyncFixture(t, nil)
	run := f.run(t)

	task, err := NewSyncStore(f.database).ResolveTask(f.ctx, 1, f.jobID, run.ID)
	require.NoError(t, err)
	require.Equal(t, f.jobID, task.JobID)
	require.Equal(t, run.ID, task.RunID)
	require.Equal(t, f.shop.ID, task.ShopID)
	require.Equal(t, "product", task.Resource)
}
