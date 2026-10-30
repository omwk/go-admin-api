package identity

import (
	"context"
	"errors"
	"time"

	"github.com/omwk/go-admin-api/internal/kernel/audit"
	"github.com/omwk/go-admin-api/internal/modules/identity/jwt"
	"github.com/omwk/go-admin-api/internal/modules/identity/model"
	"github.com/omwk/go-admin-api/internal/modules/identity/pwd"
	"github.com/omwk/go-admin-api/internal/modules/identity/refresh"
	"github.com/omwk/go-admin-api/internal/modules/identity/repo"
)

// Revocations is the Redis port; Mark must atomically retain the later expiration.
type Revocations interface {
	Ping(context.Context) error
	AllowLogin(context.Context, string) (bool, error)
	Revoked(context.Context, string) (bool, error)
	Mark(context.Context, string, time.Time, time.Time) error
}

// AuthDependencies supplies persistent queries, Redis, signing and a testable clock.
type AuthDependencies struct {
	Store  repo.Auth
	Redis  Revocations
	Secret []byte
	Now    func() time.Time
	Audit  func(context.Context, audit.Event) error
}

// AuthService implements the T1.3 authentication and session lifecycle.
type AuthService struct {
	store     repo.Auth
	redis     Revocations
	secret    []byte
	now       func() time.Time
	audit     func(context.Context, audit.Event) error
	dummyHash string
	verify    func(string, string) error
}

// NewAuthService validates its injected dependencies and prepares unknown-user bcrypt work.
func NewAuthService(deps AuthDependencies) (*AuthService, error) {
	if deps.Store == nil || deps.Redis == nil || len(deps.Secret) == 0 || deps.Now == nil || deps.Audit == nil {
		return nil, errors.New("authentication dependencies are required")
	}
	dummy, err := refresh.Generate()
	if err != nil {
		return nil, err
	}
	dummyHash, err := pwd.Hash(dummy)
	if err != nil {
		return nil, err
	}
	return &AuthService{store: deps.Store, redis: deps.Redis, secret: append([]byte(nil), deps.Secret...), now: deps.Now, audit: deps.Audit, dummyHash: dummyHash, verify: pwd.Verify}, nil
}

// RequestMeta supplies HTTP client metadata separately from the credential payload.
type RequestMeta struct{ IP, UserAgent string }

// UserSummary is the safe identity subset returned by login.
type UserSummary struct {
	ID       int64   `json:"id"`
	Username string  `json:"username"`
	Nickname *string `json:"nickname,omitempty"`
}

// Tokens contains the newly issued credentials; callers must never log this value.
type Tokens struct {
	AccessToken  string       `json:"access_token"`
	RefreshToken string       `json:"refresh_token"`
	TokenType    string       `json:"token_type"`
	ExpiresIn    int64        `json:"expires_in"`
	User         *UserSummary `json:"user,omitempty"`
}

func (s *AuthService) issue(tenant, user int64, sid string, meta RequestMeta, now time.Time) (Tokens, model.RefreshToken, error) {
	access, _, err := jwt.Issue(s.secret, tenant, user, sid, now)
	if err != nil {
		return Tokens{}, model.RefreshToken{}, err
	}
	raw, err := refresh.Generate()
	if err != nil {
		return Tokens{}, model.RefreshToken{}, err
	}
	row := model.RefreshToken{TenantID: tenant, UserID: user, SessionID: sid, TokenHash: refresh.Hash(raw), ExpiresAt: now.Add(refresh.TTL), CreatedAt: now, UserAgent: meta.UserAgent}
	if meta.IP != "" {
		row.IP = &meta.IP
	}
	return Tokens{AccessToken: access, RefreshToken: raw, TokenType: "Bearer", ExpiresIn: int64(jwt.AccessTTL.Seconds())}, row, nil
}
