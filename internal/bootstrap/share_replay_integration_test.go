package bootstrap

import (
	"fmt"
	"testing"

	"github.com/omwk/go-admin-api/internal/modules/identity"
	"github.com/omwk/go-admin-api/internal/modules/share"
	"github.com/stretchr/testify/require"
)

func TestCustomerIntegrationReplayRechecksTransferredOwner(t *testing.T) {
	f := newShareFixture(t)
	in := share.CustomerInput{Code: "transferred", Name: "Transferred customer"}
	r, _ := f.keyed(t, "POST", "/customers", "owner-replay", in)
	require.Equal(t, 201, r.Code)
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	require.NoError(t, q.Table("sys_role").Where("tenant_id=1 AND code=?", identity.SuperAdminRoleCode).Update("data_scope", "self").Error)
	require.NoError(t, q.Table("customer").Where("code=?", in.Code).Update("owner_user_id", f.user.ID).Error)
	r, _ = f.keyed(t, "POST", "/customers", "owner-replay", in)
	require.Equal(t, 404, r.Code, "a cached write response must respect the current owner scope")
}

func TestPriceListIntegrationDeleteIdempotencyHashesVersion(t *testing.T) {
	f := newShareFixture(t)
	c := f.customer(t, "delete-key")
	list := f.list(t, &c.ID)
	path := fmt.Sprintf("/price-lists/%d?version=1", list.ID)
	r, _ := f.keyed(t, "DELETE", path, "delete-key", nil)
	require.Equal(t, 204, r.Code)
	r, _ = f.keyed(t, "DELETE", path, "delete-key", nil)
	require.Equal(t, 204, r.Code)
	r, e := f.keyed(t, "DELETE", fmt.Sprintf("/price-lists/%d?version=2", list.ID), "delete-key", nil)
	require.Equal(t, 409, r.Code)
	require.Equal(t, 10006, e.Code)
}
