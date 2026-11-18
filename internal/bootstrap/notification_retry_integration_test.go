package bootstrap

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/omwk/go-admin-api/internal/kernel/errs"
	"github.com/omwk/go-admin-api/internal/kernel/notifier/channel"
	"github.com/stretchr/testify/require"
)

func retryDeliveryHTTP(t *testing.T, f *notificationFixtureF3, id int64, key string) *httptest.ResponseRecorder {
	t.Helper()
	w := f.request(t, http.MethodPost, "/deliveries/"+fmtNotificationID(id)+"/retry", key, nil)
	require.NotContains(t, w.Body.String(), f.email)
	require.NotContains(t, w.Body.String(), f.phone)
	return w
}

func deliverySnapshot(t *testing.T, f *notificationFixtureF3, id int64) string {
	t.Helper()
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	var raw string
	require.NoError(t, q.Raw("SELECT row_to_json(d)::text FROM notify_delivery d WHERE id=?", id).Scan(&raw).Error)
	return raw
}

func TestAdminNotificationRetryIntegrationFailedQueuesThenDelivers(t *testing.T) {
	// BE-IDN-015 §3 #6: HTTP queues; the existing dispatcher later succeeds.
	f := newNotificationFixtureF3(t)
	accept := &atomic.Bool{}
	calls := &atomic.Int32{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		if accept.Load() {
			w.WriteHeader(202)
		} else {
			w.WriteHeader(503)
		}
	}))
	defer server.Close()
	f.provider(t, "http", "recoverable", 0, true, channel.Config{"endpoint": server.URL, "secret": f.token})
	failed := f.send(t, "webhook")
	require.Equal(t, "failed", failed.Status)
	w := retryDeliveryHTTP(t, f, failed.ID, "failed-retry")
	require.Equal(t, 200, w.Code, w.Body.String())
	var queued channel.Delivery
	notificationDataF3(t, w, &queued)
	require.Equal(t, "pending", queued.Status)
	require.Nil(t, queued.NextRetryAt)
	require.EqualValues(t, 1, calls.Load())
	accept.Store(true)
	sent, err := f.manager.delivery.Deliver(f.ctx, 1, failed.ID)
	require.NoError(t, err)
	require.Equal(t, "sent", sent.Status)
	require.EqualValues(t, 2, calls.Load())
	before := deliverySnapshot(t, f, failed.ID)
	require.Equal(t, 200, retryDeliveryHTTP(t, f, failed.ID, "failed-retry").Code)
	require.Equal(t, before, deliverySnapshot(t, f, failed.ID))
	require.EqualValues(t, 2, calls.Load())
	t.Log("§3 #6: failed -> HTTP 200 pending -> dispatcher sent; calls=2 including original failure; replay leaves sent unchanged")
	f.assertPrivacy(t, f.token)
}

func TestAdminNotificationRetryIntegrationPendingBoundaryAndSent(t *testing.T) {
	// BE-IDN-015 §3 #7/#8: strict age, untouched attempts, and active lease safety.
	f := newNotificationFixtureF3(t)
	for _, tc := range []struct {
		name     string
		age      time.Duration
		attempts int
		status   string
		lease    time.Duration
		want     int
	}{
		{"recent", 4 * time.Minute, 0, "pending", 0, 409},
		{"exactly_five", 5 * time.Minute, 0, "pending", 0, 409},
		{"stale", 5*time.Minute + time.Second, 0, "pending", 0, 200},
		{"already_attempted", 6 * time.Minute, 1, "pending", 0, 409},
		{"active_lease", 6 * time.Minute, 0, "pending", time.Minute, 409},
		{"expired_lease", 6 * time.Minute, 0, "pending", -time.Second, 200},
		{"sent", 6 * time.Minute, 1, "sent", 0, 409},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := enqueueReadableDelivery(t, f, "webhook", "notification.test")
			var lease *time.Time
			if tc.lease != 0 {
				at := f.now.Add(tc.lease)
				lease = &at
				require.NoError(t, f.database.Exec(f.ctx, "UPDATE notify_outbox SET status='processing' WHERE id=?", d.ID))
			}
			require.NoError(t, f.database.Exec(f.ctx, "UPDATE notify_delivery SET status=?,attempts=?,created_at=?,next_retry_at=? WHERE id=?", tc.status, tc.attempts, f.now.Add(-tc.age), lease, d.ID))
			before := deliverySnapshot(t, f, d.ID)
			w := retryDeliveryHTTP(t, f, d.ID, "")
			require.Equal(t, tc.want, w.Code, w.Body.String())
			if tc.want == 409 {
				require.Equal(t, before, deliverySnapshot(t, f, d.ID))
			} else {
				var retried channel.Delivery
				notificationDataF3(t, w, &retried)
				require.Equal(t, "pending", retried.Status)
				require.Nil(t, retried.NextRetryAt)
			}
		})
	}
	t.Log("§3 #7/#8: 4m=409, exactly 5m=409, 5m1s=200, attempts>0=409, active lease=409, sent=409 and unchanged")
}

func TestAdminNotificationRetryIntegrationMixedBatchAndTenantIsolation(t *testing.T) {
	// BE-IDN-015 §3 #9: failed + stale pending + sent produces {2,1}.
	f := newNotificationFixtureF3(t)
	failed := enqueueReadableDelivery(t, f, "webhook", "notification.test")
	stale := enqueueReadableDelivery(t, f, "webhook", "notification.test")
	sent := enqueueReadableDelivery(t, f, "webhook", "notification.test")
	require.NoError(t, f.database.Exec(f.ctx, "UPDATE notify_delivery SET status='failed',attempts=1 WHERE id=?", failed.ID))
	require.NoError(t, f.database.Exec(f.ctx, "UPDATE notify_outbox SET status='failed' WHERE id=?", failed.ID))
	require.NoError(t, f.database.Exec(f.ctx, "UPDATE notify_delivery SET created_at=? WHERE id=?", f.now.Add(-6*time.Minute), stale.ID))
	require.NoError(t, f.database.Exec(f.ctx, "UPDATE notify_delivery SET status='sent',attempts=1,sent_at=? WHERE id=?", f.now, sent.ID))
	require.NoError(t, f.database.Exec(f.ctx, "UPDATE notify_outbox SET status='sent' WHERE id=?", sent.ID))
	before := deliverySnapshot(t, f, sent.ID)
	body := map[string]any{"ids": []int64{sent.ID, stale.ID, failed.ID}}
	w := f.request(t, http.MethodPost, "/deliveries/retry", "batch", body)
	require.Equal(t, 200, w.Code, w.Body.String())
	var out channel.RetryBatchResult
	notificationDataF3(t, w, &out)
	require.Equal(t, channel.RetryBatchResult{Retried: 2, Skipped: 1}, out)
	require.Equal(t, before, deliverySnapshot(t, f, sent.ID))
	replay := f.request(t, http.MethodPost, "/deliveries/retry", "batch", body)
	require.Equal(t, 200, replay.Code)
	var replayed channel.RetryBatchResult
	notificationDataF3(t, replay, &replayed)
	require.Equal(t, out, replayed)
	server, calls := mockAcceptedF3(t)
	f.provider(t, "http", "batch-worker", 0, true, channel.Config{"endpoint": server.URL, "secret": f.token})
	for _, id := range []int64{failed.ID, stale.ID} {
		delivered, err := f.manager.delivery.Deliver(f.ctx, 1, id)
		require.NoError(t, err)
		require.Equal(t, "sent", delivered.Status)
	}
	require.EqualValues(t, 2, calls.Load())
	_, err := f.manager.Retry(f.ctx, channel.Actor{TenantID: 2}, failed.ID)
	require.Equal(t, 404, errs.From(err).HTTPStatus)
	require.Equal(t, before, deliverySnapshot(t, f, sent.ID))
	t.Log("§3 #9: mixed batch -> retried=2 skipped=1; replay unchanged; dispatcher sends twice; sent row unchanged; foreign tenant=404")
	f.assertPrivacy(t, f.token)
}

func TestAdminNotificationRetryIntegrationBatchValidationAndConcurrentCalls(t *testing.T) {
	f := newNotificationFixtureF3(t)
	for _, ids := range [][]int64{nil, {}, {0}, {-1}, {1, 1}, make([]int64, 101)} {
		require.Equal(t, 400, f.request(t, http.MethodPost, "/deliveries/retry", "", map[string]any{"ids": ids}).Code)
	}
	d := f.send(t, "webhook")
	require.Equal(t, "skipped", d.Status)
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results <- f.manager.store.Transaction(f.ctx, func(ctx context.Context) error {
				_, err := f.manager.Retry(ctx, f.actor, d.ID)
				return err
			})
		}()
	}
	wg.Wait()
	close(results)
	success, conflicts := 0, 0
	for err := range results {
		if err == nil {
			success++
		} else {
			require.Equal(t, 409, errs.From(err).HTTPStatus)
			conflicts++
		}
	}
	require.Equal(t, 1, success)
	require.Equal(t, 1, conflicts)
	t.Log("batch bounds/IDs validated; concurrent skipped retries -> one success, one 409")
}
