package shop

import (
	"context"
	"encoding/json"

	"github.com/omwk/go-admin-api/internal/kernel/errs"
	"github.com/omwk/go-admin-api/internal/kernel/logger"
	"github.com/omwk/go-admin-api/internal/modules/shop/model"
	"github.com/omwk/go-admin-api/internal/modules/shop/repo"
)

func (s *Service) credentialOperation(ctx context.Context, actor Actor, fn func(context.Context, repo.CredentialStore) error) error {
	// Identity loading may use infrastructure; complete it before opening the business transaction.
	ctx, err := s.authorize(ctx, actor.TenantID, actor.UserID)
	if err != nil {
		return err
	}
	store, ok := s.store.(repo.CredentialStore)
	if !ok {
		return errs.New(errs.ErrInternal, 500, "凭据存储不可用")
	}
	return store.CredentialTransaction(ctx, func(tx context.Context) error { return fn(tx, store) })
}

// ListCredentials reads and audits only the requested page of metadata under the live parent shop.
func (s *Service) ListCredentials(ctx context.Context, actor Actor, shopID int64, page repo.Page) (CredentialPage, error) {
	result := CredentialPage{List: []CredentialView{}, Page: page.Number, PageSize: page.Size}
	err := s.credentialOperation(ctx, actor, func(tx context.Context, store repo.CredentialStore) error {
		rows, total, err := store.Credentials(tx, actor.TenantID, shopID, page, credentialAudit(tx, actor, "read"))
		if err != nil {
			return err
		}
		result.Total = total
		for _, row := range rows {
			view, err := projectCredential(row)
			if err != nil {
				return err
			}
			result.List = append(result.List, view)
		}
		return nil
	})
	if err != nil {
		return CredentialPage{}, err
	}
	return result, nil
}

// GetCredential is the internal metadata detail entry; it uses the same scope and audit as lists.
func (s *Service) GetCredential(ctx context.Context, actor Actor, id int64) (CredentialView, error) {
	var result CredentialView
	err := s.credentialOperation(ctx, actor, func(tx context.Context, store repo.CredentialStore) error {
		row, err := store.Credential(tx, actor.TenantID, id, credentialAudit(tx, actor, "read"))
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

// DisableCredential commits the state transition and audit together.
func (s *Service) DisableCredential(ctx context.Context, actor Actor, id int64) (CredentialView, error) {
	var result CredentialView
	err := s.credentialOperation(ctx, actor, func(tx context.Context, store repo.CredentialStore) error {
		row, err := store.DisableCredential(tx, actor.TenantID, id, credentialAudit(tx, actor, "disable"))
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

// CheckCredential commits a failure audit, then returns ErrUnsupported outside the transaction.
func (s *Service) CheckCredential(ctx context.Context, actor Actor, id int64) error {
	err := s.credentialOperation(ctx, actor, func(tx context.Context, store repo.CredentialStore) error {
		return store.CheckCredential(tx, actor.TenantID, id, credentialAudit(tx, actor, "check"))
	})
	if err != nil {
		return err
	}
	return ErrUnsupported
}

// CredentialAudits reads a page of safe history and appends a metadata-only read event.
func (s *Service) CredentialAudits(ctx context.Context, actor Actor, id int64, page repo.Page) (CredentialAuditPage, error) {
	result := CredentialAuditPage{List: []CredentialAuditView{}, Page: page.Number, PageSize: page.Size}
	err := s.credentialOperation(ctx, actor, func(tx context.Context, store repo.CredentialStore) error {
		rows, total, err := store.CredentialAudits(tx, actor.TenantID, id, page, credentialAudit(tx, actor, "read"))
		if err != nil {
			return err
		}
		result.Total = total
		for _, row := range rows {
			var detail CredentialAuditDetail
			if err := json.Unmarshal(row.Detail, &detail); err != nil {
				return errs.New(errs.ErrInternal, 500, "凭据审计元数据无效")
			}
			result.List = append(result.List, CredentialAuditView{ID: row.ID, Action: row.Action, Result: row.Result,
				ErrorCode: row.ErrorCode, Detail: detail, CreatedAt: row.CreatedAt.UTC()})
		}
		return nil
	})
	if err != nil {
		return CredentialAuditPage{}, err
	}
	return result, nil
}

func credentialAudit(ctx context.Context, actor Actor, action string) model.CredentialAudit {
	id := actor.UserID
	audit := model.CredentialAudit{TenantID: actor.TenantID, ActorType: "user", ActorID: &id, Action: action,
		Result: "success", Detail: json.RawMessage(`{"metadata_only":false}`), TraceID: logger.TraceID(ctx)}
	if action == "read" {
		audit.Detail = json.RawMessage(`{"metadata_only":true}`)
	}
	if action == "check" {
		audit.Result, audit.ErrorCode = "failure", "30401"
	}
	if audit.TraceID == "" {
		audit.TraceID = "credential-operation"
	}
	return audit
}
