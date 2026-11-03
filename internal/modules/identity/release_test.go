package identity

import (
	"context"
	"errors"
	"testing"

	"github.com/omwk/go-admin-api/internal/kernel/audit"
	"github.com/omwk/go-admin-api/internal/modules/identity/model"
	"github.com/stretchr/testify/require"
)

type releaseStoreFake struct {
	role         model.Role
	current      []string
	ids          map[string]int64
	markers      map[string]string
	users        []int64
	inserted     []int64
	registered   []model.Permission
	transactions int
}

func (f *releaseStoreFake) Transaction(ctx context.Context, run func(context.Context) error) error {
	f.transactions++
	return run(ctx)
}
func (f *releaseStoreFake) LockPermissionRelease(context.Context, int64) error { return nil }
func (f *releaseStoreFake) RoleByCode(context.Context, int64, string) (model.Role, error) {
	if f.role.ID == 0 {
		return model.Role{}, errors.New("missing role")
	}
	return f.role, nil
}
func (f *releaseStoreFake) RolePermissionCodes(context.Context, int64, int64) ([]string, error) {
	return append([]string(nil), f.current...), nil
}
func (f *releaseStoreFake) RegisterPermissions(_ context.Context, _ int64, rows []model.Permission) error {
	f.registered = append(f.registered, rows...)
	return nil
}
func (f *releaseStoreFake) PermissionIDs(context.Context, int64, []string) (map[string]int64, error) {
	return f.ids, nil
}
func (f *releaseStoreFake) InsertRolePermissions(_ context.Context, _ int64, _ int64, ids []int64) error {
	f.inserted = append(f.inserted, ids...)
	return nil
}
func (f *releaseStoreFake) PermissionReleaseMarker(_ context.Context, _ int64, release string) (string, bool, error) {
	value, ok := f.markers[release]
	return value, ok, nil
}
func (f *releaseStoreFake) SavePermissionReleaseMarker(_ context.Context, _ int64, release, summary string) error {
	f.markers[release] = summary
	return nil
}
func (f *releaseStoreFake) PermissionReleaseUsers(context.Context, int64, int64) ([]int64, error) {
	return f.users, nil
}

func TestPermissionReleaseApplyIsIncrementalAndIdempotent(t *testing.T) {
	store := &releaseStoreFake{role: model.Role{ID: 7}, current: []string{"shop:store:list"}, ids: map[string]int64{"shop:store:create": 11, "shop:store:read": 12}, markers: map[string]string{}, users: []int64{4, 4, 2}}
	service, err := NewPermissionReleaseService(store, func(context.Context, audit.Event) error { return nil })
	require.NoError(t, err)
	request := PermissionReleaseRequest{RoleCode: "operator", Codes: []string{"shop:store:read", "shop:store:create"}, Release: "r1"}
	result, err := service.Apply(context.Background(), 1, request)
	require.NoError(t, err)
	require.True(t, result.Changed)
	require.Equal(t, 2, result.Added)
	require.Equal(t, []int64{2, 4}, result.AffectedUser)
	require.Len(t, store.inserted, 2)
	require.Len(t, store.registered, 2)

	retry, err := service.Apply(context.Background(), 1, request)
	require.NoError(t, err)
	require.False(t, retry.Changed)
	require.Zero(t, retry.Added)
	require.Len(t, store.inserted, 2)
}

func TestPermissionReleaseRejectsChangedSummaryAndDuplicateCodes(t *testing.T) {
	store := &releaseStoreFake{role: model.Role{ID: 7}, ids: map[string]int64{"shop:store:create": 11}, markers: map[string]string{"r1": "different"}}
	service, err := NewPermissionReleaseService(store, func(context.Context, audit.Event) error { return nil })
	require.NoError(t, err)
	_, err = service.Apply(context.Background(), 1, PermissionReleaseRequest{RoleCode: "operator", Codes: []string{"shop:store:create"}, Release: "r1"})
	require.Error(t, err)
	_, err = service.Apply(context.Background(), 1, PermissionReleaseRequest{RoleCode: "operator", Codes: []string{"shop:store:create", "shop:store:create"}, Release: "r2"})
	require.Error(t, err)
}
