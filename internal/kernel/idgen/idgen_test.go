package idgen

import (
	"testing"

	"github.com/oklog/ulid/v2"
	"github.com/stretchr/testify/require"
)

func TestNewULIDProducesUniqueIdentifiers(t *testing.T) {
	first, err := NewULID()
	require.NoError(t, err)
	second, err := NewULID()
	require.NoError(t, err)
	_, err = ulid.ParseStrict(first)
	require.NoError(t, err)
	_, err = ulid.ParseStrict(second)
	require.NoError(t, err)
	if first == second {
		t.Fatal("ULIDs must be unique")
	}
}
