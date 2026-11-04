package bootstrap

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/omwk/go-admin-api/internal/kernel/cache"
	"github.com/omwk/go-admin-api/internal/kernel/config"
	"github.com/omwk/go-admin-api/internal/kernel/db"
	"github.com/omwk/go-admin-api/internal/kernel/errs"
	"github.com/omwk/go-admin-api/internal/modules/connector"
	"github.com/omwk/go-admin-api/internal/modules/connector/platforms/mock"
	"github.com/omwk/go-admin-api/internal/modules/connector/platforms/shopee"
	"github.com/omwk/go-admin-api/internal/modules/identity"
	"github.com/omwk/go-admin-api/internal/modules/ingestion"
	"github.com/omwk/go-admin-api/internal/modules/shop"
	syncmodule "github.com/omwk/go-admin-api/internal/modules/sync"
	"github.com/omwk/go-admin-api/internal/modules/sync/repo"
)

func registerSyncRoutes(router *gin.Engine, auth *identity.AuthService, profiles *identity.ProfileService, database *db.DB, shared *cache.Cache, cfg *config.Config) error {
	engine, store, err := newSyncRuntime(database, shared, cfg)
	if err != nil {
		return err
	}
	management, err := syncmodule.NewManagement(store, engine)
	if err != nil {
		return err
	}
	if err := syncmodule.RegisterRoutes(router, auth, profiles, management); err != nil {
		return err
	}
	return syncmodule.RegisterReplayRoutes(router, auth, profiles, store)
}

func newSyncRuntime(database *db.DB, shared *cache.Cache, cfg *config.Config) (*syncmodule.Orchestrator, *syncEngineStore, error) {
	sealer, err := credentialSealer(cfg)
	if err != nil {
		return nil, nil, err
	}
	credentials, err := shop.NewService(newShopStore(database), newAdminStore(database).Authorize, sealer)
	if err != nil {
		return nil, nil, err
	}
	platforms := connector.NewRegistry()
	if err := platforms.Register(mock.New("")); err != nil {
		return nil, nil, err
	}
	if cfg.Shopee.BaseURL != "" {
		adapter, err := shopee.New(cfg.Shopee.BaseURL, nil)
		if err != nil {
			return nil, nil, err
		}
		if err := platforms.Register(adapter); err != nil {
			return nil, nil, err
		}
	}
	return buildSyncRuntime(database, shared, credentials, platforms)
}

func buildSyncRuntime(database *db.DB, shared *cache.Cache, credentials shop.CredentialUsePort, platforms *connector.Registry) (*syncmodule.Orchestrator, *syncEngineStore, error) {
	handlers := registeredResourceHandlers()
	store := newSyncEngineStore(database, handlers)
	raw, err := NewRawService(database, func(ctx context.Context, shopID int64) (ingestion.RawIdentity, error) {
		a, err := store.capability(ctx, "raw.save")
		if err != nil {
			return ingestion.RawIdentity{}, err
		}
		if a.ShopID() != shopID {
			return ingestion.RawIdentity{}, syncDenied()
		}
		job, err := store.ResolveJob(ctx, a.JobID())
		return ingestion.RawIdentity{TenantID: a.TenantID(), PlatformID: job.PlatformID}, err
	})
	if err != nil {
		return nil, nil, err
	}
	engine, err := syncmodule.NewOrchestrator(syncmodule.Dependencies{Store: store, Replay: store, Raw: raw, Handlers: handlers,
		Guard: syncGuard(shared), Fetch: syncFetch(store, shared, credentials, platforms)})
	return engine, store, err
}

func syncGuard(shared *cache.Cache) func(context.Context, repo.Job, func(context.Context) error) error {
	return func(ctx context.Context, job repo.Job, fn func(context.Context) error) (err error) {
		lease, err := shared.Lock(ctx, fmt.Sprintf("lock:sync:%d:%d:%s", job.TenantID, job.ShopID, job.Resource), 30*time.Second)
		if errors.Is(err, cache.ErrLocked) {
			return syncBusy()
		}
		if err != nil {
			return err
		}
		work, cancel := context.WithCancel(ctx)
		done := make(chan error, 1)
		go func() {
			ticker := time.NewTicker(10 * time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-work.Done():
					done <- nil
					return
				case <-ticker.C:
					ok, err := lease.Renew(work, 30*time.Second)
					if err != nil || !ok {
						cancel()
						done <- errors.New("sync resource lease lost")
						return
					}
				}
			}
		}()
		err = fn(work)
		cancel()
		err = errors.Join(err, <-done)
		release, cancelRelease := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
		defer cancelRelease()
		_, releaseErr := lease.Release(release)
		return errors.Join(err, releaseErr)
	}
}

func syncFetch(store *syncEngineStore, shared *cache.Cache, credentials shop.CredentialUsePort, platforms *connector.Registry) func(context.Context, repo.Work, string) (connector.FetchResult, error) {
	return func(ctx context.Context, work repo.Work, cursor string) (connector.FetchResult, error) {
		if _, err := store.capability(ctx, "credential.use"); err != nil {
			return connector.FetchResult{}, err
		}
		platform, ok := platforms.Get(work.Job.PlatformCode)
		if !ok {
			return connector.FetchResult{}, &connector.PlatformError{Kind: connector.ErrInvalidRequest}
		}
		key := fmt.Sprintf("sync:bucket:%d:%d", work.Job.TenantID, work.Job.ShopID)
		allowed, _, retry, err := shared.TokenBucket(ctx, key, 5, 5)
		if err != nil {
			return connector.FetchResult{}, err
		}
		if !allowed {
			return connector.FetchResult{}, &connector.PlatformError{Kind: connector.ErrRateLimited, RetryAfterSeconds: int(math.Ceil(retry.Seconds()))}
		}
		credential, err := credentials.UseCredential(ctx, work.Access)
		if err != nil {
			var app *errs.AppError
			if errors.As(err, &app) && (app.Code == 30101 || app.Code == errs.ErrNotFound || app.Code == 30302 || app.Code == 30303) {
				return connector.FetchResult{}, &connector.PlatformError{Kind: connector.ErrAuthExpired}
			}
			return connector.FetchResult{}, err
		}
		defer func() {
			for _, value := range credential.Values {
				clear(value)
			}
		}()
		ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		result, err := platform.Fetch(ctx, connector.Credential{Kind: credential.Kind, Values: credential.Values}, connector.FetchRequest{Resource: connector.ResourceType(work.Job.Resource), Cursor: cursor, PageSize: work.Job.PageSize})
		var failure *connector.PlatformError
		if errors.As(err, &failure) && failure.Kind == connector.ErrRateLimited {
			if deferErr := shared.DeferBucket(ctx, key, time.Duration(max(1, failure.RetryAfterSeconds))*time.Second); deferErr != nil {
				return connector.FetchResult{}, deferErr
			}
		}
		return result, err
	}
}
