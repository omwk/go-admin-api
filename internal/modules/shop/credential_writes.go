package shop

import (
	"context"
	"time"

	"github.com/omwk/go-admin-api/internal/kernel/crypto"
	"github.com/omwk/go-admin-api/internal/kernel/errs"
	"github.com/omwk/go-admin-api/internal/kernel/idem"
	"github.com/omwk/go-admin-api/internal/modules/shop/model"
	"github.com/omwk/go-admin-api/internal/modules/shop/repo"
)

// CreateCredential seals values only after the transaction assigns the credential id.
func (s *Service) CreateCredential(ctx context.Context, actor Actor, shopID int64, req CredentialCreateRequest) (CredentialView, error) {
	plaintext, err := credentialPlaintext(req.Values)
	if err != nil {
		return CredentialView{}, err
	}
	defer clear(plaintext)
	extra, err := publicExtra(req.Extra)
	if err != nil {
		return CredentialView{}, err
	}
	strategy := req.RefreshStrategy
	if strategy == "" {
		strategy = "none"
	}
	row := model.Credential{TenantID: actor.TenantID, ShopID: shopID, Kind: req.Kind, Label: req.Label, Status: "active",
		ExpiresAt: utcTime(req.ExpiresAt), RefreshStrategy: strategy, Extra: extra}
	var result CredentialView
	err = s.credentialOperation(ctx, actor, func(tx context.Context, store repo.CredentialStore) error {
		created, err := store.CreateCredential(tx, actor.TenantID, shopID, &row, func(id int64, number int) (*model.CredentialVersion, error) {
			return s.sealVersion(plaintext, row, id, number, actor.UserID, "initial")
		}, credentialAudit(tx, actor, "create"))
		if err != nil {
			return err
		}
		result, err = projectCredential(created)
		return err
	})
	if err != nil {
		return CredentialView{}, err
	}
	return result, nil
}

// CreateCredentialIdempotent executes or replays credential creation while
// keeping the serialized HTTP response in the same transaction.
func (s *Service) CreateCredentialIdempotent(ctx context.Context, actor Actor, shopID int64, req CredentialCreateRequest, operation idem.Operation, serialize func(int, any) (idem.Result, error)) (idem.Result, error) {
	plaintext, err := credentialPlaintext(req.Values)
	if err != nil {
		return idem.Result{}, err
	}
	defer clear(plaintext)
	extra, err := publicExtra(req.Extra)
	if err != nil {
		return idem.Result{}, err
	}
	strategy := req.RefreshStrategy
	if strategy == "" {
		strategy = "none"
	}
	row := model.Credential{TenantID: actor.TenantID, ShopID: shopID, Kind: req.Kind, Label: req.Label, Status: "active",
		ExpiresAt: utcTime(req.ExpiresAt), RefreshStrategy: strategy, Extra: extra}
	return s.createCredentialIdempotent(ctx, actor, shopID, row, plaintext, operation, serialize)
}

func (s *Service) createCredentialIdempotent(ctx context.Context, actor Actor, shopID int64, row model.Credential, plaintext []byte, operation idem.Operation, serialize func(int, any) (idem.Result, error)) (idem.Result, error) {
	if s.idem == nil || serialize == nil {
		return idem.Result{}, errs.New(errs.ErrInternal, 500, "凭据幂等不可用")
	}
	ctx = context.WithoutCancel(ctx)
	ctx, err := s.authorize(ctx, actor.TenantID, actor.UserID)
	if err != nil {
		return idem.Result{}, err
	}
	store, ok := s.store.(repo.CredentialStore)
	if !ok {
		return idem.Result{}, errs.New(errs.ErrInternal, 500, "凭据存储不可用")
	}
	var result idem.Result
	err = store.CredentialTransaction(ctx, func(tx context.Context) error {
		if scoped, ok := s.store.(repo.CredentialScopeStore); ok {
			if err := scoped.ValidateCredential(tx, actor.TenantID, shopID); err != nil {
				return err
			}
		}
		claim, err := s.idem.Claim(tx, operation)
		if err != nil {
			return mapIdempotencyError(err)
		}
		if claim.Action == idem.Replay {
			result = claim.Result
			return nil
		}
		created, err := store.CreateCredential(tx, actor.TenantID, shopID, &row, func(id int64, number int) (*model.CredentialVersion, error) {
			return s.sealVersion(plaintext, row, id, number, actor.UserID, "initial")
		}, credentialAudit(tx, actor, "create"))
		if err != nil {
			return err
		}
		view, err := projectCredential(created)
		if err != nil {
			return err
		}
		result, err = serialize(201, view)
		if err != nil {
			return err
		}
		result, err = s.idem.Complete(tx, operation, result)
		return err
	})
	if err != nil {
		return idem.Result{}, err
	}
	return result, nil
}

// RotateCredential creates a successor while the adapter holds shop then credential locks.
func (s *Service) RotateCredential(ctx context.Context, actor Actor, id int64, req CredentialRotateRequest) (CredentialView, error) {
	plaintext, err := credentialPlaintext(req.Values)
	if err != nil {
		return CredentialView{}, err
	}
	defer clear(plaintext)
	extra, err := publicExtra(req.Extra)
	if err != nil {
		return CredentialView{}, err
	}
	var result CredentialView
	err = s.credentialOperation(ctx, actor, func(tx context.Context, store repo.CredentialStore) error {
		row, err := store.RotateCredential(tx, actor.TenantID, id, func(current model.Credential, number int) (*model.CredentialVersion, model.Credential, error) {
			current.ExpiresAt = utcTime(req.ExpiresAt)
			if len(req.Extra) != 0 {
				current.Extra = extra
			}
			if req.Label != nil {
				current.Label = *req.Label
			}
			if req.RefreshStrategy != nil {
				current.RefreshStrategy = *req.RefreshStrategy
			}
			current.Status = "active"
			version, err := s.sealVersion(plaintext, current, id, number, actor.UserID, "manual")
			return version, current, err
		}, credentialAudit(tx, actor, "rotate"))
		if err != nil {
			return err
		}
		result, err = projectCredential(row)
		return err
	})
	if err != nil {
		return CredentialView{}, err
	}
	return result, nil
}

func (s *Service) sealVersion(plaintext []byte, row model.Credential, id int64, number int, actorID int64, reason string) (*model.CredentialVersion, error) {
	if s.sealer == nil {
		return nil, errs.New(errs.ErrInternal, 500, "凭据加密不可用")
	}
	aad, err := (crypto.CredentialAAD{TenantID: row.TenantID, ShopID: row.ShopID, CredentialID: id, Kind: row.Kind, VersionNo: number}).Encode()
	if err != nil {
		return nil, invalidShop()
	}
	ciphertext, nonce, keyVersion, err := s.sealer.Seal(plaintext, aad)
	if err != nil {
		return nil, errs.New(errs.ErrInternal, 500, "凭据加密失败")
	}
	return &model.CredentialVersion{TenantID: row.TenantID, CredentialID: id, VersionNo: number, CipherText: ciphertext,
		Nonce: nonce, KeyVersion: keyVersion, AAD: aad, Fingerprint: crypto.Fingerprint(plaintext), ExpiresAt: row.ExpiresAt,
		CreatedReason: reason, CreatedBy: actorID, IssuedAt: time.Now().UTC()}, nil
}
