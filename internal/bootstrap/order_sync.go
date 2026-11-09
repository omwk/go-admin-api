package bootstrap

import (
	"context"
	"errors"
	"fmt"
	"github.com/omwk/go-admin-api/internal/kernel/cache"
	"github.com/omwk/go-admin-api/internal/kernel/errs"
	"github.com/omwk/go-admin-api/internal/modules/connector"
	"github.com/omwk/go-admin-api/internal/modules/ingestion"
	"github.com/omwk/go-admin-api/internal/modules/shop"
	syncmodule "github.com/omwk/go-admin-api/internal/modules/sync"
	"github.com/omwk/go-admin-api/internal/modules/sync/repo"
	"math"
	"time"
)

type orderAcquisition struct {
	store       *syncEngineStore
	shared      *cache.Cache
	credentials shop.CredentialUsePort
	platforms   *connector.Registry
	raw         *ingestion.Service
	handler     syncmodule.ResourceHandler
}

func orderExecutor(store *syncEngineStore, shared *cache.Cache, credentials shop.CredentialUsePort, platforms *connector.Registry, raw *ingestion.Service, handler syncmodule.ResourceHandler) func(context.Context, repo.Work) error {
	acquisition := &orderAcquisition{store, shared, credentials, platforms, raw, handler}
	return acquisition.execute
}

func (o *orderAcquisition) call(ctx context.Context, work repo.Work, fn func(context.Context, connector.Credential) error) error {
	if _, err := o.store.capability(ctx, "credential.use"); err != nil {
		return err
	}
	key := fmt.Sprintf("sync:bucket:%d:%d", work.Job.TenantID, work.Job.ShopID)
	allowed, _, retry, err := o.shared.TokenBucket(ctx, key, 5, 5)
	if err != nil {
		return err
	}
	if !allowed {
		return &connector.PlatformError{Kind: connector.ErrRateLimited, RetryAfterSeconds: int(math.Ceil(retry.Seconds()))}
	}
	credential, err := o.credentials.UseCredential(ctx, work.Access)
	if err != nil {
		var app *errs.AppError
		if errors.As(err, &app) && (app.Code == 30101 || app.Code == errs.ErrNotFound || app.Code == 30302 || app.Code == 30303) {
			return &connector.PlatformError{Kind: connector.ErrAuthExpired}
		}
		return err
	}
	defer func() {
		for _, value := range credential.Values {
			clear(value)
		}
	}()
	callCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	err = fn(callCtx, connector.Credential{Kind: credential.Kind, Values: credential.Values})
	var failure *connector.PlatformError
	if errors.As(err, &failure) && failure.Kind == connector.ErrRateLimited {
		if deferErr := o.shared.DeferBucket(ctx, key, time.Duration(max(1, failure.RetryAfterSeconds))*time.Second); deferErr != nil {
			return deferErr
		}
	}
	return err
}

func (o *orderAcquisition) execute(ctx context.Context, work repo.Work) error {
	platform, ok := o.platforms.Get(work.Job.PlatformCode)
	if !ok {
		return errors.New("order platform is unavailable")
	}
	adapter, ok := platform.(connector.OrderPlatform)
	if !ok {
		return errors.New("order platform requires stable list and detail capabilities")
	}
	caps := adapter.OrderCapabilities()
	if !caps.StableIdentity || (!caps.PIIFree && o.store.rawProtector == nil) {
		return errors.New("order acquisition capability or protected raw gate is not satisfied")
	}
	window, err := o.store.beginOrderWindow(ctx, work, caps)
	if err != nil {
		return err
	}
	start := time.Now()
	for pages := 0; !window.Listed && pages < work.Job.MaxPages; pages++ {
		var result connector.OrderListResult
		err = o.call(ctx, work, func(ctx context.Context, credential connector.Credential) error {
			var err error
			result, err = adapter.ListOrders(ctx, credential, connector.OrderListRequest{From: window.From, Until: window.Until, Cursor: window.Token, PageSize: work.Job.PageSize})
			return err
		})
		if err != nil {
			return o.listFailure(ctx, work, window, err)
		}
		if len(result.Orders) > work.Job.PageSize || (result.HasMore && (result.NextCursor == "" || result.NextCursor == window.Token)) {
			return errors.New("invalid order pagination evidence")
		}
		next := window
		next.Token, next.Listed = result.NextCursor, !result.HasMore
		if err := o.store.discoverOrders(ctx, work, next, result); err != nil {
			return err
		}
		window = next
		if time.Since(start) > 25*time.Second {
			break
		}
	}
	if time.Since(start) < 35*time.Second {
		blocked, err := o.details(ctx, work, adapter, start)
		if err != nil || blocked {
			return err
		}
	}
	return o.store.finishOrderSlice(ctx, work, window)
}

func (o *orderAcquisition) listFailure(ctx context.Context, work repo.Work, window orderWindow, err error) error {
	var failure *connector.PlatformError
	if errors.As(err, &failure) && failure.Kind == connector.ErrAuthExpired {
		return o.store.DeferRun(ctx, work.Run, "credential_expired", 0, true)
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return o.store.finishOrderSlice(ctx, work, window)
}
