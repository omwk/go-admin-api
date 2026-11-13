package bootstrap

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/omwk/go-admin-api/internal/kernel/audit"
	"github.com/omwk/go-admin-api/internal/modules/identity"
	"github.com/omwk/go-admin-api/internal/modules/identity/model"
	"github.com/omwk/go-admin-api/internal/modules/identity/repo"
	"github.com/stretchr/testify/require"
)

func s4LockedUser(t *testing.T, f *systemFixture) {
	t.Helper()
	require.NoError(t, f.database.Exec(f.ctx, "UPDATE sys_user SET status='locked',failed_attempts=5,locked_until=? WHERE tenant_id=1 AND id=?",
		f.now.Add(time.Hour), f.user.ID))
}

func s4Protection(t *testing.T, f *systemFixture) model.User {
	t.Helper()
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	var row model.User
	require.NoError(t, q.Select("status,failed_attempts,locked_until").Where("tenant_id=1 AND id=?", f.user.ID).Take(&row).Error)
	return row
}

func s4UnlockAudits(t *testing.T, f *systemFixture) []struct{ Before, After json.RawMessage } {
	t.Helper()
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	var rows []struct{ Before, After json.RawMessage }
	require.NoError(t, q.Table("audit_log").Select(`"before","after"`).
		Where("tenant_id=1 AND action=? AND resource_id=?", "system:user:unlock", strconv.FormatInt(f.user.ID, 10)).Find(&rows).Error)
	return rows
}

func TestIdentityUnlockIntegrationLoginAndIdempotency(t *testing.T) {
	// S4 `5.9/10: real login protection is cleared; retries do not add audit.
	f := newSystemFixture(t, nil)
	s4LockedUser(t, f)
	_, err := f.identityFixture.service.Login(f.ctx, 1, f.user.Username, f.password, identity.RequestMeta{})
	require.Error(t, err)
	path := resourcePath("users", f.user.ID) + "/unlock"
	first, original := adminRequest(t, f.adminFixture, "POST", path, nil, "s4-unlock-once")
	require.Equal(t, 200, first.Code)
	var user identity.AdminUser
	require.NoError(t, json.Unmarshal(original.Data, &user))
	require.Equal(t, "active", user.Status)
	stored := s4Protection(t, f)
	require.Equal(t, "active", stored.Status)
	require.Zero(t, stored.FailedAttempts)
	require.Nil(t, stored.LockedUntil)
	for _, key := range []string{"s4-unlock-once", "", "s4-unlock-new-key"} {
		response, replay := adminRequest(t, f.adminFixture, "POST", path, nil, key)
		require.Equal(t, 200, response.Code)
		require.JSONEq(t, string(original.Data), string(replay.Data))
	}
	response, _ := adminRequest(t, f.adminFixture, "POST", path, map[string]any{}, "s4-unlock-once")
	require.Equal(t, 409, response.Code)
	rows := s4UnlockAudits(t, f)
	require.Len(t, rows, 1)
	require.JSONEq(t, `{"status":"locked","failed_attempts":5}`, string(rows[0].Before))
	require.JSONEq(t, `{"status":"active","failed_attempts":0}`, string(rows[0].After))
	_, err = f.identityFixture.service.Login(f.ctx, 1, f.user.Username, f.password, identity.RequestMeta{})
	require.NoError(t, err)
	t.Log("S4 #9/#10: locked -> active/0/NULL; login succeeds; keyed/unkeyed retries=200; exactly one state/count-only audit")
}

func TestIdentityUnlockIntegrationDisabledMissingAndRollback(t *testing.T) {
	// S4 `5.11: disabled is an administrative stop, not login protection.
	f := newSystemFixture(t, nil)
	s4LockedUser(t, f)
	require.NoError(t, f.database.Exec(f.ctx, "UPDATE sys_user SET status='disabled' WHERE tenant_id=1 AND id=?", f.user.ID))
	path := resourcePath("users", f.user.ID) + "/unlock"
	response, envelope := adminRequest(t, f.adminFixture, "POST", path, nil, "")
	require.Equal(t, 409, response.Code)
	require.Equal(t, 10004, envelope.Code)
	require.Equal(t, "disabled", s4Protection(t, f).Status)
	require.Equal(t, 5, s4Protection(t, f).FailedAttempts)
	require.Empty(t, s4UnlockAudits(t, f))
	for _, tc := range []struct {
		id     int64
		status int
	}{{0, 400}, {-1, 400}, {999999, 404}} {
		response, _ = adminRequest(t, f.adminFixture, "POST", resourcePath("users", tc.id)+"/unlock", nil, "")
		require.Equal(t, tc.status, response.Code)
	}
	s4LockedUser(t, f)
	failure := errors.New("injected unlock audit failure")
	service, err := identity.NewAdminService(f.store, func(context.Context, audit.Event) error { return failure })
	require.NoError(t, err)
	_, err = service.Execute(f.ctx, f.actor, "POST:/api/v1/admin"+path, "", "", func(ctx context.Context) (repo.SavedResponse, error) {
		_, err := service.UnlockUser(ctx, f.actor, f.user.ID)
		return repo.SavedResponse{}, err
	})
	require.ErrorIs(t, err, failure)
	stored := s4Protection(t, f)
	require.Equal(t, "locked", stored.Status)
	require.Equal(t, 5, stored.FailedAttempts)
	require.NotNil(t, stored.LockedUntil)
	require.Empty(t, s4UnlockAudits(t, f))
	t.Log("S4 #11: disabled=409/10004 without mutation; missing=404; audit failure rolls back all protection changes")
}

func TestIdentityUnlockIntegrationConcurrentAndTenantScope(t *testing.T) {
	f := newSystemFixture(t, nil)
	s4LockedUser(t, f)
	path := "/api/v1/admin" + resourcePath("users", f.user.ID) + "/unlock"
	statuses := make([]int, 2)
	var pending sync.WaitGroup
	for i := range statuses {
		pending.Add(1)
		go func(i int) {
			defer pending.Done()
			request := httptest.NewRequest("POST", path, nil).WithContext(f.ctx)
			request.Header.Set("Authorization", "Bearer "+f.bearer)
			response := httptest.NewRecorder()
			f.router.ServeHTTP(response, request)
			statuses[i] = response.Code
		}(i)
	}
	pending.Wait()
	require.Equal(t, []int{200, 200}, statuses)
	require.Len(t, s4UnlockAudits(t, f), 1)
	require.NoError(t, f.database.Exec(f.ctx, "INSERT INTO tenant(id,code,name) OVERRIDING SYSTEM VALUE VALUES(2,?,?)", "unlock-other", "Other tenant"))
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	dept := model.Department{TenantID: 2, Name: "root", Path: "1", Status: "active"}
	require.NoError(t, q.Create(&dept).Error)
	foreign := model.User{TenantID: 2, Username: "foreign-locked", PasswordHash: f.user.PasswordHash,
		DepartmentID: dept.ID, Status: "locked", FailedAttempts: 5}
	require.NoError(t, q.Create(&foreign).Error)
	response, _ := adminRequest(t, f.adminFixture, "POST", resourcePath("users", foreign.ID)+"/unlock", nil, "")
	require.Equal(t, 404, response.Code)
	require.NoError(t, f.database.Exec(f.ctx, "UPDATE sys_user SET deleted_at=now() WHERE tenant_id=1 AND id=?", f.user.ID))
	response, _ = adminRequest(t, f.adminFixture, "POST", resourcePath("users", f.user.ID)+"/unlock", nil, "")
	require.Equal(t, 404, response.Code)
	t.Log("S4 unlock: two concurrent calls=200/200 with one audit; foreign and deleted targets=404")
}
