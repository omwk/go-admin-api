package sync

import (
	"encoding/json"
	"errors"
	"io"

	"github.com/gin-gonic/gin"
	"github.com/omwk/go-admin-api/internal/kernel/errs"
	"github.com/omwk/go-admin-api/internal/kernel/httpx"
	"github.com/omwk/go-admin-api/internal/kernel/idem"
	"github.com/omwk/go-admin-api/internal/modules/identity"
	"github.com/omwk/go-admin-api/internal/modules/identity/permission"
)

// ReplayRoutes declares the historical replay API and its permission contract.
func ReplayRoutes() []Route {
	return []Route{{Method: "POST", Path: "/replays", Permission: permission.SyncReplayRun}, {Method: "GET", Path: "/replays/:id", Permission: permission.SyncReplayRun}}
}

// RegisterReplayRoutes installs the historical replay creation and progress API.
func RegisterReplayRoutes(router *gin.Engine, auth *identity.AuthService, profiles *identity.ProfileService, store ReplayStore) error {
	if router == nil || auth == nil || profiles == nil || store == nil {
		return errors.New("replay route dependencies required")
	}
	guard, err := identity.RequirePerm(profiles, permission.SyncReplayRun)
	if err != nil {
		return err
	}
	group := router.Group("/api/v1/admin", identity.JWTAuth(auth), guard)
	for _, route := range ReplayRoutes() {
		group.Handle(route.Method, route.Path, replayHTTP(store, route.Method == "POST"))
	}
	return nil
}

func replayHTTP(store ReplayStore, create bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		actor := identity.AdminActor(c)
		ctx, err := store.Authorize(c.Request.Context(), actor.User.TenantID, actor.User.ID, permission.SyncReplayRun)
		if err != nil {
			httpx.Fail(c, err)
			return
		}
		var run ReplayRun
		if create {
			request, op, parseErr := replayBody(c)
			err = parseErr
			if err == nil {
				run, err = store.CreateReplay(ctx, request, op)
			}
		} else {
			id, parseErr := syncID(c)
			err = parseErr
			if err == nil {
				run, err = store.ReadReplay(ctx, id)
			}
		}
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
		if create {
			httpx.Created(c, run)
		} else {
			httpx.OK(c, run)
		}
	}
}

func replayBody(c *gin.Context) (ReplayRequest, *idem.Operation, error) {
	var request ReplayRequest
	input, hasKey, err := httpx.ExtractIdempotency(c)
	if err != nil {
		return request, nil, err
	}
	decoder := json.NewDecoder(io.LimitReader(c.Request.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&request) != nil {
		return request, nil, invalid()
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return request, nil, invalid()
	}
	if !hasKey {
		return request, nil, nil
	}
	actor := identity.AdminActor(c)
	op, err := idem.Prepare(actor.User.TenantID, actor.User.ID, input)
	return request, &op, err
}
