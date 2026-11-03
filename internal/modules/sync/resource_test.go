package sync

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestUnknownResourceDoesNotFallBackToAnotherHandler(t *testing.T) {
	_, err := lookupResourceHandler(map[string]ResourceHandler{}, "unknown")
	require.Error(t, err)
	require.True(t, strings.Contains(err.Error(), `sync resource "unknown" is not registered`))
}
