package bootstrap

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/omwk/go-admin-api/internal/kernel/scope"
	identityrepo "github.com/omwk/go-admin-api/internal/modules/identity/repo"
	schedulerepo "github.com/omwk/go-admin-api/internal/modules/schedule/repo"
	shoprepo "github.com/omwk/go-admin-api/internal/modules/shop/repo"
	syncrepo "github.com/omwk/go-admin-api/internal/modules/sync/repo"
	"github.com/stretchr/testify/require"
)

func TestOrchestrationRepositoryReflectionMatrix(t *testing.T) {
	assertRegisteredMethods(t, reflect.TypeOf((*schedulerepo.Store)(nil)).Elem(), []string{"ListEnabledJobs"})
	assertRegisteredMethods(t, reflect.TypeOf((*syncrepo.Store)(nil)).Elem(), []string{"ResolveTask"})
	assertRegisteredMethods(t, reflect.TypeOf((*shoprepo.CredentialUseStore)(nil)).Elem(), []string{"AuditCredentialUse", "WorkerCredential"})
	assertRegisteredMethods(t, reflect.TypeOf((*identityrepo.OutboxEmitter)(nil)).Elem(), []string{"EmitAuthorizationInvalidation"})
	assertRegisteredMethods(t, reflect.TypeOf((*identityrepo.AuthorizationInvalidationEmitter)(nil)).Elem(), []string{"EmitAuthorizationInvalidation"})
	require.Equal(t, []string{"ListEnabledJobs"}, scopeRegistryQueryNames("Schedule"))
	require.Equal(t, []string{"ResolveTask"}, scopeRegistryQueryNames("Sync"))
	require.Equal(t, []string{"AuditCredentialUse", "WorkerCredential"}, scopeRegistryQueryNames("CredentialUse"))
	require.Equal(t, []string{"EmitAuthorizationInvalidation"}, scopeRegistryQueryNames("PermissionRelease"))
}

func TestWorkAccessCannotCrossJSONOrMissingContext(t *testing.T) {
	a, err := scope.IssueWorkAccess(scope.BootstrapIssuer{}, 1, 2, 3, 4, "product", 5, 6, []string{"credential.use"})
	require.NoError(t, err)
	_, err = json.Marshal(a)
	require.Error(t, err)
	var decoded scope.WorkAccess
	require.Error(t, json.Unmarshal([]byte(`{"tenantID":1}`), &decoded))
	_, err = scope.WorkFrom(context.Background())
	require.Error(t, err)
	require.True(t, a.Allows("credential.use"))
	for _, resource := range []string{"listing", "listing_sku", "unknown"} {
		_, err := scope.IssueWorkAccess(scope.BootstrapIssuer{}, 1, 2, 3, 4, resource, 5, 6, []string{"credential.use"})
		require.Error(t, err, resource)
	}
	_, err = scope.IssueWorkAccess(scope.BootstrapIssuer{}, 1, 2, 3, 4, "category", 5, 6, []string{"credential.use"})
	require.NoError(t, err)
}
