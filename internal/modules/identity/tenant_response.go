package identity

import (
	"context"
	"encoding/json"

	"github.com/omwk/go-admin-api/internal/modules/identity/repo"
)

type tenantSavedResponse struct {
	Cipher  []byte `json:"cipher"`
	Nonce   []byte `json:"nonce"`
	Version int    `json:"version"`
}

// execute protects the one-use credential in optional idempotency storage.
func (s *TenantService) execute(ctx context.Context, actor Overview, scope, key, hash string, provision bool, run func(context.Context) (repo.SavedResponse, error)) (repo.SavedResponse, error) {
	result, err := s.admin.Execute(ctx, actor, scope, key, hash, func(tx context.Context) (repo.SavedResponse, error) {
		response, err := run(tx)
		if err != nil || !provision || key == "" {
			return response, err
		}
		cipher, nonce, version, err := s.sealer.Seal(response.Data, scope)
		if err != nil {
			return repo.SavedResponse{}, err
		}
		response.Data, err = json.Marshal(tenantSavedResponse{Cipher: cipher, Nonce: nonce, Version: version})
		return response, err
	})
	if err != nil || !provision || key == "" {
		return result, err
	}
	var saved tenantSavedResponse
	if err := json.Unmarshal(result.Data, &saved); err != nil {
		return repo.SavedResponse{}, err
	}
	result.Data, err = s.sealer.Open(saved.Cipher, saved.Nonce, scope, saved.Version)
	return result, err
}
