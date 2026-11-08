package refresh

import (
	"context"
	"errors"
	"time"

	"github.com/omwk/go-admin-api/internal/modules/shop/model"
)

// Target identifies one observed input version; duplicate triggers retain this identity.
// M7b must authorize these identifiers before entering this internal core.
type Target struct{ TenantID, CredentialID, InputVersionID int64 }

// Snapshot contains trusted relationships loaded under shop then credential locks.
type Snapshot struct {
	Shop       model.Shop
	Credential model.Credential
	Version    model.CredentialVersion
}

// Store methods except Transaction must run in its short transaction callback.
// Every lookup and mutation is tenant- and credential-qualified.
type Store interface {
	Transaction(context.Context, func(context.Context) error) error
	Lock(context.Context, Target) (Snapshot, error)
	FindRun(context.Context, Target, int64) (*model.RefreshRun, error)
	InsertRun(context.Context, *model.RefreshRun) error
	SaveRun(context.Context, model.RefreshRun) error
	SaveCredential(context.Context, model.Credential) error
	InsertStage(context.Context, *model.RefreshStage) error
	Stage(context.Context, model.RefreshRun) (model.RefreshStage, error)
	PurgeStage(context.Context, model.RefreshStage, time.Time) error
	PublishVersion(context.Context, *model.CredentialVersion, int64, time.Time) error
	Audit(context.Context, model.RefreshRun, string, string, time.Time) error
	Reconcile(context.Context, model.RefreshRun, time.Time) error
	Resolve(context.Context, model.RefreshRun, time.Time) error
}

// Internal errors contain no upstream data or credential material.
var (
	ErrIneligible    = errors.New("credential refresh is not eligible")
	ErrPersistence   = errors.New("credential refresh persistence failed")
	ErrResultUnknown = errors.New("credential refresh result requires reconciliation")
)
