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
	return []Route{
		{Method: "POST", Path: "/replays", Permission: permission.SyncReplayCreate, Kind: "create"},
		{Method: "GET", Path: "/replays", Permission: permission.SyncReplayList, Kind: "list"},
		{Method: "GET", Path: "/replays/:id", Permission: permission.SyncReplayRead, Kind: "read"},
		{Method: "POST", Path: "/replays/:id/cancel", Permission: permission.SyncReplayCancel, Kind: "cancel"},
	}
}

// RegisterReplayRoutes installs the historical replay creation and progress API.
func RegisterReplayRoutes(router *gin.Engine, auth *identity.AuthService, profiles *identity.ProfileService, store ReplayStore) error {
	if router == nil || auth == nil || profiles == nil || store == nil {
		return errors.New("replay route dependencies required")
	}
	group := router.Group("/api/v1/admin", identity.JWTAuth(auth))
	for _, route := range ReplayRoutes() {
		guard, err := identity.RequirePerm(profiles, route.Permission)
		if err != nil {
			return err
		}
		group.Handle(route.Method, route.Path, guard, replayHTTP(store, route))
	}
	return nil
}

func replayHTTP(store ReplayStore, route Route) gin.HandlerFunc {
	return func(c *gin.Context) {
		actor := identity.AdminActor(c)
		ctx, err := store.Authorize(c.Request.Context(), actor.User.TenantID, actor.User.ID, route.Permission)
		if err != nil {
			httpx.Fail(c, err)
			return
		}
		var result any
		if route.Kind == "create" {
			request, op, parseErr := replayBody(c)
			err = parseErr
			if err == nil {
				result, err = store.CreateReplay(ctx, request, op)
			}
		} else if route.Kind == "list" {
			page, parseErr := syncPage(c)
			err = parseErr
			if err == nil {
				result, err = store.ListReplays(ctx, page)
			}
		} else {
			id, parseErr := syncID(c)
			err = parseErr
			if err == nil {
				if route.Kind == "cancel" {
					var op *idem.Operation
					op, err = replayCancelOperation(c)
					if err == nil {
						result, err = store.CancelReplay(ctx, id, op)
					}
				} else {
					result, err = store.ReadReplay(ctx, id)
				}
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
		if route.Kind == "create" {
			httpx.Created(c, result)
		} else {
			httpx.OK(c, result)
		}
	}
}

func replayCancelOperation(c *gin.Context) (*idem.Operation, error) {
	input, hasKey, err := httpx.ExtractIdempotency(c)
	if err != nil {
		return nil, err
	}
	if err := emptyBody(c); err != nil {
		return nil, err
	}
	if !hasKey {
		return nil, nil
	}
	actor := identity.AdminActor(c)
	op, err := idem.Prepare(actor.User.TenantID, actor.User.ID, input)
	return &op, err
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
