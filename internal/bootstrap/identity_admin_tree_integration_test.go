package bootstrap

import (
	"context"
	"sync"
	"testing"

	"github.com/omwk/go-admin-api/internal/kernel/audit"
	"github.com/omwk/go-admin-api/internal/kernel/errs"
	"github.com/omwk/go-admin-api/internal/modules/identity"
	"github.com/omwk/go-admin-api/internal/modules/identity/model"
	"github.com/omwk/go-admin-api/internal/modules/identity/repo"
	"github.com/stretchr/testify/require"
)

func menuUpdate(row model.Menu, parent int64) identity.MenuRequest {
	return identity.MenuRequest{ParentID: parent, Name: row.Name, Type: row.Type, Path: row.Path, Component: row.Component, Redirect: row.Redirect, Icon: row.Icon,
		Sort: row.Sort, Visible: row.Visible, KeepAlive: row.KeepAlive, IsExternal: row.IsExternal, PermissionCode: row.PermissionCode, Meta: row.Meta, Status: row.Status}
}

func TestAdminIntegrationMenuMovesValidateHiddenWholeSubtree(t *testing.T) {
	f := newAdminFixture(t)
	root := createAdminMenu(t, f, "root", 0)
	child := createAdminMenu(t, f, "child", root.ID)
	leaf := child
	for i := 0; i < 3; i++ {
		leaf = createAdminMenu(t, f, "hidden disabled", leaf.ID)
	}
	other := createAdminMenu(t, f, "other", 0)
	for _, tc := range []struct {
		parent int64
		code   int
	}{{root.ID, 20106}, {leaf.ID, 20106}, {other.ID, 10001}, {9999999, 10003}} {
		response, envelope := adminRequest(t, f, "PUT", resourcePath("menus", root.ID), menuUpdate(root, tc.parent), "")
		require.Equal(t, tc.code, envelope.Code, response.Body.String())
	}
	response, envelope := adminRequest(t, f, "POST", "/menus", identity.MenuRequest{ParentID: leaf.ID, Name: "too deep", Type: "button", Status: "active"}, "")
	require.Equal(t, 400, response.Code)
	require.Equal(t, 10001, envelope.Code)
	response, _ = adminRequest(t, f, "PUT", resourcePath("menus", child.ID), menuUpdate(child, 0), "")
	require.Equal(t, 200, response.Code, response.Body.String())
	current, err := f.store.Menu(f.ctx, 1, child.ID)
	require.NoError(t, err)
	require.Zero(t, current.ParentID)
	require.Equal(t, child.Path, current.Path)
}

type waitingAdminStore struct {
	repo.Admin
	attempted chan struct{}
	once      sync.Once
}

func (s *waitingAdminStore) LockManagement(ctx context.Context, tenant int64) error {
	s.once.Do(func() { close(s.attempted) })
	return s.Admin.LockManagement(ctx, tenant)
}

func TestAdminIntegrationCrossingTransactionsCannotCreateMenuCycle(t *testing.T) {
	// REQ-IAM-006: transaction B starts its lock attempt after A reads, before A commits.
	f := newAdminFixture(t)
	a := createAdminMenu(t, f, "a", 0)
	b := createAdminMenu(t, f, "b", 0)
	attempted, firstRead := make(chan struct{}), make(chan struct{})
	wrapped := &waitingAdminStore{Admin: f.store, attempted: attempted}
	second, err := identity.NewAdminService(wrapped, func(ctx context.Context, e audit.Event) error { return audit.Record(ctx, f.database, e) })
	require.NoError(t, err)
	results := make(chan error, 2)
	go func() {
		results <- f.store.Transaction(f.ctx, func(ctx context.Context) error {
			if err := f.store.LockManagement(ctx, 1); err != nil {
				return err
			}
			if _, err := f.store.Menus(ctx, 1); err != nil {
				return err
			}
			close(firstRead)
			select {
			case <-attempted:
			case <-ctx.Done():
				return ctx.Err()
			}
			_, err := f.service.UpdateMenu(ctx, f.actor, a.ID, menuUpdate(a, b.ID))
			return err
		})
	}()
	select {
	case <-firstRead:
	case <-f.ctx.Done():
		t.Fatal(f.ctx.Err())
	}
	go func() {
		_, err := second.Execute(f.ctx, f.actor, "move-b", "", "", func(ctx context.Context) (repo.SavedResponse, error) {
			_, err := second.UpdateMenu(ctx, f.actor, b.ID, menuUpdate(b, a.ID))
			return repo.SavedResponse{Status: 200}, err
		})
		results <- err
	}()
	winners, cycles := 0, 0
	for i := 0; i < 2; i++ {
		select {
		case err := <-results:
			if err == nil {
				winners++
			} else {
				require.Equal(t, 20106, errs.From(err).Code)
				cycles++
			}
		case <-f.ctx.Done():
			t.Fatal(f.ctx.Err())
		}
	}
	require.Equal(t, 1, winners)
	require.Equal(t, 1, cycles)
	rows, err := f.store.Menus(f.ctx, 1)
	require.NoError(t, err)
	current, err := f.store.Menu(f.ctx, 1, a.ID)
	require.NoError(t, err)
	require.NoError(t, identity.ValidateMenuMove(rows, a.ID, current.ParentID))
}
