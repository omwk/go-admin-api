package bootstrap

import (
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestMain(m *testing.M) {
	gin.SetMode(gin.TestMode)
	os.Exit(m.Run())
}

var integrationRuntime = struct {
	scheduled sync.Map
	slots     chan struct{}
}{slots: make(chan struct{}, 3)}

// Keep deadline-sensitive and capacity scenarios in Go's serial phase.
// Other tests own at most three live fixtures, including pool/drop cleanup.
func scheduleIntegration(t *testing.T) {
	t.Helper()
	if strings.Contains(t.Name(), "/") {
		return
	}
	for _, part := range []string{"HundredThousand", "Capacity", "Benchmark", "TestReplay", "KillWorkerDuringUpload", "SnapshotIntegrationPublishAndWorker"} {
		if strings.Contains(t.Name(), part) {
			return
		}
	}
	if _, loaded := integrationRuntime.scheduled.LoadOrStore(t, struct{}{}); loaded {
		return
	}
	t.Parallel()
	integrationRuntime.slots <- struct{}{}
	t.Cleanup(func() {
		<-integrationRuntime.slots
		integrationRuntime.scheduled.Delete(t)
	})
}
