package storage

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/omwk/go-admin-api/internal/kernel/crypto"
)

// Provider is an encrypted, platform-owned object storage configuration.
type Provider struct {
	ID            int64     `json:"id" gorm:"primaryKey"`
	TenantID      int64     `json:"-"`
	Name          string    `json:"name"`
	Endpoint      string    `json:"endpoint"`
	Port          int       `json:"port"`
	Bucket        string    `json:"bucket"`
	UseSSL        bool      `json:"use_ssl"`
	Region        string    `json:"region"`
	AccessKey     string    `json:"access_key"`
	SecretEnc     []byte    `json:"-"`
	SecretKey     string    `json:"secret_key" gorm:"-"`
	PublicBaseURL string    `json:"public_base_url"`
	IsDefault     bool      `json:"is_default"`
	Enabled       bool      `json:"enabled"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// TableName names the provider configuration table.
func (Provider) TableName() string { return "storage_provider" }

type secretEnvelope struct {
	Cipher  []byte `json:"cipher"`
	Nonce   []byte `json:"nonce"`
	Version int    `json:"version"`
}

func providerAAD(p Provider) string {
	return fmt.Sprintf("storage-provider:v1:%d:%d", p.TenantID, p.ID)
}

// SealSecret binds the secret envelope to the persistent provider identity.
func SealSecret(sealer crypto.Sealer, p Provider) ([]byte, error) {
	if sealer == nil {
		return nil, errors.New("storage encryption unavailable")
	}
	raw := []byte(p.SecretKey)
	defer clear(raw)
	cipher, nonce, version, err := sealer.Seal(raw, providerAAD(p))
	if err != nil {
		return nil, errors.New("storage encryption failed")
	}
	return json.Marshal(secretEnvelope{cipher, nonce, version})
}

// OpenSecret authenticates a stored envelope without exposing crypto errors.
func OpenSecret(sealer crypto.Sealer, p Provider) (string, error) {
	if sealer == nil {
		return "", errors.New("storage encryption unavailable")
	}
	var e secretEnvelope
	if json.Unmarshal(p.SecretEnc, &e) != nil {
		return "", errors.New("storage decryption failed")
	}
	raw, err := sealer.Open(e.Cipher, e.Nonce, providerAAD(p), e.Version)
	if err != nil {
		return "", errors.New("storage decryption failed")
	}
	defer clear(raw)
	return string(raw), nil
}

// NormalizeEndpoint returns the SDK authority and transport security setting.
func NormalizeEndpoint(endpoint string, port int, secure bool) (string, bool, error) {
	endpoint = strings.TrimSpace(endpoint)
	if port < 1 || port > 65535 || endpoint == "" {
		return "", false, errors.New("invalid storage endpoint")
	}
	raw := endpoint
	if !strings.Contains(raw, "://") {
		raw = "http://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" || u.User != nil || (u.Scheme != "http" && u.Scheme != "https") || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
		return "", false, errors.New("invalid storage endpoint")
	}
	if strings.Contains(endpoint, "://") {
		secure = u.Scheme == "https"
	}
	if u.Port() != "" {
		parsed, err := strconv.Atoi(u.Port())
		if err != nil || parsed < 1 || parsed > 65535 {
			return "", false, errors.New("invalid storage endpoint")
		}
		port = parsed
	}
	return net.JoinHostPort(u.Hostname(), strconv.Itoa(port)), secure, nil
}
