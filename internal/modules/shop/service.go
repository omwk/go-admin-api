package shop

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/omwk/go-admin-api/internal/kernel/audit"
	"github.com/omwk/go-admin-api/internal/kernel/crypto"
	"github.com/omwk/go-admin-api/internal/kernel/errs"
	"github.com/omwk/go-admin-api/internal/kernel/idem"
	"github.com/omwk/go-admin-api/internal/modules/shop/model"
	"github.com/omwk/go-admin-api/internal/modules/shop/repo"
)

// Actor is the trusted identity projection needed by shop operations.
type Actor struct {
	TenantID, UserID, DepartmentID int64
}

// Authorizer attaches the verified identity and effective roles to a context.
type Authorizer func(context.Context, int64, int64) (context.Context, error)

// Service owns shop validation and uses only the shop persistence port.
type Service struct {
	store     repo.Store
	authorize Authorizer
	sealer    crypto.Sealer
	idem      idem.Store
	record    func(context.Context, audit.Event) error
}

// NewService constructs the shop service.
func NewService(store repo.Store, authorize Authorizer, sealers ...crypto.Sealer) (*Service, error) {
	if store == nil || authorize == nil {
		return nil, errors.New("shop store and authorizer are required")
	}
	var sealer crypto.Sealer
	if len(sealers) > 0 {
		sealer = sealers[0]
	}
	return &Service{store: store, authorize: authorize, sealer: sealer}, nil
}

// UseIdempotency attaches a transaction-bound result store and the audit
// recorder used by idempotent shop creation.
func (s *Service) UseIdempotency(store idem.Store, record func(context.Context, audit.Event) error) {
	s.idem, s.record = store, record
}

// CreateIdempotent executes or replays shop creation on the caller's
// transaction. The serializer runs before completion so a committed row is
// always the exact body later written to the client.
func (s *Service) CreateIdempotent(ctx context.Context, actor Actor, req CreateRequest, operation idem.Operation, serialize func(int, any) (idem.Result, error)) (idem.Result, error) {
	if s.idem == nil || s.record == nil || serialize == nil {
		return idem.Result{}, errors.New("shop idempotency dependencies are required")
	}
	ctx = context.WithoutCancel(ctx)
	ctx, err := s.authorize(ctx, actor.TenantID, actor.UserID)
	if err != nil {
		return idem.Result{}, err
	}
	owner, dept := actor.UserID, actor.DepartmentID
	if req.OwnerUserID != nil {
		owner = *req.OwnerUserID
	}
	if req.DepartmentID != nil {
		dept = *req.DepartmentID
	}
	transactional, ok := s.store.(repo.TransactionalStore)
	if !ok {
		return idem.Result{}, errors.New("shop transactional store is required")
	}
	var result idem.Result
	err = transactional.Transaction(ctx, func(tx context.Context) error {
		if err := s.store.ValidateAssignment(tx, actor.TenantID, owner, dept); err != nil {
			return err
		}
		claim, err := s.idem.Claim(tx, operation)
		if err != nil {
			return mapIdempotencyError(err)
		}
		if claim.Action == idem.Replay {
			result = claim.Result
			return nil
		}
		row := req.row(actor.TenantID, owner, dept)
		if err := s.store.CreateShop(tx, &row); err != nil {
			return err
		}
		created, err := s.store.Shop(tx, actor.TenantID, row.ID)
		if err != nil {
			return err
		}
		view := project(created)
		if s.record != nil {
			id := view.ID
			if err := s.record(tx, audit.Event{TenantID: actor.TenantID, ActorType: "user", ActorID: &actor.UserID,
				Action: "shop:create", ResourceType: "shop", ResourceID: fmt.Sprint(id), After: map[string]any{"id": id, "name": view.Name, "platform_id": view.PlatformID, "external_shop_id": view.ExternalShopID}, Result: "success"}); err != nil {
				return err
			}
		}
		result, err = serialize(201, view)
		if err != nil {
			return err
		}
		result, err = s.idem.Complete(tx, operation, result)
		return err
	})
	return result, err
}

func mapIdempotencyError(err error) error {
	if errors.Is(err, idem.ErrMismatch) {
		return errs.New(errs.ErrIdempotencyMismatch, 409, "幂等键请求内容不一致")
	}
	if errors.Is(err, idem.ErrProcessing) {
		return errs.New(errs.ErrConflict, 409, "幂等请求正在处理")
	}
	return err
}

// PlatformView is the safe global platform directory projection.
type PlatformView struct {
	ID           int64           `json:"id"`
	Code         string          `json:"code"`
	Name         string          `json:"name"`
	AuthType     string          `json:"auth_type"`
	Capabilities json.RawMessage `json:"capabilities"`
	ConfigSchema json.RawMessage `json:"config_schema"`
	Status       string          `json:"status"`
}

// View is the safe shop API projection.
type View struct {
	ID             int64           `json:"id"`
	TenantID       int64           `json:"tenant_id"`
	PlatformID     int64           `json:"platform_id"`
	PlatformName   string          `json:"platform_name"`
	Name           string          `json:"name"`
	ExternalShopID string          `json:"external_shop_id"`
	Region         string          `json:"region"`
	Timezone       string          `json:"timezone"`
	Currency       string          `json:"currency"`
	OwnerUserID    int64           `json:"owner_user_id"`
	DepartmentID   int64           `json:"dept_id"`
	Status         string          `json:"status"`
	Settings       json.RawMessage `json:"settings"`
	LastSyncAt     any             `json:"last_sync_at"`
	Version        int             `json:"version"`
	CreatedAt      string          `json:"created_at"`
	UpdatedAt      string          `json:"updated_at"`
}

// Page is the standard shop list response.
type Page struct {
	List     []View `json:"list"`
	Total    int64  `json:"total"`
	Page     int    `json:"page"`
	PageSize int    `json:"page_size"`
}

// ListPlatforms reads the global seeded platform directory.
func (s *Service) ListPlatforms(ctx context.Context) ([]PlatformView, error) {
	rows, err := s.store.Platforms(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]PlatformView, 0, len(rows))
	for _, row := range rows {
		result = append(result, PlatformView{ID: row.ID, Code: row.Code, Name: row.Name, AuthType: row.AuthType, Capabilities: row.Capabilities, ConfigSchema: row.ConfigSchema, Status: row.Status})
	}
	return result, nil
}

// List returns only shops in the actor's effective data scope.
func (s *Service) List(ctx context.Context, actor Actor, page repo.Page) (Page, error) {
	ctx, err := s.authorize(ctx, actor.TenantID, actor.UserID)
	if err != nil {
		return Page{}, err
	}
	rows, total, err := s.store.Shops(ctx, actor.TenantID, page)
	if err != nil {
		return Page{}, err
	}
	result := Page{Total: total, Page: page.Number, PageSize: page.Size, List: make([]View, 0, len(rows))}
	for _, row := range rows {
		result.List = append(result.List, project(row))
	}
	return result, nil
}

// Get returns one scoped shop or the indistinguishable not-found error.
func (s *Service) Get(ctx context.Context, actor Actor, id int64) (View, error) {
	ctx, err := s.authorize(ctx, actor.TenantID, actor.UserID)
	if err != nil {
		return View{}, err
	}
	row, err := s.store.Shop(ctx, actor.TenantID, id)
	if err != nil {
		return View{}, err
	}
	return project(row), nil
}

// Create creates a shop with identity tenant and validated ownership.
func (s *Service) Create(ctx context.Context, actor Actor, req CreateRequest) (View, error) {
	ctx, err := s.authorize(ctx, actor.TenantID, actor.UserID)
	if err != nil {
		return View{}, err
	}
	owner, dept := actor.UserID, actor.DepartmentID
	if req.OwnerUserID != nil {
		owner = *req.OwnerUserID
	}
	if req.DepartmentID != nil {
		dept = *req.DepartmentID
	}
	if err := s.store.ValidateAssignment(ctx, actor.TenantID, owner, dept); err != nil {
		return View{}, err
	}
	row := req.row(actor.TenantID, owner, dept)
	if err := s.store.CreateShop(ctx, &row); err != nil {
		return View{}, err
	}
	created, err := s.store.Shop(ctx, actor.TenantID, row.ID)
	if err != nil {
		return View{}, err
	}
	return project(created), nil
}

// Update applies a version-checked full update and validates both assignments.
func (s *Service) Update(ctx context.Context, actor Actor, id int64, req UpdateRequest) (View, error) {
	if req.Version == nil {
		return View{}, errs.New(errs.ErrInvalidParam, 400, "参数校验失败")
	}
	ctx, err := s.authorize(ctx, actor.TenantID, actor.UserID)
	if err != nil {
		return View{}, err
	}
	old, err := s.store.Shop(ctx, actor.TenantID, id)
	if err != nil {
		return View{}, err
	}
	if *req.Version != old.Version {
		return View{}, errs.New(errs.ErrVersionConflict, 409, "店铺版本冲突")
	}
	owner, dept := old.OwnerUserID, old.DepartmentID
	if req.OwnerUserID != nil {
		owner = *req.OwnerUserID
	}
	if req.DepartmentID != nil {
		dept = *req.DepartmentID
	}
	if err := s.store.ValidateAssignment(ctx, actor.TenantID, owner, dept); err != nil {
		return View{}, err
	}
	values := req.values(owner, dept)
	if err := s.store.UpdateShop(ctx, actor.TenantID, id, *req.Version, values); err != nil {
		return View{}, err
	}
	updated, err := s.store.Shop(ctx, actor.TenantID, id)
	if err != nil {
		return View{}, err
	}
	return project(updated), nil
}

// Delete soft-deletes a scoped shop.
func (s *Service) Delete(ctx context.Context, actor Actor, id int64) error {
	ctx, err := s.authorize(ctx, actor.TenantID, actor.UserID)
	if err != nil {
		return err
	}
	return s.store.DeleteShop(ctx, actor.TenantID, id)
}

// CreateRequest accepts only client-editable shop fields; tenant is identity-derived.
type CreateRequest struct {
	PlatformID     int64           `json:"platform_id" binding:"required,gt=0"`
	Name           string          `json:"name" binding:"required,max=256"`
	ExternalShopID string          `json:"external_shop_id" binding:"required,max=256"`
	Region         string          `json:"region" binding:"max=256"`
	Timezone       string          `json:"timezone" binding:"required,max=128"`
	Currency       string          `json:"currency" binding:"required,len=3"`
	OwnerUserID    *int64          `json:"owner_user_id" binding:"omitempty,gt=0"`
	DepartmentID   *int64          `json:"dept_id" binding:"omitempty,gt=0"`
	Status         string          `json:"status" binding:"omitempty,oneof=active paused disabled"`
	Settings       json.RawMessage `json:"settings"`
}

func (r CreateRequest) row(tenant, owner, dept int64) model.Shop {
	status := r.Status
	if status == "" {
		status = "active"
	}
	settings := r.Settings
	if len(settings) == 0 {
		settings = json.RawMessage("{}")
	}
	return model.Shop{TenantID: tenant, PlatformID: r.PlatformID, Name: r.Name, ExternalShopID: r.ExternalShopID, Region: r.Region, Timezone: r.Timezone, Currency: strings.ToUpper(r.Currency), OwnerUserID: owner, DepartmentID: dept, Status: status, Settings: settings}
}

// UpdateRequest contains mutable fields and the required optimistic-lock version.
type UpdateRequest struct {
	PlatformID     *int64          `json:"platform_id" binding:"omitempty,gt=0"`
	Name           *string         `json:"name" binding:"omitempty,max=256"`
	ExternalShopID *string         `json:"external_shop_id" binding:"omitempty,max=256"`
	Region         *string         `json:"region" binding:"omitempty,max=256"`
	Timezone       *string         `json:"timezone" binding:"omitempty,max=128"`
	Currency       *string         `json:"currency" binding:"omitempty,len=3"`
	OwnerUserID    *int64          `json:"owner_user_id" binding:"omitempty,gt=0"`
	DepartmentID   *int64          `json:"dept_id" binding:"omitempty,gt=0"`
	Status         *string         `json:"status" binding:"omitempty,oneof=active paused disabled"`
	Settings       json.RawMessage `json:"settings"`
	Version        *int            `json:"version" binding:"required,gte=0"`
}

func (r UpdateRequest) values(owner, dept int64) map[string]any {
	values := map[string]any{"owner_user_id": owner, "dept_id": dept, "version": *r.Version + 1}
	if r.PlatformID != nil {
		values["platform_id"] = *r.PlatformID
	}
	if r.Name != nil {
		values["name"] = *r.Name
	}
	if r.ExternalShopID != nil {
		values["external_shop_id"] = *r.ExternalShopID
	}
	if r.Region != nil {
		values["region"] = *r.Region
	}
	if r.Timezone != nil {
		values["timezone"] = *r.Timezone
	}
	if r.Currency != nil {
		values["currency"] = strings.ToUpper(*r.Currency)
	}
	if r.Status != nil {
		values["status"] = *r.Status
	}
	if len(r.Settings) > 0 {
		values["settings"] = r.Settings
	}
	return values
}

func project(row repo.ShopRow) View {
	return View{ID: row.ID, TenantID: row.TenantID, PlatformID: row.PlatformID, PlatformName: row.PlatformName, Name: row.Name, ExternalShopID: row.ExternalShopID, Region: row.Region, Timezone: row.Timezone, Currency: row.Currency, OwnerUserID: row.OwnerUserID, DepartmentID: row.DepartmentID, Status: row.Status, Settings: row.Settings, LastSyncAt: row.LastSyncAt, Version: row.Version, CreatedAt: row.CreatedAt.UTC().Format("2006-01-02T15:04:05.999999999Z07:00"), UpdatedAt: row.UpdatedAt.UTC().Format("2006-01-02T15:04:05.999999999Z07:00")}
}

// NotFound returns the anti-enumeration shop error.
func NotFound() error { return errs.New(errs.ErrShopNotFound, 404, "店铺不存在") }
