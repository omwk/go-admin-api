package bootstrap

import (
	"os"
	"regexp"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSystemDictSupplyIntegrationSyncCheckDomain(t *testing.T) {
	// BE-IDN-008 §3.4: compare actual seed values with both migration SQL and live CHECK.
	f := newSystemFixture(t, nil)
	require.NoError(t, f.database.Exec(f.ctx, "INSERT INTO tenant(id,code,name) OVERRIDING SYSTEM VALUE VALUES(2,'dict-domain','Dictionary domain')"))
	require.NoError(t, seedSystemDictionaries(f.ctx, f.database, 2))
	store := newSystemDictStore(f.database)
	dict, err := store.DictionaryByType(f.ctx, 2, "sync_run_status")
	require.NoError(t, err)
	items, err := store.DictionaryItems(f.ctx, 2, dict.ID, 1, 100, false)
	require.NoError(t, err)
	values := []string{}
	for _, item := range items.List {
		values = append(values, item.Value)
	}
	sql, err := os.ReadFile("../../migrations/000006_sync_init.up.sql")
	require.NoError(t, err)
	match := regexp.MustCompile(`(?s)CREATE TABLE sync_run\s*\(.*?status text NOT NULL CHECK \(status IN \(([^)]+)\)\)`).FindStringSubmatch(string(sql))
	require.Len(t, match, 2)
	var liveCheck string
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	require.NoError(t, q.Raw("SELECT pg_get_constraintdef(oid) FROM pg_constraint WHERE conrelid='sync_run'::regclass AND conname='sync_run_status_check'").Scan(&liveCheck).Error)
	quoted := regexp.MustCompile(`'([^']+)'`)
	for _, source := range []struct{ name, text string }{{"000006 CHECK", match[1]}, {"live CHECK", liveCheck}} {
		actual := []string{}
		for _, entry := range quoted.FindAllStringSubmatch(source.text, -1) {
			actual = append(actual, entry[1])
		}
		require.Equal(t, values, actual, source.name)
		for i, value := range values {
			t.Logf("sync_run_status[%d]=%s == %s[%d]=%s", i+1, value, source.name, i+1, actual[i])
		}
	}
}

func TestSystemDictSupplyIntegrationOrderDisplayDomain(t *testing.T) {
	// BE-IDN-008 §3.4: evaluate the actual orderDisplaySQL projection for every display value.
	f := newSystemFixture(t, nil)
	require.NoError(t, f.database.Exec(f.ctx, "INSERT INTO tenant(id,code,name) OVERRIDING SYSTEM VALUE VALUES(2,'order-domain','Order domain')"))
	require.NoError(t, seedSystemDictionaries(f.ctx, f.database, 2))
	store := newSystemDictStore(f.database)
	dict, err := store.DictionaryByType(f.ctx, 2, "order_status")
	require.NoError(t, err)
	items, err := store.DictionaryItems(f.ctx, 2, dict.ID, 1, 100, false)
	require.NoError(t, err)
	values := []string{}
	for _, item := range items.List {
		values = append(values, item.Value)
	}
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	actual := []string{}
	for _, tc := range []struct{ lifecycle, payment, afterSales, display string }{
		{"pending_payment", "unpaid", "none", "pending_payment"},
		{"paid", "paid", "none", "pending_shipment"},
		{"shipped", "paid", "none", "shipped"},
		{"completed", "paid", "none", "completed"},
		{"cancelled", "unpaid", "none", "closed"},
		{"paid", "paid", "refunding", "refunding"},
		{"cancelled", "refunded", "refunded", "refunded"},
		{"future_state", "unpaid", "none", "unknown"},
	} {
		var display string
		require.NoError(t, q.Raw("SELECT "+orderDisplaySQL+" FROM (VALUES (?::text,?::text,?::text)) AS o(lifecycle_status,payment_status,after_sales_status)", tc.lifecycle, tc.payment, tc.afterSales).Scan(&display).Error)
		require.Equal(t, tc.display, display)
		actual = append(actual, display)
		t.Logf("orderDisplaySQL(%s/%s/%s)=%s == order_status[%d]=%s", tc.lifecycle, tc.payment, tc.afterSales, display, len(actual), values[len(actual)-1])
	}
	require.Equal(t, values, actual)
	var exhaustive []string
	require.NoError(t, q.Raw("SELECT DISTINCT "+orderDisplaySQL+` AS display FROM
		(VALUES ('pending_payment'),('paid'),('shipped'),('completed'),('cancelled'),('future_state')) l(lifecycle_status)
		CROSS JOIN (VALUES ('unpaid'),('paid'),('refunded')) p(payment_status)
		CROSS JOIN (VALUES ('none'),('refunding'),('refunded')) a(after_sales_status)
		CROSS JOIN LATERAL (SELECT lifecycle_status,payment_status,after_sales_status) o`).Scan(&exhaustive).Error)
	require.ElementsMatch(t, values, exhaustive)
}
