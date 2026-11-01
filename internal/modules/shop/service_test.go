package shop

import (
	"context"
	"testing"

	"github.com/omwk/go-admin-api/internal/kernel/errs"
	"github.com/omwk/go-admin-api/internal/modules/shop/model"
	"github.com/omwk/go-admin-api/internal/modules/shop/repo"
	"github.com/stretchr/testify/require"
)

type serviceStore struct {
	assignmentErr error
	created       *model.Shop
	updated       bool
	shop          repo.ShopRow
}

func (s *serviceStore) Platforms(context.Context) ([]model.Platform, error) { return nil, nil }
func (s *serviceStore) Shops(context.Context, int64, repo.Page) ([]repo.ShopRow, int64, error) {
	return nil, 0, nil
}
func (s *serviceStore) Shop(_ context.Context, _ int64, _ int64) (repo.ShopRow, error) {
	return s.shop, nil
}
func (s *serviceStore) ValidateAssignment(_ context.Context, _ int64, _, _ int64) error {
	return s.assignmentErr
}
func (s *serviceStore) CreateShop(_ context.Context, row *model.Shop) error {
	row.ID = 9
	s.created = row
	s.shop.Shop = *row
	return nil
}
func (s *serviceStore) UpdateShop(_ context.Context, _, _ int64, _ int, _ map[string]any) error {
	s.updated = true
	return nil
}
func (s *serviceStore) DeleteShop(context.Context, int64, int64) error { return nil }

func testService(t *testing.T, store *serviceStore) *Service {
	t.Helper()
	service, err := NewService(store, func(ctx context.Context, _, _ int64) (context.Context, error) { return ctx, nil })
	require.NoError(t, err)
	return service
}

func TestServiceCreateDefaultsIdentityOwnership(t *testing.T) {
	store := &serviceStore{}
	service := testService(t, store)
	view, err := service.Create(context.Background(), Actor{TenantID: 7, UserID: 11, DepartmentID: 13}, CreateRequest{PlatformID: 1, Name: "shop", ExternalShopID: "external", Timezone: "UTC", Currency: "cny"})
	require.NoError(t, err)
	require.Equal(t, int64(9), view.ID)
	require.Equal(t, int64(7), store.created.TenantID)
	require.Equal(t, int64(11), store.created.OwnerUserID)
	require.Equal(t, int64(13), store.created.DepartmentID)
	require.Equal(t, "CNY", store.created.Currency)
}

func TestServiceCreateRejectsUnmanageableExplicitAssignment(t *testing.T) {
	store := &serviceStore{assignmentErr: errs.New(errs.ErrDataScopeDenied, 403, "超出数据权限")}
	service := testService(t, store)
	_, err := service.Create(context.Background(), Actor{TenantID: 7, UserID: 11, DepartmentID: 13}, CreateRequest{PlatformID: 1, Name: "shop", ExternalShopID: "external", Timezone: "UTC", Currency: "CNY", OwnerUserID: ptr(int64(99))})
	app := errs.From(err)
	require.Equal(t, 403, app.HTTPStatus)
	require.Equal(t, errs.ErrDataScopeDenied, app.Code)
	require.Nil(t, store.created)
}

func TestServiceUpdateRejectsStaleVersionBeforeMutation(t *testing.T) {
	store := &serviceStore{shop: repo.ShopRow{Shop: model.Shop{ID: 9, TenantID: 7, OwnerUserID: 11, DepartmentID: 13, Version: 2}}}
	service := testService(t, store)
	_, err := service.Update(context.Background(), Actor{TenantID: 7, UserID: 11, DepartmentID: 13}, 9, UpdateRequest{Version: ptr(1), Name: ptr("new")})
	app := errs.From(err)
	require.Equal(t, 409, app.HTTPStatus)
	require.Equal(t, errs.ErrVersionConflict, app.Code)
	require.False(t, store.updated)
}

func ptr[T any](value T) *T { return &value }
