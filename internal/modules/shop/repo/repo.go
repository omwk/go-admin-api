// Package repo defines the shop persistence boundary.
package repo

import (
	"context"
	"github.com/omwk/go-admin-api/internal/modules/shop/model"
)

// Page is a validated shop list request.
type Page struct {
	Number int
	Size   int
	Sort   string
}

// ShopRow combines a scoped shop with its display-only platform name.
type ShopRow struct {
	model.Shop
	PlatformName string `gorm:"column:platform_name"`
}

// Store is the persistence port for the T2.3 shop CRUD surface.
type Store interface {
	Platforms(context.Context) ([]model.Platform, error)
	Shops(context.Context, int64, Page) ([]ShopRow, int64, error)
	Shop(context.Context, int64, int64) (ShopRow, error)
	ValidateAssignment(context.Context, int64, int64, int64) error
	CreateShop(context.Context, *model.Shop) error
	UpdateShop(context.Context, int64, int64, int, map[string]any) error
	DeleteShop(context.Context, int64, int64) error
}

// TransactionalStore runs a callback on the same transaction used by a shop write.
type TransactionalStore interface {
	Transaction(context.Context, func(context.Context) error) error
}

// CredentialRow contains only public credential metadata and the current version projection.
type CredentialRow struct {
	model.Credential
	VersionNo   int    `gorm:"column:version_no"`
	Fingerprint string `gorm:"column:fingerprint"`
}

// CredentialStore is the transactional persistence boundary for credential operations.
// The version builder is called after the database assigns the credential id.
type CredentialStore interface {
	CredentialTransaction(context.Context, func(context.Context) error) error
	CreateCredential(context.Context, int64, int64, *model.Credential, func(int64, int) (*model.CredentialVersion, error), model.CredentialAudit) (CredentialRow, error)
	Credentials(context.Context, int64, int64, Page, model.CredentialAudit) ([]CredentialRow, int64, error)
	Credential(context.Context, int64, int64, model.CredentialAudit) (CredentialRow, error)
	RotateCredential(context.Context, int64, int64, func(model.Credential, int) (*model.CredentialVersion, model.Credential, error), model.CredentialAudit) (CredentialRow, error)
	DisableCredential(context.Context, int64, int64, model.CredentialAudit) (CredentialRow, error)
	CheckCredential(context.Context, int64, int64, model.CredentialAudit) error
	CredentialAudits(context.Context, int64, int64, Page, model.CredentialAudit) ([]model.CredentialAudit, int64, error)
}

// CredentialScopeStore validates a live parent without creating an audit row;
// it is used before replay so authorization and data scope are rechecked.
type CredentialScopeStore interface {
	ValidateCredential(context.Context, int64, int64) error
}

// FileStore is the persistence boundary for file metadata and tenant checks.
type FileStore interface {
	FileTransaction(context.Context, func(context.Context) error) error
	FindFileByHash(context.Context, int64, string) (model.FileObject, error)
	CreateFile(context.Context, *model.FileObject) error
	File(context.Context, int64, int64) (model.FileObject, error)
}

// FileHashLocker serializes same-tenant content registration inside the file transaction.
type FileHashLocker interface {
	LockFileHash(context.Context, int64, string) error
}
