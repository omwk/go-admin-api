package bootstrap

import (
	"context"
	"errors"
	"testing"

	"github.com/omwk/go-admin-api/internal/kernel/audit"
	"github.com/omwk/go-admin-api/internal/kernel/errs"
	"github.com/omwk/go-admin-api/internal/modules/identity"
	"github.com/omwk/go-admin-api/internal/modules/identity/repo"
	"github.com/stretchr/testify/require"
)

type departmentWrite func(context.Context, *identity.AdminService) error

func crossingDepartmentWrites(t *testing.T, f *adminFixture, first, second departmentWrite) []error {
	t.Helper()
	attempted, read := make(chan struct{}), make(chan struct{})
	wrapped := &waitingAdminStore{Admin: f.store, attempted: attempted}
	service, err := identity.NewAdminService(wrapped, func(ctx context.Context, event audit.Event) error { return audit.Record(ctx, f.database, event) })
	require.NoError(t, err)
	results := make(chan error, 2)
	go func() {
		results <- f.store.Transaction(f.ctx, func(ctx context.Context) error {
			if err := f.store.LockManagement(ctx, 1); err != nil {
				return err
			}
			if _, err := f.store.LockDepartments(ctx, 1); err != nil {
				return err
			}
			close(read)
			select {
			case <-attempted:
			case <-ctx.Done():
				return ctx.Err()
			}
			return first(ctx, f.service)
		})
	}()
	select {
	case <-read:
	case <-f.ctx.Done():
		t.Fatal(f.ctx.Err())
	}
	go func() {
		_, err := service.Execute(f.ctx, f.actor, "s1-crossing", "", "", func(ctx context.Context) (repo.SavedResponse, error) {
			return repo.SavedResponse{Status: 200}, second(ctx, service)
		})
		results <- err
	}()
	got := []error{}
	for i := 0; i < 2; i++ {
		select {
		case err := <-results:
			got = append(got, err)
		case <-f.ctx.Done():
			t.Fatal(f.ctx.Err())
		}
	}
	return got
}

func requireDepartmentWriteResults(t *testing.T, results []error, code int) {
	t.Helper()
	succeeded, rejected := 0, 0
	for _, err := range results {
		if err == nil {
			succeeded++
		} else {
			require.Equal(t, code, errs.From(err).Code, err)
			rejected++
		}
	}
	require.Equal(t, 1, succeeded)
	require.Equal(t, 1, rejected)
}

func TestAdminS1IntegrationDepartmentConcurrentDuplicates(t *testing.T) {
	// S1 §3.3: a second transaction already waiting on the lock sees the committed sibling.
	for _, root := range []bool{false, true} {
		t.Run(map[bool]string{false: "siblings", true: "roots"}[root], func(t *testing.T) {
			f := newAdminFixture(t)
			parent := f.user.DepartmentID
			if root {
				parent = 0
			}
			request := identity.DepartmentRequest{ParentID: parent, Name: "S1 concurrent", Status: "active"}
			write := func(ctx context.Context, service *identity.AdminService) error {
				_, err := service.CreateDepartment(ctx, f.actor, request)
				return err
			}
			requireDepartmentWriteResults(t, crossingDepartmentWrites(t, f, write, write), 10001)
			q, cancel := f.database.WithContext(f.ctx)
			defer cancel()
			var count int64
			require.NoError(t, q.Table("sys_dept").Where("tenant_id = ? AND parent_id = ? AND name = ?", 1, parent, request.Name).Count(&count).Error)
			require.EqualValues(t, 1, count)
		})
	}
}

func TestAdminS1IntegrationDepartmentConcurrentMovesCannotCycle(t *testing.T) {
	// S1 §3.1/3.3: crossing A->B and B->A moves cannot both commit.
	f := newAdminFixture(t)
	a := createS1Department(t, f, "S1 cycle a", f.user.DepartmentID)
	b := createS1Department(t, f, "S1 cycle b", f.user.DepartmentID)
	first := func(ctx context.Context, service *identity.AdminService) error {
		_, err := service.UpdateDepartment(ctx, f.actor, a.ID, identity.DepartmentRequest{ParentID: b.ID, Name: a.Name, Status: "active"})
		return err
	}
	second := func(ctx context.Context, service *identity.AdminService) error {
		_, err := service.UpdateDepartment(ctx, f.actor, b.ID, identity.DepartmentRequest{ParentID: a.ID, Name: b.Name, Status: "active"})
		return err
	}
	requireDepartmentWriteResults(t, crossingDepartmentWrites(t, f, first, second), 10001)
	require.Equal(t, b.ID, readS1Department(t, f, a.ID).ParentID)
	require.Equal(t, f.user.DepartmentID, readS1Department(t, f, b.ID).ParentID)
}

func TestAdminS1IntegrationDepartmentDeleteSerializesChildCreation(t *testing.T) {
	// S1 §3.2/3.3: a child insert queued behind a deletion returns 404, never an orphan.
	f := newAdminFixture(t)
	parent := createS1Department(t, f, "S1 deleting", f.user.DepartmentID)
	first := func(ctx context.Context, service *identity.AdminService) error {
		return service.DeleteDepartment(ctx, f.actor, parent.ID)
	}
	second := func(ctx context.Context, service *identity.AdminService) error {
		_, err := service.CreateDepartment(ctx, f.actor, identity.DepartmentRequest{ParentID: parent.ID, Name: "S1 late child", Status: "active"})
		return err
	}
	requireDepartmentWriteResults(t, crossingDepartmentWrites(t, f, first, second), 10003)
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	var count int64
	require.NoError(t, q.Table("sys_dept").Where("id = ? OR parent_id = ?", parent.ID, parent.ID).Count(&count).Error)
	require.Zero(t, count)
}

type failingDepartmentStore struct {
	repo.Admin
	failID int64
}

func (s *failingDepartmentStore) UpdateDepartment(ctx context.Context, tenant, id int64, values map[string]any) error {
	if id == s.failID {
		return errors.New("injected descendant write failure")
	}
	return s.Admin.UpdateDepartment(ctx, tenant, id, values)
}

func TestAdminS1IntegrationDepartmentSubtreeFailureRollsBack(t *testing.T) {
	// S1 §3.3/§4.5: fail after root + child writes; paths, parent, audit and idempotency all roll back.
	f := newAdminFixture(t)
	a := createS1Department(t, f, "S1 rollback a", f.user.DepartmentID)
	b := createS1Department(t, f, "S1 rollback b", a.ID)
	c := createS1Department(t, f, "S1 rollback c", b.ID)
	target := createS1Department(t, f, "S1 rollback target", 0)
	store := &failingDepartmentStore{Admin: f.store, failID: c.ID}
	service, err := identity.NewAdminService(store, func(ctx context.Context, event audit.Event) error { return audit.Record(ctx, f.database, event) })
	require.NoError(t, err)
	_, err = service.Execute(f.ctx, f.actor, "s1-rollback", "s1-rollback-key", "body-hash", func(ctx context.Context) (repo.SavedResponse, error) {
		_, err := service.UpdateDepartment(ctx, f.actor, a.ID, identity.DepartmentRequest{ParentID: target.ID, Name: a.Name, Status: "active"})
		return repo.SavedResponse{Status: 200}, err
	})
	require.ErrorContains(t, err, "injected descendant write failure")
	for _, row := range []repo.DepartmentRow{a, b, c} {
		require.Equal(t, row, readS1Department(t, f, row.ID))
	}
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	var count int64
	require.NoError(t, q.Table("audit_log").Where("action = ?", "system:dept:update").Count(&count).Error)
	require.Zero(t, count)
	_, found, err := f.store.Replay(f.ctx, 1, "s1-rollback", "s1-rollback-key", "body-hash")
	require.NoError(t, err)
	require.False(t, found)
}

func TestAdminS1IntegrationDepartmentAuditFailureRollsBack(t *testing.T) {
	// S1 hard deletes and creates must not commit without their audit snapshot.
	f := newAdminFixture(t)
	row := createS1Department(t, f, "S1 audit delete", f.user.DepartmentID)
	service, err := identity.NewAdminService(f.store, func(context.Context, audit.Event) error { return errors.New("injected department audit failure") })
	require.NoError(t, err)
	for _, method := range []string{"create", "delete"} {
		_, err := service.Execute(f.ctx, f.actor, "s1-audit-"+method, "", "", func(ctx context.Context) (repo.SavedResponse, error) {
			if method == "delete" {
				return repo.SavedResponse{Status: 204}, service.DeleteDepartment(ctx, f.actor, row.ID)
			}
			_, err := service.CreateDepartment(ctx, f.actor, identity.DepartmentRequest{ParentID: row.ParentID, Name: "S1 audit create", Status: "active"})
			return repo.SavedResponse{Status: 201}, err
		})
		require.ErrorContains(t, err, "injected department audit failure")
	}
	require.Equal(t, row, readS1Department(t, f, row.ID))
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	var count int64
	require.NoError(t, q.Table("sys_dept").Where("tenant_id = ? AND name = ?", 1, "S1 audit create").Count(&count).Error)
	require.Zero(t, count)
}
