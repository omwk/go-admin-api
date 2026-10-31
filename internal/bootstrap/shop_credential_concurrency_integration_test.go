package bootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/omwk/go-admin-api/internal/kernel/crypto"
	"github.com/omwk/go-admin-api/internal/modules/shop"
	shopmodel "github.com/omwk/go-admin-api/internal/modules/shop/model"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type credentialBlockingSealer struct {
	crypto.Sealer
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (s *credentialBlockingSealer) Seal(plaintext []byte, aad string) ([]byte, []byte, int, error) {
	s.once.Do(func() { close(s.entered) })
	select {
	case <-s.release:
		return s.Sealer.Seal(plaintext, aad)
	case <-time.After(2 * time.Second):
		return nil, nil, 0, context.DeadlineExceeded
	}
}

func TestCredentialIntegrationT24ConcurrentHTTPRotationsHaveOneWinner(t *testing.T) {
	// REQ-SHP-006: both requests observe v1 before either commits; only one v2 may exist.
	f := newCredentialFixture(t)
	created, _ := f.create(t, "oauth2")
	block := &credentialBlockingSealer{Sealer: f.sealer, entered: make(chan struct{}), release: make(chan struct{})}
	service, err := shop.NewService(f.store, f.adminFixture.store.Authorize, block)
	require.NoError(t, err)
	f.useService(t, service)
	secondObserved := make(chan struct{})
	var calls atomic.Int32
	raw := f.store.raw
	f.store.raw = func(ctx context.Context) (*gorm.DB, context.CancelFunc) {
		select {
		case <-block.entered:
			// After the first request is inside Seal, the second raw call is its shop lock:
			// its preceding parent SELECT has completed and observed the original pointer.
			if calls.Add(1) == 2 {
				close(secondObserved)
			}
		default:
		}
		return raw(ctx)
	}
	requests := make([]*http.Request, 2)
	for i := range requests {
		body, err := json.Marshal(map[string]any{"values": map[string]string{"private_value": f.randomValue(t)}})
		require.NoError(t, err)
		requests[i] = httptest.NewRequest("PUT", "/api/v1/admin"+credentialPath(created.ID), bytes.NewReader(body))
		requests[i].Header.Set("Content-Type", "application/json")
		requests[i].Header.Set("Authorization", "Bearer "+f.bearer)
	}
	responses := make(chan *httptest.ResponseRecorder, 2)
	serve := func(request *http.Request) {
		response := httptest.NewRecorder()
		f.router.ServeHTTP(response, request)
		responses <- response
	}
	go serve(requests[0])
	awaitCredentialSignal(t, block.entered)
	go serve(requests[1])
	awaitCredentialSignal(t, secondObserved)
	close(block.release)
	statuses := []int{}
	for range requests {
		select {
		case response := <-responses:
			statuses = append(statuses, response.Code)
			f.noLeaks(t, "concurrent HTTP response", response.Body.String())
			if response.Code == 409 {
				require.Contains(t, response.Body.String(), `"code":10409`)
			}
		case <-time.After(4 * time.Second):
			t.Fatal("concurrent request did not terminate")
		}
	}
	require.ElementsMatch(t, []int{200, 409}, statuses)
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	var numbers []int
	require.NoError(t, q.Model(&shopmodel.CredentialVersion{}).Where("credential_id = ?", created.ID).Order("version_no").Pluck("version_no", &numbers).Error)
	require.Equal(t, []int{1, 2}, numbers)
	var rotations int64
	require.NoError(t, q.Model(&shopmodel.CredentialAudit{}).Where("credential_id = ? AND action = 'rotate'", created.ID).Count(&rotations).Error)
	require.EqualValues(t, 1, rotations)
}

func awaitCredentialSignal(t *testing.T, signal <-chan struct{}) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(2 * time.Second):
		t.Fatal("credential synchronization timed out")
	}
}
