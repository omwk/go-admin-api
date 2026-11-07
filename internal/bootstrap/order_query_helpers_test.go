package bootstrap

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/omwk/go-admin-api/internal/modules/identity"
	"github.com/omwk/go-admin-api/internal/modules/order"
	"github.com/stretchr/testify/require"
)

type orderQueryFixture struct {
	*credentialFixture
	orders  *orderFixture
	queries *order.QueryService
}

func newOrderQueryFixture(t *testing.T) *orderQueryFixture {
	t.Helper()
	f := &orderQueryFixture{credentialFixture: newCredentialFixture(t)}
	var err error
	f.orders = &orderFixture{database: f.database, ctx: f.ctx, sealer: f.sealer, shopID: f.shop.ID, platformID: f.shop.PlatformID}
	f.orders.upserter, err = order.NewUpserter(f.sealer, order.MoneyPolicy{FormulaToleranceCents: 2, PaymentToleranceCents: 2})
	require.NoError(t, err)
	f.queries, err = order.NewQueryService(NewOrderQueryStore(f.database))
	require.NoError(t, err)
	profiles, err := identity.NewProfileService(newProfileStore(f.database))
	require.NoError(t, err)
	require.NoError(t, order.RegisterRoutes(f.router, f.identityFixture.service, profiles, f.queries))
	return f
}

func (f *orderQueryFixture) add(t *testing.T, key string, created time.Time) int64 {
	t.Helper()
	snapshot := f.orders.snapshot(t, key)
	snapshot.PlacedAt = &created
	result, err := f.orders.apply(snapshot)
	require.NoError(t, err)
	require.Equal(t, "applied", result.Outcome)
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	require.NoError(t, q.Table("sales_order").Where("id=?", result.OrderID).Update("created_at", created).Error)
	return result.OrderID
}

func (f *orderQueryFixture) page(t *testing.T, path string) order.Page {
	t.Helper()
	response, envelope := f.request(t, "GET", path, nil)
	require.Equal(t, 200, response.Code, response.Body.String())
	var page order.Page
	require.NoError(t, json.Unmarshal(envelope.Data, &page))
	return page
}

func (f *orderQueryFixture) stats(t *testing.T) []order.Stat {
	t.Helper()
	response, envelope := f.request(t, "GET", "/orders/stats", nil)
	require.Equal(t, 200, response.Code, response.Body.String())
	var rows []order.Stat
	require.NoError(t, json.Unmarshal(envelope.Data, &rows))
	return rows
}

func (f *orderQueryFixture) detail(t *testing.T, id int64) order.Detail {
	t.Helper()
	response, envelope := f.request(t, "GET", fmt.Sprintf("/orders/%d", id), nil)
	require.Equal(t, 200, response.Code, response.Body.String())
	for _, field := range []string{"receiver_name", "phone", "address", "cipher", "nonce", "candidate_states", "platform_amounts"} {
		require.NotContains(t, response.Body.String(), field)
	}
	var detail order.Detail
	require.NoError(t, json.Unmarshal(envelope.Data, &detail))
	return detail
}
