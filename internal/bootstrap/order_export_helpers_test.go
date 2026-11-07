package bootstrap

import (
	"encoding/csv"
	"testing"

	"github.com/omwk/go-admin-api/internal/modules/catalog"
	"github.com/omwk/go-admin-api/internal/modules/identity"
	"github.com/omwk/go-admin-api/internal/modules/order"
	"github.com/stretchr/testify/require"
)

type orderExportFixture struct {
	*orderQueryFixture
	store   *exportStore
	objects *exportMemory
	files   *catalog.ExportService
	exports *order.ExportService
}

func newOrderExportFixture(t *testing.T) *orderExportFixture {
	t.Helper()
	return orderExportFixtureFor(t, newOrderQueryFixture(t))
}

func orderExportFixtureFor(t *testing.T, queries *orderQueryFixture) *orderExportFixture {
	t.Helper()
	f := &orderExportFixture{orderQueryFixture: queries, objects: newExportMemory()}
	f.store = &exportStore{database: f.database, bucket: "order-export-tests"}
	var err error
	f.files, err = catalog.NewExportService(f.store, f.objects, newAdminStore(f.database).Authorize)
	require.NoError(t, err)
	require.NoError(t, useOrderExportFormat(f.files))
	f.exports, err = order.NewExportService(f.store, f.files, (&orderQueryStore{database: f.database}).Authorize)
	require.NoError(t, err)
	profiles, err := identity.NewProfileService(newProfileStore(f.database))
	require.NoError(t, err)
	require.NoError(t, order.RegisterExportRoutes(f.router, f.identityFixture.service, profiles, f.exports))
	require.NoError(t, catalog.RegisterExportRoutes(f.router, f.identityFixture.service, profiles, f.files))
	return f
}

func (f *orderExportFixture) create(t *testing.T) catalog.ExportJob {
	t.Helper()
	job, err := f.exports.Create(f.ctx, 1, f.actor.UserID, order.ExportRequest{Filters: order.ExportFilter{ShopID: f.shop.ID}}, nil)
	require.NoError(t, err)
	return job
}

func (f *orderExportFixture) csv(t *testing.T, id int64) [][]string {
	t.Helper()
	job, err := f.store.ReadExport(f.ctx, 1, f.actor.UserID, id)
	require.NoError(t, err)
	object, err := f.objects.Get(f.ctx, job.ObjectKey)
	require.NoError(t, err)
	defer func() { require.NoError(t, object.Body.Close()) }()
	records, err := csv.NewReader(object.Body).ReadAll()
	require.NoError(t, err)
	return records
}
