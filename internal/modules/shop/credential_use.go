package shop

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/omwk/go-admin-api/internal/kernel/logger"
	"github.com/omwk/go-admin-api/internal/kernel/scope"
	"github.com/omwk/go-admin-api/internal/modules/shop/credential"
	"github.com/omwk/go-admin-api/internal/modules/shop/model"
	"github.com/omwk/go-admin-api/internal/modules/shop/repo"
)

// CredentialDTO is the neutral credential contract consumed by platform ports.
// It contains decrypted values only in memory and never includes ciphertext.
type CredentialDTO struct {
	CredentialID int64                      `json:"credential_id"`
	ShopID       int64                      `json:"shop_id"`
	Kind         string                     `json:"kind"`
	Values       map[string]json.RawMessage `json:"values"`
}

// CredentialUsePort is the worker-only credential access boundary.
type CredentialUsePort interface {
	UseCredential(context.Context, scope.WorkAccess) (CredentialDTO, error)
}

// UseCredential audits the system access before decrypting the active version.
func (s *Service) UseCredential(ctx context.Context, access scope.WorkAccess) (CredentialDTO, error) {
	verified, err := scope.WorkFrom(scope.WithWorkAccess(ctx, access))
	if err != nil {
		return CredentialDTO{}, err
	}
	if !verified.Allows("credential.use") {
		return CredentialDTO{}, errors.New("worker credential method denied")
	}
	store, ok := s.store.(repo.CredentialUseStore)
	if !ok || s.sealer == nil {
		return CredentialDTO{}, errors.New("worker credential dependencies are required")
	}
	row, err := store.WorkerCredential(ctx, verified.TenantID(), verified.ShopID())
	if err != nil {
		return CredentialDTO{}, err
	}
	detail, err := json.Marshal(map[string]any{"job_id": verified.JobID(), "run_id": verified.RunID(), "metadata_only": false})
	if err != nil {
		return CredentialDTO{}, errors.New("credential audit detail is invalid")
	}
	if err := store.AuditCredentialUse(ctx, verified.TenantID(), row.Credential.ID, model.CredentialAudit{
		// The existing audit contract records credential consumption as a read;
		// "use" is not an allowed database action.
		Action: "read", ActorType: "system", Result: "success", Detail: detail, TraceID: logger.TraceID(ctx),
	}); err != nil {
		return CredentialDTO{}, fmt.Errorf("credential use audit: %w", err)
	}
	plain, err := credential.OpenVersion(s.sealer, verified.TenantID(), row.Shop, row.Credential, row.Version)
	if err != nil {
		return CredentialDTO{}, err
	}
	defer clearBytes(plain)
	var values map[string]json.RawMessage
	if err := json.Unmarshal(plain, &values); err != nil || len(values) == 0 {
		return CredentialDTO{}, errors.New("credential plaintext is invalid")
	}
	return CredentialDTO{CredentialID: row.Credential.ID, ShopID: row.Shop.ID, Kind: row.Credential.Kind, Values: values}, nil
}

func clearBytes(value []byte) {
	for i := range value {
		value[i] = 0
	}
}

var _ CredentialUsePort = (*Service)(nil)
