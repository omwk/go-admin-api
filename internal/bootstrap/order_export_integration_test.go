package bootstrap

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/omwk/go-admin-api/internal/kernel/errs"
	"github.com/omwk/go-admin-api/internal/modules/catalog"
	"github.com/omwk/go-admin-api/internal/modules/identity"
	"github.com/omwk/go-admin-api/internal/modules/identity/model"
	"github.com/omwk/go-admin-api/internal/modules/order"
	"github.com/stretchr/testify/require"
)

func TestOrderExportIntegrationFrozenPIIFreeProjection(t *testing.T) {
	// REQ-ORD-007/008: a synthetic PII canary must never reach materialization or CSV.
	f := newOrderExportFixture(t)
	snapshot := f.orders.snapshot(t, "export-instance")
	marker := "PII-CANARY-123"
	snapshot.Address = &order.AddressInput{ReceiverName: []byte(marker), Phone: []byte(marker), Address: []byte(marker)}
	result, err := f.orders.apply(snapshot)
	require.NoError(t, err)
	require.NoError(t, f.database.Exec(f.ctx, "UPDATE sales_order SET order_no=? WHERE id=?", " \t=1+1", result.OrderID))
	job := f.create(t)
	first, err := f.store.BeginExport(f.ctx, 1, job.ID)
	require.NoError(t, err)
	require.EqualValues(t, 1, first.RowTotal)
	rows, err := f.store.ExportRows(f.ctx, first, 0)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	for _, forbidden := range []string{marker, "receiver_name", "phone", "address", "buyer_id", "buyer_nick", "raw", "ext", "_ingestion"} {
		require.NotContains(t, string(rows[0].Projection), forbidden)
	}
	require.NoError(t, f.database.Exec(f.ctx, "UPDATE sales_order SET order_no='changed-after-freeze' WHERE id=?", result.OrderID))
	require.NoError(t, f.store.FailExport(f.ctx, first))
	second, err := f.store.BeginExport(f.ctx, 1, job.ID)
	require.NoError(t, err)
	require.NoError(t, f.files.Upload(f.ctx, second))
	records := f.csv(t, job.ID)
	require.Equal(t, order.ExportCSVHeader(), records[0])
	require.Equal(t, "' \t=1+1", records[1][2])
	encoded, err := json.Marshal(records)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), marker)
	require.NotContains(t, string(encoded), "changed-after-freeze")
	status, err := f.exports.Status(f.ctx, 1, f.actor.UserID, job.ID)
	require.NoError(t, err)
	require.Equal(t, "succeeded", status.Status)
	require.NotEmpty(t, status.DownloadURL)
	_, err = f.files.Status(f.ctx, 1, f.actor.UserID, job.ID)
	require.Equal(t, 404, errs.From(err).HTTPStatus)
}

func TestOrderExportIntegrationHTTPAndPermissionIsolation(t *testing.T) {
	f := newOrderExportFixture(t)
	f.add(t, "http-export", time.Now().UTC())
	response, _ := f.request(t, "POST", "/orders/export", map[string]any{"filters": map[string]any{"phone": "PII-CANARY-123"}})
	require.Equal(t, 400, response.Code)
	require.NotContains(t, response.Body.String(), "PII-CANARY-123")
	response, envelope := f.request(t, "POST", "/orders/export", order.ExportRequest{})
	require.Equal(t, 201, response.Code, response.Body.String())
	var job catalog.ExportJob
	require.NoError(t, json.Unmarshal(envelope.Data, &job))
	require.NoError(t, f.files.Execute(f.ctx, 1, job.ID))
	response, _ = f.request(t, "GET", fmt.Sprintf("/orders/exports/%d", job.ID), nil)
	require.Equal(t, 200, response.Code, response.Body.String())
	response, _ = f.request(t, "GET", fmt.Sprintf("/exports/%d", job.ID), nil)
	require.Equal(t, 404, response.Code)
	_, err := f.exports.Status(f.ctx, 2, f.actor.UserID, job.ID)
	require.Error(t, err)
	_, err = f.exports.Status(f.ctx, 1, f.user.ID, job.ID)
	require.Equal(t, 403, errs.From(err).HTTPStatus)
	assignProfileRole(t, f.identityFixture, identity.SalesRoleCode)
	f.bearer = f.login(t).AccessToken
	response, envelope = f.request(t, "POST", "/orders/export", order.ExportRequest{})
	require.Equal(t, 403, response.Code)
	require.Equal(t, 20107, envelope.Code)
}

func TestOrderExportIntegrationRevokedShopScopeAndWorkerPermission(t *testing.T) {
	f := newOrderExportFixture(t)
	f.add(t, "scope-export", time.Now().UTC())
	job := f.create(t)
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	ordinaryScopeActor(t, f.identityFixture, f.actor.UserID)
	require.NoError(t, q.Model(&model.Role{}).Where("tenant_id=1 AND code=?", identity.SuperAdminRoleCode).Update("data_scope", "self").Error)
	require.NoError(t, q.Table("shop").Where("id=?", f.shop.ID).Update("owner_user_id", f.user.ID).Error)
	_, err := f.store.BeginExport(f.ctx, 1, job.ID)
	require.Equal(t, 403, errs.From(err).HTTPStatus)
	require.NoError(t, q.Table("shop").Where("id=?", f.shop.ID).Update("owner_user_id", f.actor.UserID).Error)
	require.NoError(t, f.files.Execute(f.ctx, 1, job.ID))
	require.NoError(t, q.Table("shop").Where("id=?", f.shop.ID).Update("owner_user_id", f.user.ID).Error)
	_, err = f.exports.Status(f.ctx, 1, f.actor.UserID, job.ID)
	require.Equal(t, 403, errs.From(err).HTTPStatus)
	require.NoError(t, q.Table("shop").Where("id=?", f.shop.ID).Update("owner_user_id", f.actor.UserID).Error)
	next := f.create(t)
	require.NoError(t, q.Table("sys_user").Where("id=?", f.actor.UserID).Update("is_super", false).Error)
	require.NoError(t, q.Exec("DELETE FROM sys_role_permission WHERE permission_id IN (SELECT id FROM sys_permission WHERE code='trade:order:export')").Error)
	_, err = f.store.BeginExport(f.ctx, 1, next.ID)
	require.Equal(t, 403, errs.From(err).HTTPStatus)
}
