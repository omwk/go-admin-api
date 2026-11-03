package sync

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/omwk/go-admin-api/internal/kernel/errs"
	"github.com/omwk/go-admin-api/internal/kernel/httpx"
	"github.com/omwk/go-admin-api/internal/kernel/idem"
	"github.com/omwk/go-admin-api/internal/modules/identity"
	"github.com/omwk/go-admin-api/internal/modules/identity/permission"
	"github.com/omwk/go-admin-api/internal/modules/sync/repo"
)

// Route is one permission-protected management operation.
type Route struct{ Method, Path, Permission, Kind string }

func Routes() []Route {
	return []Route{
		{"POST", "/sync-jobs/:id/run", permission.SyncJobRun, ""},
		{"GET", "/sync-runs", permission.SyncRunList, ""},
		{"GET", "/sync-runs/:id", permission.SyncRunRead, ""},
		{"GET", "/dead-letters", permission.SyncDLQList, "dead_letter"},
		{"POST", "/dead-letters/:id/replay", permission.SyncDLQReplay, "dead_letter"},
		{"GET", "/quarantines", permission.SyncQuarantineList, "quarantine_record"},
		{"POST", "/quarantines/:id/replay", permission.SyncQuarantineReplay, "quarantine_record"},
	}
}

// RegisterRoutes installs exactly the T3.7/T3.9 execution and disposition APIs.
func RegisterRoutes(router *gin.Engine, auth *identity.AuthService, profiles *identity.ProfileService, service *Management) error {
	if router == nil || auth == nil || profiles == nil || service == nil {
		return errors.New("sync route dependencies required")
	}
	group := router.Group("/api/v1/admin", identity.JWTAuth(auth))
	for _, route := range Routes() {
		guard, err := identity.RequirePerm(profiles, route.Permission)
		if err != nil {
			return err
		}
		group.Handle(route.Method, route.Path, guard, service.handle(route))
	}
	return nil
}

func (s *Management) handle(route Route) gin.HandlerFunc {
	return func(c *gin.Context) {
		a := identity.AdminActor(c)
		ctx, err := s.store.Authorize(c.Request.Context(), a.User.TenantID, a.User.ID, route.Permission)
		if err != nil {
			httpx.Fail(c, err)
			return
		}
		c.Request = c.Request.WithContext(ctx)
		if route.Method == "POST" {
			s.write(c, route.Kind)
			return
		}
		var result any
		if c.Param("id") != "" {
			id, parseErr := syncID(c)
			err = parseErr
			if err == nil {
				result, err = s.store.ReadRun(ctx, id)
			}
		} else {
			page, parseErr := syncPage(c)
			err = parseErr
			if err == nil {
				if route.Kind == "" {
					result, err = s.store.ListRuns(ctx, page)
				} else {
					result, err = s.store.ListRecords(ctx, route.Kind, page)
				}
			}
		}
		if err != nil {
			httpx.Fail(c, err)
			return
		}
		httpx.OK(c, result)
	}
}

func (s *Management) write(c *gin.Context, kind string) {
	id, err := syncID(c)
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	input, hasKey, err := httpx.ExtractIdempotency(c)
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	if err := emptyBody(c); err != nil {
		httpx.Fail(c, err)
		return
	}
	var op *idem.Operation
	if hasKey {
		a := identity.AdminActor(c)
		prepared, err := idem.Prepare(a.User.TenantID, a.User.ID, input)
		if err != nil {
			httpx.Fail(c, invalid())
			return
		}
		op = &prepared
	}
	run, err := s.start(c.Request.Context(), id, kind, op)
	switch {
	case errors.Is(err, idem.ErrMismatch):
		err = errs.New(errs.ErrIdempotencyMismatch, 409, "幂等请求不一致")
	case errors.Is(err, idem.ErrProcessing):
		err = errs.New(errs.ErrConflict, 409, "请求处理中")
	}
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	httpx.Created(c, run)
}

func emptyBody(c *gin.Context) error {
	raw, err := io.ReadAll(io.LimitReader(c.Request.Body, 1025))
	if err != nil || len(raw) > 1024 {
		return invalid()
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || fields == nil || len(fields) > 0 {
		return invalid()
	}
	return nil
}

func invalid() error { return errs.New(errs.ErrInvalidParam, 400, "参数校验失败") }
func syncID(c *gin.Context) (int64, error) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		return 0, invalid()
	}
	return id, nil
}
func syncPage(c *gin.Context) (repo.PageQuery, error) {
	p := repo.PageQuery{Number: 1, Size: 20, Status: c.Query("status")}
	for key, target := range map[string]*int{"page": &p.Number, "page_size": &p.Size} {
		if raw, ok := c.GetQuery(key); ok {
			v, err := strconv.Atoi(raw)
			if err != nil || v <= 0 {
				return p, invalid()
			}
			*target = v
		}
	}
	p.Size = min(p.Size, 100)
	if p.Number > 10000 || (p.Number-1)*p.Size >= 10000 {
		return p, invalid()
	}
	for key, target := range map[string]*int64{"shop_id": &p.ShopID, "job_id": &p.JobID} {
		if raw, ok := c.GetQuery(key); ok {
			v, err := strconv.ParseInt(raw, 10, 64)
			if err != nil || v <= 0 {
				return p, invalid()
			}
			*target = v
		}
	}
	if sort := c.Query("sort"); sort != "" && sort != "-started_at" && sort != "-created_at" {
		return p, invalid()
	}
	return p, nil
}
