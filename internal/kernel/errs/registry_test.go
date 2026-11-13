package errs

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRegisteredCodesPreserveAllOriginalValues(t *testing.T) {
	want := map[int]int{10001: ErrInvalidParam, 10002: ErrInternal, 10003: ErrNotFound, 10004: ErrConflict, 10005: ErrTooManyRequests, 10006: ErrIdempotencyMismatch, 10007: ErrUpstreamTimeout, 10409: ErrVersionConflict, 20107: ErrPermissionDenied, 20108: ErrDataScopeDenied, 20109: ErrRefreshInFlight, 30101: ErrShopNotFound, 30201: ErrShopDuplicate}
	seen := map[int]bool{}
	last := 0
	for _, definition := range All() {
		require.False(t, seen[definition.Code])
		seen[definition.Code] = true
		require.Greater(t, definition.Code, last)
		last = definition.Code
		require.Equal(t, definition.Code, want[definition.Code])
		require.NotEmpty(t, definition.Message)
		require.NotEmpty(t, definition.Domain)
		require.GreaterOrEqual(t, definition.HTTP, 400)
	}
	require.Len(t, seen, len(want))
	for code, value := range want {
		require.Equal(t, code, value)
		require.True(t, seen[code])
	}
	owned := All()
	owned[0].Message = "changed"
	require.NotEqual(t, "changed", All()[0].Message)
}
