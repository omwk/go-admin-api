package bootstrap

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"time"

	"github.com/omwk/go-admin-api/internal/kernel/config"
	"github.com/omwk/go-admin-api/internal/kernel/crypto"
	"github.com/omwk/go-admin-api/internal/kernel/db"
	"github.com/omwk/go-admin-api/internal/kernel/errs"
	"github.com/omwk/go-admin-api/internal/kernel/scope"
	"github.com/omwk/go-admin-api/internal/modules/identity"
	identityscope "github.com/omwk/go-admin-api/internal/modules/identity/scope"
	"github.com/omwk/go-admin-api/internal/modules/ingestion"
	shopmodel "github.com/omwk/go-admin-api/internal/modules/shop/model"
	syncmodule "github.com/omwk/go-admin-api/internal/modules/sync"
	"github.com/omwk/go-admin-api/internal/modules/sync/repo"
	"gorm.io/gorm"
)

type syncEngineStore struct {
	database     *db.DB
	compiled     *ingestion.CompiledCache
	handlers     map[string]syncmodule.ResourceHandler
	now          func() time.Time
	settings     config.Sync
	rawSealer    crypto.Sealer
	rawProtector *ingestion.RawProtector
}

const syncRunColumns = "id,tenant_id,job_id,shop_id,resource_type,trigger,status,stats,triggered_by,started_at,finished_at,error_code,error_msg,trace_id,scheduling_generation"

var errRunAuthorizationRevoked = errors.New("sync initiator authorization revoked")

func newSyncEngineStore(database *db.DB, handlers map[string]syncmodule.ResourceHandler, settings config.Sync) *syncEngineStore {
	return &syncEngineStore{database: database, compiled: ingestion.NewCompiledCache(100), handlers: handlers, now: time.Now, settings: settings}
}

func syncDenied() error  { return errs.New(errs.ErrDataScopeDenied, 403, "数据权限不足") }
func syncBusy() error    { return errs.New(60201, 409, "同步任务已在运行") }
func syncMissing() error { return errs.New(60101, 404, "同步任务不存在或不可用") }

func engineQuery(name string) error { _, err := scopeRegistryQuery(name); return err }

// ResolveJob is a dedicated bootstrap projection, not an arbitrary SQL entry.
func (s *syncEngineStore) ResolveJob(ctx context.Context, id int64) (repo.Job, error) {
	if err := engineQuery("SyncEngine.ResolveJob"); err != nil {
		return repo.Job{}, err
	}
	if id <= 0 {
		return repo.Job{}, syncMissing()
	}
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	var row struct {
		repo.Job     `gorm:"embedded"`
		Options      json.RawMessage
		Capabilities json.RawMessage
	}
	err := q.Table("sync_job j").Select("j.id, j.tenant_id, j.shop_id, s.platform_id, j.resource_type AS resource, p.code AS platform_code, j.options, p.capabilities").
		Joins("JOIN shop s ON s.id=j.shop_id AND s.tenant_id=j.tenant_id AND s.deleted_at IS NULL AND s.status='active'").
		Joins("JOIN platform p ON p.id=s.platform_id AND p.status='active'").Where("j.id=? AND j.enabled=true", id).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return repo.Job{}, syncMissing()
	}
	if err != nil {
		return repo.Job{}, err
	}
	handler, registered := s.handlers[row.Resource]
	row.MappingVersion, row.Generation = taskOptions(row.Options)
	var options struct {
		MaxPages             int `json:"max_pages_per_run"`
		MaxPagesAlt          int `json:"max_pages"`
		PageSize             int `json:"page_size"`
		OrderLookbackSeconds int `json:"order_lookback_seconds"`
	}
	if err := json.Unmarshal(row.Options, &options); err != nil {
		return repo.Job{}, syncMissing()
	}
	if !registered || handler == nil || row.MappingVersion <= 0 || row.Generation <= 0 || !capabilityHasResource(row.Capabilities, row.Resource) {
		return repo.Job{}, syncMissing()
	}
	row.MaxPages = options.MaxPages
	if row.MaxPages <= 0 {
		row.MaxPages = options.MaxPagesAlt
	}
	if row.MaxPages <= 0 {
		row.MaxPages = 10
	}
	row.MaxPages = min(row.MaxPages, 100)
	row.PageSize = options.PageSize
	if row.PageSize <= 0 {
		row.PageSize = 100
	}
	row.PageSize = min(row.PageSize, 100)
	row.OrderLookbackSeconds = options.OrderLookbackSeconds
	if row.OrderLookbackSeconds <= 0 {
		row.OrderLookbackSeconds = 900
	}
	return row.Job, nil
}

func (s *syncEngineStore) Authorize(ctx context.Context, tenant, user int64, code string) (context.Context, error) {
	if err := engineQuery("SyncAdmin.Authorize"); err != nil {
		return ctx, err
	}
	profile, err := identity.NewProfileService(newProfileStore(s.database))
	if err != nil {
		return ctx, err
	}
	actor, err := profile.Me(ctx, tenant, user)
	if err != nil {
		return ctx, err
	}
	if !actor.User.IsSuper && !slices.Contains(actor.Permissions, code) {
		return ctx, errs.New(errs.ErrPermissionDenied, 403, "权限不足")
	}
	return newAdminStore(s.database).Authorize(ctx, tenant, user)
}

func (s *syncEngineStore) checkHuman(ctx context.Context, job repo.Job, code string) error {
	access, err := identityscope.From(ctx)
	if err != nil || access.Tenant != job.TenantID {
		return syncDenied()
	}
	ctx, err = s.Authorize(ctx, access.Tenant, access.User, code)
	if err != nil {
		return err
	}
	_, err = newShopStore(s.database).Shop(ctx, job.TenantID, job.ShopID)
	return err
}

func (s *syncEngineStore) LoadWork(ctx context.Context, jobID, runID int64) (repo.Work, error) {
	if err := engineQuery("SyncEngine.LoadWork"); err != nil {
		return repo.Work{}, err
	}
	job, err := s.ResolveJob(ctx, jobID)
	if err != nil {
		return repo.Work{}, err
	}
	if _, err := (&syncStore{database: s.database}).ResolveTask(ctx, job.TenantID, jobID, runID); err != nil {
		return repo.Work{}, err
	}
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	var run repo.Run
	if err := q.Table("sync_run").Select(syncRunColumns).Where("id=? AND tenant_id=? AND job_id=? AND shop_id=? AND resource_type=?", runID, job.TenantID, jobID, job.ShopID, job.Resource).Take(&run).Error; err != nil {
		return repo.Work{}, err
	}
	if run.Stats.Generation != job.Generation || run.Stats.MappingVersion <= 0 {
		return repo.Work{}, syncDenied()
	}
	if run.TriggeredBy != nil {
		code := runPermission(run.Stats.ReplayKind)
		human, authErr := s.Authorize(ctx, job.TenantID, *run.TriggeredBy, code)
		if authErr == nil {
			authErr = s.checkHuman(human, job, code)
		}
		if authErr != nil {
			authErr = errors.Join(errRunAuthorizationRevoked, authErr)
			if db.InTransaction(ctx) {
				return repo.Work{}, authErr
			}
			if err := s.cancelUnauthorized(ctx, run); err != nil {
				return repo.Work{}, errors.Join(authErr, err)
			}
			return repo.Work{}, authErr
		}
	}
	job.MappingVersion = run.Stats.MappingVersion
	access, err := scope.IssueWorkAccess(scope.BootstrapIssuer{}, job.TenantID, job.ID, run.ID, job.ShopID, job.Resource, job.MappingVersion, job.Generation,
		[]string{"sync.cursor", "sync.page", "sync.defer", "sync.replay", "raw.save", "credential.use"})
	if err != nil {
		return repo.Work{}, err
	}
	var mapping struct {
		Spec     json.RawMessage
		Checksum string
	}
	err = q.Table("mapping_version v").Select("v.spec, v.checksum").Joins("JOIN mapping_profile p ON p.id=v.profile_id AND p.tenant_id=v.tenant_id").
		Where("v.id=? AND v.tenant_id=? AND v.status IN ? AND p.platform_id=? AND p.resource_type=?", job.MappingVersion, job.TenantID, []string{"published", "archived"}, job.PlatformID, job.Resource).Take(&mapping).Error
	if err != nil {
		return repo.Work{}, err
	}
	program, err := s.compiled.GetOrCompile(job.MappingVersion, mapping.Checksum, mapping.Spec)
	return repo.Work{Job: job, Run: run, Access: access, Mapping: program}, err
}

func runPermission(kind string) string {
	switch kind {
	case "dead_letter":
		return "sync:dlq:replay"
	case "quarantine_record":
		return "sync:quarantine:replay"
	default:
		return "sync:job:run"
	}
}

func (s *syncEngineStore) cancelUnauthorized(ctx context.Context, run repo.Run) error {
	run.Stats.Pending = false
	raw, err := json.Marshal(run.Stats)
	if err != nil {
		return err
	}
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	return q.Table("sync_run").Where("id=? AND tenant_id=? AND job_id=?", run.ID, run.TenantID, run.JobID).
		Updates(map[string]any{"status": "cancelled", "stats": json.RawMessage(raw), "error_code": "authorization_revoked", "error_msg": "initiator authorization revoked", "finished_at": time.Now().UTC()}).Error
}

func (s *syncEngineStore) capability(ctx context.Context, method string) (scope.WorkAccess, error) {
	a, err := scope.WorkFrom(ctx)
	if err != nil || !a.Allows(method) {
		return scope.WorkAccess{}, syncDenied()
	}
	work, err := s.LoadWork(ctx, a.JobID(), a.RunID())
	if err != nil {
		return scope.WorkAccess{}, err
	}
	if work.Access.TenantID() != a.TenantID() || work.Access.ShopID() != a.ShopID() || work.Access.Resource() != a.Resource() || work.Access.MappingVersion() != a.MappingVersion() || work.Access.Generation() != a.Generation() {
		return scope.WorkAccess{}, syncDenied()
	}
	return a, nil
}

// humanQuery scopes the shop root before every joined SELECT, count or subquery.
func (s *syncEngineStore) humanQuery(ctx context.Context, table, method string) (*gorm.DB, context.CancelFunc, error) {
	if err := engineQuery(method); err != nil {
		return nil, func() {}, err
	}
	q, cancel := s.database.WithContext(ctx)
	q, err := shopScope(ctx, shopmodel.Shop{}, q.Table("shop"))
	if err != nil {
		cancel()
		return nil, func() {}, err
	}
	return q.Joins("JOIN " + table + " x ON x.tenant_id=shop.tenant_id AND x.shop_id=shop.id"), cancel, nil
}
