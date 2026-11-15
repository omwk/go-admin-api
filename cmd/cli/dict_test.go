package main

import (
	"bytes"
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSystemDictResetRejectsProductionBeforeOpeningDatabase(t *testing.T) {
	// BE-IDN-008 §2.1: production must be rejected before validation or database access.
	for _, env := range []string{"prod", "PROD", " prod "} {
		t.Run(env, func(t *testing.T) {
			t.Setenv("CH_CONFIG_PATH", "")
			t.Setenv("CH_APP_ENV", env)
			t.Setenv("CH_DB_DSN", "")
			t.Setenv("CH_CRYPTO_MASTER_KEY", "")
			var out bytes.Buffer
			err := run(context.Background(), []string{"dict-reset"}, &out)
			require.EqualError(t, err, "dict-reset is forbidden in prod")
			require.Empty(t, out.String())
		})
	}
}
