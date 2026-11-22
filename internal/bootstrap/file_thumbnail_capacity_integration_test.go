package bootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	filemodule "github.com/omwk/go-admin-api/internal/modules/file"
	"github.com/omwk/go-admin-api/internal/testenv"
	"github.com/stretchr/testify/require"
)

func TestFileThumbnailIntegrationFivePhotoCapacity(t *testing.T) {
	// BE-IDN-028 §4.7: this full-scale measurement is excluded from normal PR work.
	if testenv.Lookup("THUMB_CAPACITY") != "1" {
		t.Skip("set THUMB_CAPACITY=1 and THUMB_PHOTO_PATH for the five-photo measurement")
	}
	data := thumbnailRealPhoto(t)
	f := newThumbnailFixture(t)
	runtime.GC()
	var baseline runtime.MemStats
	runtime.ReadMemStats(&baseline)
	peakHeap, stopSample := sampleThumbnailHeap(t)
	started := time.Now()
	views := concurrentThumbnailUploads(t, f, data)
	uploadElapsed := time.Since(started)
	var active, peak, completed atomic.Int64
	cfg, publisher := thumbnailQueueFixture(t, f)
	startThumbnailRuntime(t, f, cfg, func(generate func(context.Context, int64, int64) error) func(context.Context, int64, int64) error {
		return func(ctx context.Context, tenant, id int64) error {
			n := active.Add(1)
			defer active.Add(-1)
			updateThumbnailPeak(&peak, n)
			err := generate(ctx, tenant, id)
			completed.Add(1)
			return err
		}
	})
	dispatchThumbnailEvents(t, f, publisher)
	require.Eventually(t, func() bool { return completed.Load() == 5 }, 60*time.Second, 50*time.Millisecond)
	for _, view := range views {
		assertThumbnailReady(t, f, view.ID)
	}
	stopSample()
	require.EqualValues(t, 2, peak.Load())
	t.Logf("#7 five concurrent real-photo HTTP uploads=201 pending; upload_ms=%d total_ms=%d peak_task_concurrency=%d baseline_heap_bytes=%d peak_heap_bytes=%d heap_growth_bytes=%d no OOM", uploadElapsed.Milliseconds(), time.Since(started).Milliseconds(), peak.Load(), baseline.HeapAlloc, peakHeap.Load(), peakHeap.Load()-baseline.HeapAlloc)
}

func concurrentThumbnailUploads(t *testing.T, f *fileStorageFixture, data []byte) []filemodule.View {
	t.Helper()
	requests := make([]*http.Request, 5)
	responses := make([]*httptest.ResponseRecorder, 5)
	for i := range requests {
		var body bytes.Buffer
		writer := multipart.NewWriter(&body)
		part, err := writer.CreateFormFile("file", fmt.Sprintf("concurrent-%d.jpg", i))
		require.NoError(t, err)
		_, err = part.Write(data)
		require.NoError(t, err)
		_, err = part.Write([]byte{byte(i)}) // Distinct hashes, same decoded photograph.
		require.NoError(t, err)
		require.NoError(t, writer.Close())
		requests[i] = httptest.NewRequest("POST", "/api/v1/admin/files", &body)
		requests[i].Header.Set("Content-Type", writer.FormDataContentType())
		requests[i].Header.Set("Authorization", "Bearer "+f.bearer)
		responses[i] = httptest.NewRecorder()
	}
	var group sync.WaitGroup
	for i := range requests {
		group.Add(1)
		go func(i int) { defer group.Done(); f.router.ServeHTTP(responses[i], requests[i]) }(i)
	}
	group.Wait()
	views := make([]filemodule.View, 5)
	for i, response := range responses {
		require.Equal(t, 201, response.Code)
		var envelope struct{ Data filemodule.View }
		require.NoError(t, json.Unmarshal(response.Body.Bytes(), &envelope))
		views[i] = envelope.Data
		require.Equal(t, "pending", views[i].ThumbStatus)
		require.Nil(t, views[i].ThumbURL)
	}
	return views
}

func updateThumbnailPeak(peak *atomic.Int64, value int64) {
	for {
		old := peak.Load()
		if value <= old || peak.CompareAndSwap(old, value) {
			return
		}
	}
}

func sampleThumbnailHeap(t *testing.T) (*atomic.Uint64, func()) {
	t.Helper()
	var peak atomic.Uint64
	stop, done := make(chan struct{}), make(chan struct{})
	var once sync.Once
	go func() {
		defer close(done)
		ticker := time.NewTicker(10 * time.Millisecond)
		defer ticker.Stop()
		for {
			var memory runtime.MemStats
			runtime.ReadMemStats(&memory)
			for {
				old := peak.Load()
				if memory.HeapAlloc <= old || peak.CompareAndSwap(old, memory.HeapAlloc) {
					break
				}
			}
			select {
			case <-stop:
				return
			case <-ticker.C:
			}
		}
	}()
	finish := func() { once.Do(func() { close(stop); <-done }) }
	t.Cleanup(finish)
	return &peak, finish
}
