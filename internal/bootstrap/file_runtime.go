package bootstrap

import (
	"context"

	"github.com/gin-gonic/gin"
	"github.com/omwk/go-admin-api/internal/infra/storage"
	"github.com/omwk/go-admin-api/internal/kernel/audit"
	"github.com/omwk/go-admin-api/internal/kernel/db"
	filemodule "github.com/omwk/go-admin-api/internal/modules/file"
	"github.com/omwk/go-admin-api/internal/modules/identity"
)

func registerFileRoutes(router *gin.Engine, auth *identity.AuthService, profiles *identity.ProfileService, database *db.DB, resolver *storage.Resolver) error {
	service, err := filemodule.NewResolvedFileService(newFileStore(database), fileResolver(resolver), newIdempotencyStore(database), func(ctx context.Context, e audit.Event) error { return RecordAudit(ctx, database, e) })
	if err != nil {
		return err
	}
	return filemodule.RegisterFileRoutes(router, auth, profiles, service)
}
func fileResolver(resolver *storage.Resolver) filemodule.Resolver {
	return func(ctx context.Context, id *int64, bucket string) (filemodule.Binding, error) {
		p, err := resolver.Resolve(ctx, id)
		if err != nil {
			return filemodule.Binding{}, err
		}
		return filemodule.Binding{Object: storage.FileObject{S3: p.Object.WithBucket(bucket)}, Bucket: p.Bucket, ProviderID: &p.ProviderID}, nil
	}
}
