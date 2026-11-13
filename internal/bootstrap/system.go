package bootstrap

import (
	"context"

	"github.com/gin-gonic/gin"
	"github.com/omwk/go-admin-api/internal/kernel/audit"
	"github.com/omwk/go-admin-api/internal/kernel/db"
	"github.com/omwk/go-admin-api/internal/kernel/idem"
	"github.com/omwk/go-admin-api/internal/kernel/setting"
	"github.com/omwk/go-admin-api/internal/modules/identity"
	"github.com/omwk/go-admin-api/internal/modules/identity/repo"
	"github.com/omwk/go-admin-api/internal/modules/system"
)

func newSystemServices(database *db.DB) (*system.SettingService, *system.DictService, error) {
	record := func(ctx context.Context, event audit.Event) error { return audit.Record(ctx, database, event) }
	settings, err := system.NewSettingService(&systemSettingStore{database: database}, setting.DefaultRegistry(), record)
	if err != nil {
		return nil, nil, err
	}
	dicts, err := system.NewDictService(newSystemDictStore(database), record)
	return settings, dicts, err
}

func systemWriter(admin *identity.AdminService) system.WriteFunc {
	return func(ctx context.Context, actor system.Actor, operation, key, hash string, run func(context.Context) (idem.Result, error)) (idem.Result, error) {
		current := identity.Overview{User: identity.UserView{ID: actor.UserID, TenantID: actor.TenantID}}
		response, err := admin.Execute(ctx, current, operation, key, hash, func(tx context.Context) (repo.SavedResponse, error) {
			result, err := run(tx)
			return repo.SavedResponse{Status: result.Status, Data: result.Body}, err
		})
		return idem.Result{Status: response.Status, Body: response.Data}, err
	}
}

func registerSystemRoutes(router *gin.Engine, auth *identity.AuthService, profiles *identity.ProfileService, admin *identity.AdminService, database *db.DB) error {
	settings, dicts, err := newSystemServices(database)
	if err != nil {
		return err
	}
	auth.UseSettings(settings)
	admin.UseSettings(settings)
	return system.RegisterRoutes(router, auth, profiles, settings, dicts, systemWriter(admin))
}
