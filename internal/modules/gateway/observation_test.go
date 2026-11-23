package gateway

import (
	"context"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestObservationRangeAndPagination(t *testing.T) {
	// BE-IDN-029: deterministic defaults and strict query boundaries.
	now := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
	s := &ObservationService{now: func() time.Time { return now }}
	f, err := s.filter(url.Values{}, false)
	require.NoError(t, err)
	require.Equal(t, now, f.To)
	require.Equal(t, now.Add(-24*time.Hour), f.From)
	require.Equal(t, 1, f.Page)
	require.Equal(t, 20, f.PageSize)
	for _, query := range []string{
		"from=2026-10-01T00:00:00Z", "from=bad", "to=bad", "from=2026-11-24T00:00:00Z",
		"page=0", "page_size=101", "client_id=-1", "route_id=no", "status=unknown", "page=1&page=2",
	} {
		t.Run(query, func(t *testing.T) {
			values, err := url.ParseQuery(query)
			require.NoError(t, err)
			_, err = s.filter(values, false)
			require.Error(t, err)
		})
	}
	_, err = s.filter(url.Values{"from": {now.Add(-30 * 24 * time.Hour).Format(time.RFC3339)}}, false)
	require.NoError(t, err)
	for _, group := range []string{"client", "route", "day"} {
		_, err := s.filter(url.Values{"group_by": {group}}, true)
		require.NoError(t, err)
	}
	_, err = s.filter(url.Values{"group_by": {"client;drop"}}, true)
	require.Error(t, err)
}

func TestManualTrialSkipsChargesAndMeter(t *testing.T) {
	// BE-IDN-029: a successful manual call has a durable log but no success meter.
	s, store := newCallTestService(t)
	call := s.NewCall(Principal{TenantID: 1, ClientID: 1}, Route{ID: 1, Version: 1, Method: "GET", BillingMode: "per_call", PriceCents: 7, UnitsPerCall: 1}, "trial-trace", "", "192.0.2.1")
	call.ChargeSource = "manual_test"
	_, err := s.RunRequest(context.Background(), &call, func(context.Context) (OpenOrderStatus, error) {
		return OpenOrderStatus{ID: "public-result", Status: "completed"}, nil
	})
	require.NoError(t, err)
	require.Empty(t, store.meters)
	require.JSONEq(t, `{}`, string(store.calls[call.ID].Result))
}
