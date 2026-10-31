package main

import (
	"bytes"
	"context"
	"testing"

	"github.com/omwk/go-admin-api/internal/modules/identity/permission"
	"github.com/stretchr/testify/require"
)

func TestPermissionsExportNeedsNoInfrastructure(t *testing.T) {
	var expected, actual bytes.Buffer
	require.NoError(t, permission.Export(&expected))
	require.NoError(t, run(context.Background(), []string{"permissions", "export"}, &actual))
	require.Equal(t, expected.String(), actual.String())
}
