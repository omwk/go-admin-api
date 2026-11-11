package pwd

import (
	"crypto/rand"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPasswordPolicy(t *testing.T) {
	// F1 §5.7: length and complexity are enforced before hashing.
	material := make([]byte, 12)
	_, err := rand.Read(material)
	require.NoError(t, err)
	valid := "a1" + hex.EncodeToString(material)
	for _, tc := range []struct {
		name, value string
		valid       bool
	}{
		{"valid", valid, true}, {"short", valid[:7], false},
		{"letters", strings.Repeat("a", 12), false}, {"digits", strings.Repeat("1", 12), false},
		{"bcrypt limit", valid + strings.Repeat("a", 73), false},
	} {
		t.Run(tc.name, func(t *testing.T) { require.Equal(t, tc.valid, Validate(tc.value) == nil) })
	}
}
