package bootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/omwk/go-admin-api/internal/kernel/audit"
	"github.com/omwk/go-admin-api/internal/modules/identity"
	"github.com/omwk/go-admin-api/internal/modules/shop"
	"github.com/stretchr/testify/require"
)

func TestShopIdempotencyIntegrationReplayMismatchAndRollback(t *testing.T) {
	f, service := newIdempotentShopFixture(t)
	key := "shop-create-replay"
	payload := map[string]any{"platform_id": 1, "name": "idem-shop", "external_shop_id": "idem-shop-ext", "timezone": "UTC", "currency": "CNY"}
	first := idemShopRequest(t, f.router, http.MethodPost, "/shops", payload, f.bearer, key)
	second := idemShopRequest(t, f.router, http.MethodPost, "/shops", payload, f.bearer, key)
	require.Equal(t, http.StatusCreated, first.Code)
	require.Equal(t, first.Code, second.Code)
	require.True(t, bytes.Equal(first.Body.Bytes(), second.Body.Bytes()))
	var count int64
	q, cancel := f.database.WithContext(f.ctx)
	require.NoError(t, q.Table("shop").Where("external_shop_id = ?", "idem-shop-ext").Count(&count).Error)
	cancel()
	require.EqualValues(t, 1, count)
	mismatch := idemShopRequest(t, f.router, http.MethodPost, "/shops", map[string]any{"platform_id": 1, "name": "changed", "external_shop_id": "idem-shop-ext-2", "timezone": "UTC", "currency": "CNY"}, f.bearer, key)
	require.Equal(t, http.StatusConflict, mismatch.Code)
	var envelope struct {
		Code int `json:"code"`
	}
	require.NoError(t, json.Unmarshal(mismatch.Body.Bytes(), &envelope))
	require.Equal(t, 10006, envelope.Code)

	service.UseIdempotency(newIdempotencyStore(f.database), func(context.Context, audit.Event) error { return errors.New("injected audit failure") })
	failingKey := "shop-create-rollback"
	failing := idemShopRequest(t, f.router, http.MethodPost, "/shops", map[string]any{"platform_id": 1, "name": "rollback", "external_shop_id": "rollback-ext", "timezone": "UTC", "currency": "CNY"}, f.bearer, failingKey)
	require.Equal(t, http.StatusInternalServerError, failing.Code)
	service.UseIdempotency(newIdempotencyStore(f.database), func(ctx context.Context, event audit.Event) error { return audit.Record(ctx, f.database, event) })
	retried := idemShopRequest(t, f.router, http.MethodPost, "/shops", map[string]any{"platform_id": 1, "name": "rollback", "external_shop_id": "rollback-ext", "timezone": "UTC", "currency": "CNY"}, f.bearer, failingKey)
	require.Equal(t, http.StatusCreated, retried.Code)
}

func TestShopIdempotencyIntegrationConcurrentSingleWinner(t *testing.T) {
	f, service := newIdempotentShopFixture(t)
	service.UseIdempotency(newIdempotencyStore(f.database), func(ctx context.Context, event audit.Event) error { return audit.Record(ctx, f.database, event) })
	payload := map[string]any{"platform_id": 1, "name": "concurrent-idem", "external_shop_id": "concurrent-idem-ext", "timezone": "UTC", "currency": "CNY"}
	responses := make([]*httptest.ResponseRecorder, 2)
	var wait sync.WaitGroup
	for i := range responses {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			responses[index] = idemShopRequest(t, f.router, http.MethodPost, "/shops", payload, f.bearer, "concurrent-key")
		}(i)
	}
	wait.Wait()
	for _, response := range responses {
		require.Contains(t, []int{http.StatusCreated, http.StatusConflict}, response.Code)
	}
	var count int64
	q, cancel := f.database.WithContext(f.ctx)
	require.NoError(t, q.Table("shop").Where("external_shop_id = ?", "concurrent-idem-ext").Count(&count).Error)
	cancel()
	require.EqualValues(t, 1, count)
}

func TestShopIdempotencyIntegrationExpiredKeyCanExecuteAgain(t *testing.T) {
	f, _ := newIdempotentShopFixture(t)
	firstPayload := map[string]any{"platform_id": 1, "name": "expired-one", "external_shop_id": "expired-one-ext", "timezone": "UTC", "currency": "CNY"}
	first := idemShopRequest(t, f.router, http.MethodPost, "/shops", firstPayload, f.bearer, "expired-key")
	require.Equal(t, http.StatusCreated, first.Code)
	q, cancel := f.database.WithContext(f.ctx)
	require.NoError(t, q.Exec("UPDATE idempotency_key SET expires_at = now() - interval '1 minute'").Error)
	cancel()
	secondPayload := map[string]any{"platform_id": 1, "name": "expired-two", "external_shop_id": "expired-two-ext", "timezone": "UTC", "currency": "CNY"}
	second := idemShopRequest(t, f.router, http.MethodPost, "/shops", secondPayload, f.bearer, "expired-key")
	require.Equal(t, http.StatusCreated, second.Code)
}

func TestCredentialIdempotencyIntegrationReplay(t *testing.T) {
	f := newCredentialFixture(t)
	f.service.UseIdempotency(newIdempotencyStore(f.database), func(ctx context.Context, event audit.Event) error { return audit.Record(ctx, f.database, event) })
	payload := map[string]any{"kind": "oauth2", "label": "idem", "values": map[string]string{"private_value": f.randomValue(t)}}
	first := idemShopRequest(t, f.router, http.MethodPost, f.shopPath()+"/credentials", payload, f.bearer, "credential-replay")
	second := idemShopRequest(t, f.router, http.MethodPost, f.shopPath()+"/credentials", payload, f.bearer, "credential-replay")
	require.Equal(t, http.StatusCreated, first.Code)
	require.Equal(t, first.Code, second.Code)
	require.True(t, bytes.Equal(first.Body.Bytes(), second.Body.Bytes()))
	var count int64
	q, cancel := f.database.WithContext(f.ctx)
	require.NoError(t, q.Table("shop_credential").Where("shop_id = ?", f.shop.ID).Count(&count).Error)
	cancel()
	require.EqualValues(t, 1, count)
}

func newIdempotentShopFixture(t *testing.T) (*adminFixture, *shop.Service) {
	t.Helper()
	f := newAdminFixture(t)
	profiles, err := identity.NewProfileService(newProfileStore(f.database))
	require.NoError(t, err)
	service, err := shop.NewService(newShopStore(f.database), f.store.Authorize)
	require.NoError(t, err)
	service.UseIdempotency(newIdempotencyStore(f.database), func(ctx context.Context, event audit.Event) error { return audit.Record(ctx, f.database, event) })
	require.NoError(t, shop.RegisterRoutes(f.router, f.identityFixture.service, profiles, service))
	return f, service
}

func idemShopRequest(t *testing.T, router http.Handler, method, path string, payload any, bearer, key string) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(payload)
	require.NoError(t, err)
	request := httptest.NewRequest(method, "/api/v1/admin"+path, bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+bearer)
	request.Header.Set("Idempotency-Key", key)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	return response
}
