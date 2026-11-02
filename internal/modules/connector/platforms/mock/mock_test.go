package mock

import (
	"context"
	"testing"

	"github.com/omwk/go-admin-api/internal/modules/connector"
	"github.com/stretchr/testify/require"
)

func TestFetchFixturesAndPaging(t *testing.T) {
	platform := New("")
	page, err := platform.Fetch(context.Background(), connector.Credential{}, connector.FetchRequest{Resource: connector.ResourceProduct, PageSize: 1})
	require.NoError(t, err)
	require.Len(t, page.Items, 1)
	require.True(t, page.HasMore)
	require.Equal(t, "1", page.NextCursor)
}

func TestFaultInjection(t *testing.T) {
	for _, test := range []struct {
		fault connector.ErrKind
		name  string
	}{
		{connector.ErrAuthExpired, "auth_expired"},
		{connector.ErrRateLimited, "rate_limited"},
		{connector.ErrUnavailable, "500"},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := New(test.name).Fetch(context.Background(), connector.Credential{}, connector.FetchRequest{Resource: connector.ResourceProduct})
			var platformErr *connector.PlatformError
			require.ErrorAs(t, err, &platformErr)
			require.Equal(t, test.fault, platformErr.Kind)
		})
	}
}

func TestDriftFaultAddsField(t *testing.T) {
	page, err := New("drift").Fetch(context.Background(), connector.Credential{}, connector.FetchRequest{Resource: connector.ResourceProduct})
	require.NoError(t, err)
	require.Contains(t, string(page.Items[0].Payload), "drift_field")
}
