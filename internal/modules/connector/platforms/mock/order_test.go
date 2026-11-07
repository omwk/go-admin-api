package mock

import (
	"context"
	"testing"
	"time"

	"github.com/omwk/go-admin-api/internal/modules/connector"
	"github.com/stretchr/testify/require"
)

func TestOrderListDetailAndEqualTimeBoundary(t *testing.T) {
	p := New("")
	require.True(t, p.OrderCapabilities().PIIFree)
	from := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	r := connector.OrderListRequest{From: from, Until: from.Add(time.Second), PageSize: 1}
	first, err := p.ListOrders(context.Background(), connector.Credential{}, r)
	require.NoError(t, err)
	require.Len(t, first.Orders, 1)
	require.True(t, first.HasMore)
	r.Cursor = first.NextCursor
	second, err := p.ListOrders(context.Background(), connector.Credential{}, r)
	require.NoError(t, err)
	require.Len(t, second.Orders, 1)
	require.False(t, second.HasMore)
	require.NotEqual(t, first.Orders[0].ExternalID, second.Orders[0].ExternalID)
	require.Equal(t, first.Orders[0].UpdatedAt, second.Orders[0].UpdatedAt)
	detail, err := p.FetchOrder(context.Background(), connector.Credential{}, first.Orders[0].ExternalID)
	require.NoError(t, err)
	require.Contains(t, string(detail.Payload), `"items"`)
	_, err = p.FetchOrder(context.Background(), connector.Credential{}, "missing")
	require.Error(t, err)
}
