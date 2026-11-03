package identity

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/omwk/go-admin-api/internal/kernel/audit"
	"github.com/omwk/go-admin-api/internal/kernel/errs"
	"github.com/omwk/go-admin-api/internal/modules/identity/permission"
	"github.com/omwk/go-admin-api/internal/modules/identity/repo"
)

// PermissionReleaseRequest is the immutable input to one authorization release.
type PermissionReleaseRequest struct {
	RoleCode string
	Codes    []string
	Release  string
}

// PermissionReleaseResult describes the committed grant delta.
type PermissionReleaseResult struct {
	Release      string
	Summary      string
	Added        int
	AffectedUser []int64
	Changed      bool
}

// PermissionReleaseService applies approved permission increments atomically.
type PermissionReleaseService struct {
	store repo.PermissionReleaseStore
	audit func(context.Context, audit.Event) error
}

// NewPermissionReleaseService constructs the release service.
func NewPermissionReleaseService(store repo.PermissionReleaseStore, record func(context.Context, audit.Event) error) (*PermissionReleaseService, error) {
	if store == nil || record == nil {
		return nil, errors.New("permission release dependencies are required")
	}
	return &PermissionReleaseService{store: store, audit: record}, nil
}

// Apply validates and commits a release. The caller invalidates AffectedUser after commit.
func (s *PermissionReleaseService) Apply(ctx context.Context, tenant int64, request PermissionReleaseRequest) (PermissionReleaseResult, error) {
	canonical, err := normalizeRelease(request)
	if err != nil {
		return PermissionReleaseResult{}, errs.Invalid(err)
	}
	summary := releaseSummary(canonical)
	result := PermissionReleaseResult{Release: request.Release, Summary: summary}
	err = s.store.Transaction(ctx, func(tx context.Context) error {
		if err := s.store.LockPermissionRelease(tx, tenant); err != nil {
			return err
		}
		if old, found, err := s.store.PermissionReleaseMarker(tx, tenant, request.Release); err != nil {
			return err
		} else if found {
			if old != summary {
				return errs.New(errs.ErrConflict, 409, "发布清单与既有发布不一致")
			}
			result.Changed = false
			return nil
		}
		role, err := s.store.RoleByCode(tx, tenant, request.RoleCode)
		if err != nil {
			return err
		}
		current, err := s.store.RolePermissionCodes(tx, tenant, role.ID)
		if err != nil {
			return err
		}
		seen := make(map[string]bool, len(current))
		for _, code := range current {
			seen[code] = true
		}
		addedCodes := make([]string, 0, len(canonical))
		for _, code := range canonical {
			if !seen[code] {
				addedCodes = append(addedCodes, code)
			}
		}
		ids, err := s.store.PermissionIDs(tx, tenant, addedCodes)
		if err != nil {
			return err
		}
		addedIDs := make([]int64, 0, len(addedCodes))
		for _, code := range addedCodes {
			id, ok := ids[code]
			if !ok {
				return errs.New(errs.ErrNotFound, 404, "权限不存在")
			}
			addedIDs = append(addedIDs, id)
		}
		if err := s.store.InsertRolePermissions(tx, tenant, role.ID, addedIDs); err != nil {
			return err
		}
		users, err := s.store.PermissionReleaseUsers(tx, tenant, role.ID)
		if err != nil {
			return err
		}
		result.AffectedUser = uniqueIDs(users)
		result.Added = len(addedIDs)
		result.Changed = true
		if err := s.store.SavePermissionReleaseMarker(tx, tenant, request.Release, summary); err != nil {
			return err
		}
		if err := s.audit(tx, audit.Event{TenantID: tenant, ActorType: "system", Action: "permissions.release", ResourceType: "permission_release", ResourceID: request.Release, After: map[string]any{"role_code": request.RoleCode, "summary": summary, "added": len(addedIDs)}, Result: "success"}); err != nil {
			return err
		}
		if emitter, ok := s.store.(repo.OutboxEmitter); ok && len(result.AffectedUser) > 0 {
			if err := emitter.EmitAuthorizationInvalidation(tx, tenant, result.AffectedUser); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return PermissionReleaseResult{}, err
	}
	return result, nil
}

func normalizeRelease(request PermissionReleaseRequest) ([]string, error) {
	if request.RoleCode == "" || request.Release == "" || len(request.Codes) == 0 {
		return nil, errors.New("role, codes and release are required")
	}
	seen := map[string]bool{}
	items := make([]string, 0, len(request.Codes))
	for _, raw := range request.Codes {
		code := strings.TrimSpace(raw)
		if code == "" || seen[code] {
			return nil, errors.New("permission codes must be non-empty and unique")
		}
		if err := permission.Registered(code); err != nil {
			return nil, fmt.Errorf("permission code is not registered")
		}
		seen[code] = true
		items = append(items, code)
	}
	sort.Strings(items)
	return items, nil
}

func releaseSummary(codes []string) string {
	digest := sha256.Sum256([]byte(strings.Join(codes, "\n")))
	return hex.EncodeToString(digest[:])
}

func uniqueIDs(ids []int64) []int64 {
	seen := map[int64]bool{}
	result := make([]int64, 0, len(ids))
	for _, id := range ids {
		if id > 0 && !seen[id] {
			seen[id] = true
			result = append(result, id)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })
	return result
}

// MarshalPermissionRelease is a stable marker representation for diagnostics and tests.
func MarshalPermissionRelease(request PermissionReleaseRequest) ([]byte, error) {
	codes, err := normalizeRelease(request)
	if err != nil {
		return nil, err
	}
	return json.Marshal(struct {
		Release string   `json:"release"`
		Codes   []string `json:"codes"`
		Summary string   `json:"summary"`
	}{request.Release, codes, releaseSummary(codes)})
}
