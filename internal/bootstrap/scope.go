package bootstrap

import (
	"sync"

	kernelscope "github.com/omwk/go-admin-api/internal/kernel/scope"
	identityscope "github.com/omwk/go-admin-api/internal/modules/identity/scope"
	shopscope "github.com/omwk/go-admin-api/internal/modules/shop/scope"
)

var scopeRegistry = sync.OnceValues(func() (*kernelscope.Registry, error) {
	return kernelscope.NewRegistry(identityscope.Declarations(), shopscope.Declarations(), kernelscope.Set{Queries: map[string]kernelscope.QueryDeclaration{
		"Schedule.ListEnabledJobs":                        {Policy: "bootstrap-only enabled schedule projection"},
		"Sync.ResolveTask":                                {Policy: "bootstrap-only verified task resolution"},
		"CredentialUse.WorkerCredential":                  {Policy: "worker-only active credential projection"},
		"CredentialUse.AuditCredentialUse":                {Policy: "worker-only system credential audit"},
		"Admin.EmitAuthorizationInvalidation":             {Policy: "transactional authorization invalidation outbox"},
		"PermissionRelease.EmitAuthorizationInvalidation": {Policy: "transactional authorization invalidation outbox"},
	}})
})
