package db

import (
	"context"
	"testing"
	"time"

	"github.com/omwk/go-admin-api/internal/kernel/config"
	"github.com/stretchr/testify/require"
)

func TestOpenRejectsInvalidPoolSettings(t *testing.T) {
	for _, cfg := range []config.DB{
		{},
		{MaxOpen: 1, MaxIdle: 2, ConnMaxLifetime: time.Minute},
		{MaxOpen: 1, MaxIdle: -1, ConnMaxLifetime: time.Minute},
	} {
		_, err := Open(context.Background(), cfg)
		require.ErrorContains(t, err, "db pool")
	}
}

func TestMigrateRejectsInvalidCommandBeforeConnecting(t *testing.T) {
	_, err := (&DB{}).Migrate(context.Background(), "invalid")
	require.ErrorContains(t, err, "up, down or version")
}
