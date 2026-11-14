package identity

import (
	"context"
	"errors"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/omwk/go-admin-api/internal/modules/identity/jwt"
	"github.com/omwk/go-admin-api/internal/modules/identity/repo"
	"github.com/omwk/go-admin-api/internal/modules/identity/session"
)

type revocation struct {
	key     string
	expires time.Time
}

func boundedRequestMeta(meta RequestMeta) RequestMeta {
	meta.UserAgent = truncateClientMetadata(meta.UserAgent, 256)
	if len(meta.IP) > 64 {
		meta.IP = ""
	}
	ip := net.ParseIP(meta.IP)
	meta.IP = ""
	if ip != nil {
		meta.IP = ip.String()
	}
	return meta
}

func truncateClientMetadata(value string, limit int) string {
	if len(value) > limit {
		value = value[:limit]
	}
	return strings.ToValidUTF8(value, "")
}

func familyRevoked(family repo.Family) bool {
	if len(family.Tokens) == 0 {
		return true
	}
	for _, row := range family.Tokens {
		if row.RevokedReason == nil {
			continue
		}
		switch *row.RevokedReason {
		case "logout", "kick", "replay":
			return true
		}
	}
	return false
}

func familyMarks(family repo.Family, sid string) []revocation {
	var latest time.Time
	marks := make([]revocation, 0, len(family.Tokens)+1)
	for _, row := range family.Tokens {
		if row.ExpiresAt.After(latest) {
			latest = row.ExpiresAt
		}
		marks = append(marks, revocation{key: session.RefreshKey(row.TokenHash), expires: row.ExpiresAt})
	}
	return append(marks, revocation{key: session.RevocationKey(sid), expires: latest})
}

func (s *AuthService) mark(ctx context.Context, marks []revocation) error {
	for _, mark := range marks {
		now := s.now().UTC()
		if !mark.expires.After(now) {
			continue
		}
		if err := s.redis.Mark(ctx, mark.key, mark.expires, now); err != nil {
			return dependencyFailure(err)
		}
	}
	return nil
}

// Authenticate checks the signature, both Redis namespaces and persistent session state.
func (s *AuthService) Authenticate(ctx context.Context, raw string) (jwt.Claims, error) {
	claims, err := jwt.Verify(s.secret, raw, s.now().UTC())
	if errors.Is(err, jwt.ErrTokenExpired) {
		return jwt.Claims{}, expiredToken()
	}
	if err != nil {
		return jwt.Claims{}, revokedToken()
	}
	for _, key := range []string{session.AccessKey(claims.TokenID), session.RevocationKey(claims.SessionID)} {
		revoked, err := s.redis.Revoked(ctx, key)
		if err != nil {
			return jwt.Claims{}, dependencyFailure(err)
		}
		if revoked {
			return jwt.Claims{}, revokedToken()
		}
	}
	user, err := strconv.ParseInt(claims.Subject, 10, 64)
	if err != nil {
		return jwt.Claims{}, revokedToken()
	}
	revoked, err := s.store.SessionRevoked(ctx, claims.TenantID, user, claims.SessionID)
	if err != nil {
		return jwt.Claims{}, dependencyFailure(err)
	}
	if revoked {
		return jwt.Claims{}, revokedToken()
	}
	return claims, nil
}

// Logout persists revocation of every historical row before writing Redis markers.
func (s *AuthService) Logout(ctx context.Context, claims jwt.Claims) error {
	user, err := strconv.ParseInt(claims.Subject, 10, 64)
	if err != nil || user <= 0 || claims.TenantID <= 0 || claims.SessionID == "" {
		return revokedToken()
	}
	var marks []revocation
	var outcome error
	err = s.store.Transaction(ctx, func(txCtx context.Context) error {
		if err := s.lockAccount(txCtx, claims.TenantID, user); err != nil {
			return err
		}
		if err := s.store.LockSession(txCtx, claims.TenantID, claims.SessionID); err != nil {
			return err
		}
		family, err := s.store.Family(txCtx, claims.TenantID, user, claims.SessionID)
		if err != nil {
			return err
		}
		if len(family.Tokens) == 0 {
			outcome = revokedToken()
			return nil
		}
		if err := s.store.RevokeFamily(txCtx, claims.TenantID, user, claims.SessionID, "logout", s.now().UTC()); err != nil {
			return err
		}
		marks = familyMarks(family, claims.SessionID)
		marks = append(marks, revocation{key: session.AccessKey(claims.TokenID), expires: time.Unix(claims.ExpiresAt, 0)})
		return nil
	})
	if err != nil {
		return dependencyFailure(err)
	}
	if outcome != nil {
		return outcome
	}
	return s.mark(ctx, marks)
}
