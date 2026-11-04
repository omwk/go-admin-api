package bootstrap

import (
	"testing"
	"time"

	"github.com/omwk/go-admin-api/internal/modules/sync/repo"
	"github.com/stretchr/testify/require"
)

func TestResourceCycleCompletionRequiresCompleteSuccessfulNonEmptyCycle(t *testing.T) {
	finished := time.Now().UTC()
	for _, tc := range []struct {
		name string
		run  repo.Run
		page repo.Page
		want bool
	}{
		{name: "success", run: repo.Run{Status: "success", FinishedAt: &finished, Stats: repo.Stats{Fetched: 1}}, want: true},
		{name: "failed page", run: repo.Run{Status: "success", FinishedAt: &finished, Stats: repo.Stats{Fetched: 1, CycleFailed: true}}},
		{name: "failed record", run: repo.Run{Status: "success", FinishedAt: &finished, Stats: repo.Stats{Fetched: 1, Failed: 1}}},
		{name: "partial unfinished", run: repo.Run{Status: "partial", Stats: repo.Stats{Fetched: 1, Pending: true}}, page: repo.Page{HasMore: true}},
		{name: "empty", run: repo.Run{Status: "success", FinishedAt: &finished}},
		{name: "empty page", run: repo.Run{Status: "success", FinishedAt: &finished}, page: repo.Page{}},
		{name: "replay", run: repo.Run{Status: "success", FinishedAt: &finished, Stats: repo.Stats{Fetched: 1, ReplayID: 1}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, resourceCycleComplete(tc.run, tc.page))
		})
	}
}
