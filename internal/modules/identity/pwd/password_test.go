package pwd

import (
	"crypto/rand"
	"encoding/base64"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"
)

func TestHashAndVerifyUsesConfiguredCost(t *testing.T) {
	password := make([]byte, 32)
	_, err := rand.Read(password)
	require.NoError(t, err)
	passwordValue := base64.RawURLEncoding.EncodeToString(password)
	wrongPassword := make([]byte, 32)
	_, err = rand.Read(wrongPassword)
	require.NoError(t, err)
	wrongPasswordValue := base64.RawURLEncoding.EncodeToString(wrongPassword)
	hashed, err := Hash(passwordValue)
	require.NoError(t, err)
	cost, err := bcrypt.Cost([]byte(hashed))
	require.NoError(t, err)
	require.Equal(t, 12, cost)
	require.NoError(t, Verify(hashed, passwordValue))
	require.Error(t, Verify(hashed, wrongPasswordValue))
	require.Error(t, Verify("invalid-hash", wrongPasswordValue))
	require.Error(t, Verify("", ""))
}
