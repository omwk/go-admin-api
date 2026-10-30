package jwt

import (
	"crypto/rand"
	"encoding/base64"
	"strings"
	"testing"
	"time"

	"github.com/omwk/go-admin-api/internal/kernel/idgen"
	"github.com/stretchr/testify/require"
)

func testSecret(t *testing.T) []byte {
	t.Helper()
	secret := make([]byte, 32)
	_, err := rand.Read(secret)
	require.NoError(t, err)
	return secret
}

func TestIssueAndVerifyAccessToken(t *testing.T) {
	secret := testSecret(t)
	now := time.Unix(1_800_000_000, 0).UTC()
	sessionID, err := idgen.NewULID()
	require.NoError(t, err)
	token, claims, err := Issue(secret, 3, 7, sessionID, now)
	require.NoError(t, err)
	verified, err := Verify(secret, token, now.Add(time.Minute))
	require.NoError(t, err)
	require.Equal(t, claims, verified)
	require.Equal(t, "7", verified.Subject)
	require.EqualValues(t, 3, verified.TenantID)
	require.Equal(t, now.Add(AccessTTL).Unix(), verified.ExpiresAt)

	_, next, err := Issue(secret, 3, 7, verified.SessionID, now)
	require.NoError(t, err)
	if claims.TokenID == next.TokenID {
		t.Fatal("each access token must receive a new JTI")
	}
}

func TestVerifyRejectsExpiredAndTamperedTokens(t *testing.T) {
	secret := testSecret(t)
	now := time.Unix(1_800_000_000, 0).UTC()
	sessionID, err := idgen.NewULID()
	require.NoError(t, err)
	token, _, err := Issue(secret, 3, 7, sessionID, now)
	require.NoError(t, err)
	_, err = Verify(secret, token, now.Add(AccessTTL))
	require.ErrorIs(t, err, ErrTokenExpired)

	parts := strings.Split(token, ".")
	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	require.NoError(t, err)
	signature[0] ^= 1
	parts[2] = base64.RawURLEncoding.EncodeToString(signature)
	_, err = Verify(secret, strings.Join(parts, "."), now)
	require.ErrorIs(t, err, ErrInvalidToken)
}
