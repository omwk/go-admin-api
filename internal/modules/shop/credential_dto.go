package shop

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/omwk/go-admin-api/internal/kernel/errs"
	"github.com/omwk/go-admin-api/internal/modules/shop/repo"
)

// ErrUnsupported is the check result until a real platform adapter is available.
var ErrUnsupported = errs.New(30401, 400, "暂不支持该操作")

// CredentialExtra contains only the public metadata explicitly approved by M2.
type CredentialExtra struct {
	Scope string `json:"scope,omitempty"`
}

// CredentialView is the complete public projection; secret storage models are never serialized.
type CredentialView struct {
	ID            int64           `json:"id"`
	Kind          string          `json:"kind"`
	Label         string          `json:"label"`
	Status        string          `json:"status"`
	Fingerprint   string          `json:"fingerprint"`
	ExpiresAt     *time.Time      `json:"expires_at"`
	LastCheckedAt *time.Time      `json:"last_checked_at"`
	VersionNo     int             `json:"version_no"`
	Extra         CredentialExtra `json:"extra"`
}

// CredentialAuditView exposes only audit metadata, never error chains or encrypted data.
type CredentialAuditView struct {
	ID        int64                 `json:"id"`
	Action    string                `json:"action"`
	Result    string                `json:"result"`
	ErrorCode string                `json:"error_code"`
	Detail    CredentialAuditDetail `json:"detail"`
	CreatedAt time.Time             `json:"created_at"`
}

// CredentialAuditDetail is the fixed audit allowlist.
type CredentialAuditDetail struct {
	MetadataOnly bool `json:"metadata_only"`
}

// CredentialCreateRequest separates encrypted values from explicitly public metadata.
type CredentialCreateRequest struct {
	Kind            string                     `json:"kind" binding:"required,oneof=oauth2 api_key cookie session"`
	Label           string                     `json:"label" binding:"max=256"`
	Values          map[string]json.RawMessage `json:"values" binding:"required"`
	ExpiresAt       *time.Time                 `json:"expires_at"`
	RefreshStrategy string                     `json:"refresh_strategy" binding:"omitempty,oneof=auto manual none"`
	Extra           json.RawMessage            `json:"extra"`
}

// CredentialRotateRequest supplies a new immutable version and optional metadata changes.
type CredentialRotateRequest struct {
	Values          map[string]json.RawMessage `json:"values" binding:"required"`
	Label           *string                    `json:"label" binding:"omitempty,max=256"`
	ExpiresAt       *time.Time                 `json:"expires_at"`
	RefreshStrategy *string                    `json:"refresh_strategy" binding:"omitempty,oneof=auto manual none"`
	Extra           json.RawMessage            `json:"extra"`
}

// CredentialPage follows the standard pagination contract.
type CredentialPage struct {
	List     []CredentialView `json:"list"`
	Total    int64            `json:"total"`
	Page     int              `json:"page"`
	PageSize int              `json:"page_size"`
}

// CredentialAuditPage follows the standard pagination contract.
type CredentialAuditPage struct {
	List     []CredentialAuditView `json:"list"`
	Total    int64                 `json:"total"`
	Page     int                   `json:"page"`
	PageSize int                   `json:"page_size"`
}

func projectCredential(row repo.CredentialRow) (CredentialView, error) {
	var extra CredentialExtra
	if err := json.Unmarshal(row.Extra, &extra); err != nil {
		return CredentialView{}, errs.New(errs.ErrInternal, 500, "凭据元数据无效")
	}
	return CredentialView{ID: row.ID, Kind: row.Kind, Label: row.Label, Status: row.Status, Fingerprint: row.Fingerprint,
		ExpiresAt: utcTime(row.ExpiresAt), LastCheckedAt: utcTime(row.LastCheckedAt), VersionNo: row.VersionNo, Extra: extra}, nil
}

func publicExtra(raw json.RawMessage) (json.RawMessage, error) {
	if len(raw) == 0 {
		return json.RawMessage("{}"), nil
	}
	var input map[string]json.RawMessage
	if err := json.Unmarshal(raw, &input); err != nil || input == nil {
		return nil, invalidShop()
	}
	for key, value := range input {
		var scope string
		if key != "scope" || string(value) == "null" || json.Unmarshal(value, &scope) != nil || len(scope) > 256 {
			return nil, invalidShop()
		}
	}
	encoded, err := json.Marshal(input)
	if err != nil {
		return nil, invalidShop()
	}
	return encoded, nil
}

func credentialPlaintext(values map[string]json.RawMessage) ([]byte, error) {
	if len(values) == 0 {
		return nil, invalidShop()
	}
	for key, value := range values {
		if strings.TrimSpace(key) == "" || strings.ContainsAny(key, "\r\n") || !json.Valid(value) || string(value) == "null" {
			return nil, invalidShop()
		}
	}
	encoded, err := json.Marshal(values)
	if err != nil {
		return nil, invalidShop()
	}
	return encoded, nil
}

func utcTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	utc := value.UTC()
	return &utc
}
