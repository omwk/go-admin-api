package bootstrap

import (
	"sync"

	kernelscope "github.com/omwk/go-admin-api/internal/kernel/scope"
	identityscope "github.com/omwk/go-admin-api/internal/modules/identity/scope"
	shopscope "github.com/omwk/go-admin-api/internal/modules/shop/scope"
)

var scopeRegistry = sync.OnceValues(func() (*kernelscope.Registry, error) {
	return kernelscope.NewRegistry(identityscope.Declarations(), shopscope.Declarations())
})
