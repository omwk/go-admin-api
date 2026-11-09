package scope

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

// REQ-SHP-008: refresh cannot borrow a sync job, resource, or a human all identity.
func TestCredentialRefreshAccessIsolation(t *testing.T) {
	ctx := context.Background()
	_, err := CredentialRefreshFrom(ctx)
	require.Error(t, err)
	human := WithAccess(ctx, Access{Tenant: 1, User: 1, Roles: []Role{{Kind: "all"}}})
	_, err = CredentialRefreshFrom(human)
	require.Error(t, err)
	work, err := IssueWorkAccess(BootstrapIssuer{}, 1, 2, 3, 4, "product", 5, 6, []string{"credential.refresh"})
	require.NoError(t, err)
	_, err = CredentialRefreshFrom(WithWorkAccess(ctx, work))
	require.Error(t, err)
	var forged CredentialRefreshAccess
	require.Error(t, json.Unmarshal([]byte(`{"tenant_id":1,"credential_id":2,"job_id":3,"resource":"product"}`), &forged))
	_, err = CredentialRefreshFrom(WithCredentialRefreshAccess(ctx, forged))
	require.Error(t, err)
}

func TestCredentialRefreshAccessPurposeAndGeneration(t *testing.T) {
	for _, purpose := range []string{"", "raw.save", "sync.page"} {
		_, err := IssueCredentialRefreshAccess(BootstrapIssuer{}, 1, 2, 3, 4, 5, 6, purpose)
		require.Error(t, err)
	}
	_, err := IssueCredentialRefreshAccess(BootstrapIssuer{}, 1, 2, 3, 4, 5, 0, "credential.refresh")
	require.Error(t, err)
	a, err := IssueCredentialRefreshAccess(BootstrapIssuer{}, 1, 2, 3, 4, 5, 6, "credential.refresh")
	require.NoError(t, err)
	verified, err := CredentialRefreshFrom(WithCredentialRefreshAccess(context.Background(), a))
	require.NoError(t, err)
	require.Equal(t, int64(1), verified.TenantID())
	require.Equal(t, int64(3), verified.CredentialID())
	require.Equal(t, int64(6), verified.Generation())
	_, err = json.Marshal(a)
	require.Error(t, err)
}
