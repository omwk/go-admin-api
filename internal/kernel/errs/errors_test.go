package errs

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFromPreservesApplicationErrorAndHidesUnknownCause(t *testing.T) {
	cause := errors.New("database failed")
	app := New(ErrConflict, 409, "资源冲突")
	for _, tc := range []struct {
		name string
		err  error
		code int
	}{
		{"direct", app, ErrConflict},
		{"wrapped", fmt.Errorf("operation: %w", app), ErrConflict},
		{"unknown", cause, ErrInternal},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := From(tc.err)
			require.Equal(t, tc.code, got.Code)
			require.NotContains(t, got.Message, cause.Error())
			if tc.code == ErrInternal {
				require.ErrorIs(t, got, cause)
			} else {
				require.Same(t, app, got)
			}
		})
	}
}

func TestInvalidPreservesCauseWithoutExposingIt(t *testing.T) {
	cause := errors.New("invalid input")
	err := Invalid(cause)
	require.Equal(t, ErrInvalidParam, err.Code)
	require.Equal(t, 400, err.HTTPStatus)
	require.ErrorIs(t, err, cause)
	require.NotEqual(t, cause.Error(), err.Message)
}
