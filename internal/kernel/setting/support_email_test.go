package setting

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

type supportEmailReader struct {
	values map[string]any
	err    error
}

func (r supportEmailReader) Values(context.Context, int64, string) (map[string]any, error) {
	return r.values, r.err
}

func TestSupportEmailDefaultOverrideAndFailure(t *testing.T) {
	ctx := context.Background()
	value, err := SupportEmail(ctx, nil, 1)
	require.NoError(t, err)
	require.Empty(t, value)
	value, err = SupportEmail(ctx, supportEmailReader{values: map[string]any{"support_email": "support@example.invalid"}}, 1)
	require.NoError(t, err)
	require.Equal(t, "support@example.invalid", value)
	_, err = SupportEmail(ctx, supportEmailReader{err: errors.New("settings unavailable")}, 1)
	require.Error(t, err)
	_, err = SupportEmail(ctx, supportEmailReader{values: map[string]any{"support_email": true}}, 1)
	require.Error(t, err)
}
