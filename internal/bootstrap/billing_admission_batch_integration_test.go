package bootstrap

import (
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestBillingGatewayIntegrationAdmissionRoundTrips(t *testing.T) {
	// REQ-BILL-003: every durable admission fact remains, in one PG query.
	for _, source := range []string{"wallet", "free", "paid"} {
		t.Run(source, func(t *testing.T) {
			f := newBillingHTTPFixture(t, 100)
			if source != "wallet" {
				billingGift(t, f, source, 10, nil)
			}
			q, cancel := f.database.WithContext(f.ctx)
			defer cancel()
			var queries atomic.Int64
			record := func(*gorm.DB) { queries.Add(1) }
			require.NoError(t, q.Callback().Create().Before("gorm:create").Register("m9c:admission", record))
			require.NoError(t, q.Callback().Query().Before("gorm:query").Register("m9c:admission", record))
			require.NoError(t, q.Callback().Update().Before("gorm:update").Register("m9c:admission", record))
			require.NoError(t, q.Callback().Raw().Before("gorm:raw").Register("m9c:admission", record))
			require.NoError(t, q.Callback().Row().Before("gorm:row").Register("m9c:admission", record))
			c := f.intent()
			require.NoError(t, f.calls.Start(f.ctx, c))
			require.EqualValues(t, 1, queries.Load(), "admission must combine all durable writes in one PG roundtrip")
		})
	}
}
