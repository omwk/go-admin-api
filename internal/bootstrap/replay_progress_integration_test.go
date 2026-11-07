package bootstrap

import (
	"encoding/json"
	"testing"
	"time"

	syncmodule "github.com/omwk/go-admin-api/internal/modules/sync"
	"github.com/stretchr/testify/require"
)

func TestReplayIntegrationETAUsesWallClockAndExpires(t *testing.T) {
	f := replayFixture(t, 601)
	run := createReplay(t, f)
	now := run.StartedAt.Add(time.Second)
	f.engineStore.now = func() time.Time { return now }
	for n := 0; n < 3; n++ {
		now = run.StartedAt.Add(time.Duration(1+n*30) * time.Second)
		var err error
		started := time.Now()
		run, err = f.engineStore.ProcessReplay(f.ctx, 1, run.ID, run.Processed)
		t.Logf("D1 replay batch=%d elapsed=%s error=%v", n+1, time.Since(started), err)
		require.NoError(t, err)
		progress, err := f.engineStore.ReadReplay(f.ctx, run.ID)
		require.NoError(t, err)
		if n < 2 {
			require.Nil(t, progress.ETASeconds)
		} else {
			require.NotNil(t, progress.ETASeconds)
			require.EqualValues(t, 1, *progress.ETASeconds)
			require.NotNil(t, progress.LastCommittedAt)
		}
	}
	now = now.Add(6 * time.Minute)
	progress, err := f.engineStore.ReadReplay(f.ctx, run.ID)
	require.NoError(t, err)
	require.Nil(t, progress.ETASeconds)
}

func TestReplayIntegrationHTTPListAndIdempotentCancel(t *testing.T) {
	f := replayFixture(t, 1)
	run := createReplay(t, f)
	response, envelope := f.request(t, "GET", "/replays?page=1&page_size=20", nil)
	require.Equal(t, 200, response.Code, response.Body.String())
	var page struct {
		List     []syncmodule.ReplayRun `json:"list"`
		Total    int64                  `json:"total"`
		Page     int                    `json:"page"`
		PageSize int                    `json:"page_size"`
	}
	require.NoError(t, json.Unmarshal(envelope.Data, &page))
	require.EqualValues(t, 1, page.Total)
	require.Len(t, page.List, 1)
	require.Equal(t, 1, page.Page)
	require.Equal(t, 20, page.PageSize)
	path := "/replays/" + itoa(run.ID) + "/cancel"
	first := idemShopRequest(t, f.router, "POST", path, struct{}{}, f.bearer, "cancel-once")
	second := idemShopRequest(t, f.router, "POST", path, struct{}{}, f.bearer, "cancel-once")
	require.Equal(t, 200, first.Code, first.Body.String())
	require.Equal(t, 200, second.Code, second.Body.String())
	var a, b struct {
		Data syncmodule.ReplayRun `json:"data"`
	}
	require.NoError(t, json.Unmarshal(first.Body.Bytes(), &a))
	require.NoError(t, json.Unmarshal(second.Body.Bytes(), &b))
	require.Equal(t, "cancelled", a.Data.Status)
	require.Equal(t, a.Data, b.Data)
	_, err := f.engineStore.ProcessReplay(f.ctx, 1, run.ID, 0)
	require.NoError(t, err)
	assertNoReplayTargets(t, f)
}
