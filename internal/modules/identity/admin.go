package identity

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"

	"github.com/omwk/go-admin-api/internal/kernel/audit"
	"github.com/omwk/go-admin-api/internal/kernel/errs"
	"github.com/omwk/go-admin-api/internal/kernel/setting"
	"github.com/omwk/go-admin-api/internal/modules/identity/repo"
)

// AdminService owns management transactions, validation and audit records.
type AdminService struct {
	store      repo.Admin
	audit      func(context.Context, audit.Event) error
	invalidate func(context.Context, int64, []int64) bool
	sessions   repo.Sessions
	auth       *AuthService
	accounts   *AccountService
	settings   setting.Reader
}

// UseInvalidator configures bounded, post-commit invalidation during dependency assembly.
func (s *AdminService) UseInvalidator(invalidate func(context.Context, int64, []int64) bool) {
	s.invalidate = invalidate
}

// NewAdminService constructs the management service without infrastructure handles.
func NewAdminService(store repo.Admin, record func(context.Context, audit.Event) error) (*AdminService, error) {
	if store == nil || record == nil {
		return nil, errors.New("management store and audit recorder are required")
	}
	return &AdminService{store: store, audit: record}, nil
}

// Execute serializes a management write with seed/tree changes and persists optional idempotency.
// The callback and response record commit atomically; a failed audit rolls back the write.
func (s *AdminService) Execute(ctx context.Context, actor Overview, scope, key, hash string, run func(context.Context) (repo.SavedResponse, error)) (repo.SavedResponse, error) {
	var response repo.SavedResponse
	if actor.User.ID <= 0 || actor.User.TenantID <= 0 {
		return response, revokedToken()
	}
	ctx, err := s.store.Authorize(ctx, actor.User.TenantID, actor.User.ID)
	if err != nil {
		return response, err
	}
	var affected []int64
	err = s.store.Transaction(ctx, func(ctx context.Context) error {
		var err error
		response, affected, err = s.executeLocked(ctx, actor, scope, key, hash, run)
		if err == nil && len(affected) > 0 {
			if emitter, ok := s.store.(repo.AuthorizationInvalidationEmitter); ok {
				err = emitter.EmitAuthorizationInvalidation(ctx, actor.User.TenantID, affected)
			}
		}
		return err
	})
	if err == nil && s.invalidate != nil {
		response.InvalidationPending = s.invalidate(ctx, actor.User.TenantID, affected)
	}
	return response, err
}

func (s *AdminService) executeLocked(ctx context.Context, actor Overview, operation, key, hash string, run func(context.Context) (repo.SavedResponse, error)) (repo.SavedResponse, []int64, error) {
	var response repo.SavedResponse
	if err := s.store.LockManagement(ctx, actor.User.TenantID); err != nil {
		return response, nil, err
	}
	ctx, err := s.store.Authorize(ctx, actor.User.TenantID, actor.User.ID)
	if err != nil {
		return response, nil, err
	}
	affected, err := s.store.AffectedUsers(ctx, actor.User.TenantID, authzChange(operation))
	if err != nil {
		return response, nil, err
	}
	if key != "" {
		cached, found, err := s.store.Replay(ctx, actor.User.TenantID, operation, key, hash)
		if err != nil {
			return response, nil, err
		}
		if found {
			return cached, affected, nil
		}
	}
	response, err = run(ctx)
	if err != nil {
		return response, nil, err
	}
	if key != "" {
		err = s.store.SaveResponse(ctx, actor.User.TenantID, operation, key, hash, response)
	}
	return response, affected, err
}

func authzChange(operation string) repo.AuthzChange {
	parts := strings.Split(operation, "/api/v1/admin/")
	if len(parts) != 2 {
		return repo.AuthzChange{}
	}
	path := strings.Split(parts[1], "/")
	change := repo.AuthzChange{Resource: path[0]}
	if len(path) > 1 {
		id, err := strconv.ParseInt(path[1], 10, 64)
		if err == nil {
			change.ID = id
		}
	}
	return change
}

func (s *AdminService) record(ctx context.Context, actor Overview, action, resource string, id int64, before, after map[string]any, removed bool) error {
	return s.audit(ctx, audit.Event{TenantID: actor.User.TenantID, ActorType: "user", ActorID: &actor.User.ID,
		Action: action, ResourceType: resource, ResourceID: strconv.FormatInt(id, 10), Before: before, After: after,
		AfterNull: removed, Result: "success"})
}

func snapshot(row any) (map[string]any, error) {
	encoded, err := json.Marshal(row)
	if err != nil {
		return nil, err
	}
	var result map[string]any
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.UseNumber()
	err = decoder.Decode(&result)
	return result, err
}

func denied() error        { return errs.New(20107, 403, "无权限") }
func invalidAdmin() error  { return errs.New(errs.ErrInvalidParam, 400, "参数校验失败") }
func conflictAdmin() error { return errs.New(errs.ErrConflict, 409, "资源仍被引用或受保护") }

func validIDs(ids []int64) bool {
	seen := map[int64]bool{}
	if len(ids) > 500 {
		return false
	}
	for _, id := range ids {
		if id <= 0 || seen[id] {
			return false
		}
		seen[id] = true
	}
	return true
}
