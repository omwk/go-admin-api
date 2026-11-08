package bootstrap

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/omwk/go-admin-api/internal/modules/shop/model"
	"github.com/omwk/go-admin-api/internal/modules/shop/refresh"
	"github.com/omwk/go-admin-api/internal/modules/shop/refresh/port"
	"github.com/stretchr/testify/require"
)

func TestCredentialRefreshIntegrationTenConcurrentTriggers(t *testing.T) {
	f := newCredentialRefreshFixture(t)
	entered, release := make(chan struct{}), make(chan struct{})
	handler := f.platform.handle
	f.platform.handle = func(ctx context.Context, r port.Request) (port.Result, error) {
		close(entered)
		select {
		case <-release:
			return handler(ctx, r)
		case <-ctx.Done():
			return port.Result{}, ctx.Err()
		}
	}
	ctx, cancel := context.WithTimeout(f.ctx, 10*time.Second)
	defer cancel()
	start := make(chan struct{})
	results := make(chan error, 10)
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			run, err := f.executor.Prepare(ctx, f.target)
			if err == nil {
				_, err = f.executor.Execute(ctx, f.target, run.ID)
			}
			results <- err
		}()
	}
	close(start)
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("refresh did not start")
	}
	for i := 0; i < 9; i++ {
		select {
		case err := <-results:
			require.NoError(t, err)
		case <-ctx.Done():
			t.Fatal("duplicates did not finish")
		}
	}
	close(release)
	wg.Wait()
	require.NoError(t, <-results)
	require.Equal(t, int32(1), f.platform.calls.Load())
	q, stop := f.database.WithContext(f.ctx)
	defer stop()
	var runs, versions int64
	require.NoError(t, q.Model(&model.RefreshRun{}).Count(&runs).Error)
	require.NoError(t, q.Model(&model.CredentialVersion{}).Count(&versions).Error)
	require.EqualValues(t, 1, runs)
	require.EqualValues(t, 2, versions)
	f.assertSafe(t)
}

func TestCredentialRefreshIntegrationTemporaryBudgetSurvivesRestart(t *testing.T) {
	f := newCredentialRefreshFixture(t)
	f.platform.handle = func(context.Context, port.Request) (port.Result, error) {
		return port.Result{}, &port.Failure{HTTPStatus: 503, NotConsumed: true}
	}
	for i := 1; i <= 5; i++ {
		f.executor = f.newExecutor(t, f.refreshStore)
		run := f.prepare(t)
		require.Equal(t, i, run.Attempt)
		run, err := f.executor.Execute(f.ctx, f.target, run.ID)
		require.NoError(t, err)
		row, _ := f.readStored(t, f.target.CredentialID, 1)
		require.Equal(t, i, row.ConsecutiveFailures)
		require.Equal(t, 5-i, row.RetryBudget)
		require.NotEqual(t, "needs_reauth", row.Status)
		duplicate := f.prepare(t)
		require.Equal(t, run.ID, duplicate.ID)
		_, err = f.executor.Execute(f.ctx, f.target, duplicate.ID)
		require.NoError(t, err)
		require.EqualValues(t, i, f.platform.calls.Load())
		f.now = run.NextRetryAt.Add(time.Microsecond)
	}
	sixth := f.prepare(t)
	_, err := f.executor.Execute(f.ctx, f.target, sixth.ID)
	require.NoError(t, err)
	require.Equal(t, int32(5), f.platform.calls.Load())
}

func TestCredentialRefreshIntegrationRevocationDoesNotIncrementTemporaryCounter(t *testing.T) {
	f := newCredentialRefreshFixture(t)
	f.platform.handle = func(context.Context, port.Request) (port.Result, error) {
		return port.Result{}, &port.Failure{HTTPStatus: 429, NotConsumed: true}
	}
	first := f.prepare(t)
	first, err := f.executor.Execute(f.ctx, f.target, first.ID)
	require.NoError(t, err)
	f.now = first.NextRetryAt.Add(time.Microsecond)
	f.platform.handle = func(context.Context, port.Request) (port.Result, error) {
		return port.Result{}, &port.Failure{Revoked: true}
	}
	second := f.prepare(t)
	second, err = f.executor.Execute(f.ctx, f.target, second.ID)
	require.NoError(t, err)
	row, version := f.readStored(t, f.target.CredentialID, 1)
	require.Equal(t, "needs_reauth", row.Status)
	require.Equal(t, 1, row.ConsecutiveFailures)
	require.Equal(t, 4, row.RetryBudget)
	require.Equal(t, refresh.FailureInvalid, second.FailureClass)
	require.Nil(t, version.RetiredAt)
	_, err = f.executor.Execute(f.ctx, f.target, second.ID)
	require.NoError(t, err)
	require.Equal(t, int32(2), f.platform.calls.Load())
}

func TestCredentialRefreshIntegrationTenantTargetCannotCrossBoundary(t *testing.T) {
	f := newCredentialRefreshFixture(t)
	run := f.prepare(t)
	other := f.target
	other.TenantID++
	_, err := f.executor.Prepare(f.ctx, other)
	require.Error(t, err)
	_, err = f.executor.Execute(f.ctx, other, run.ID)
	require.Error(t, err)
	require.Zero(t, f.platform.calls.Load())
}
