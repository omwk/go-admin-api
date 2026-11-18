package main

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestAuditPruneArgumentsAndProductionGuard(t *testing.T) {
	for _, args := range [][]string{{}, {"--before", "bad"}, {"--before", "2026-01-01"}, {"--unknown"}, {"--before", "2026-01-01T00:00:00Z", "extra"}} {
		var out bytes.Buffer
		require.Error(t, run(context.Background(), append([]string{"audit-prune"}, args...), &out))
		require.Empty(t, out.String())
	}
	t.Setenv("CH_CONFIG_PATH", "")
	t.Setenv("CH_APP_ENV", "prod")
	var out bytes.Buffer
	err := run(context.Background(), []string{"audit-prune", "--before", "2026-01-01T00:00:00Z"}, &out)
	require.EqualError(t, err, "audit-prune in prod requires --force")
	require.Empty(t, out.String())
}

func TestIdentityAuditPruneCLIIntegrationCutoffAndForce(t *testing.T) {
	// BE-IDN-009 §4.11: invoke the real command against an isolated schema.
	f := newSeedFixture(t)
	t.Setenv("CH_APP_ENV", "prod")
	t.Setenv("CH_APP_PUBLIC_URL", "https://accounts.example.invalid")
	cutoff := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, at := range []time.Time{cutoff.Add(-time.Microsecond), cutoff, cutoff.Add(time.Microsecond)} {
		require.NoError(t, f.database.Exec(f.ctx, `INSERT INTO audit_log(tenant_id,actor_type,action,resource_type,resource_id,trace_id,result,created_at)
			VALUES(1,'system','identity.reauth','user','1','prune-cli','success',?)`, at))
	}
	var out bytes.Buffer
	args := []string{"audit-prune", "--before", cutoff.Format(time.RFC3339)}
	require.EqualError(t, run(f.ctx, args, &out), "audit-prune in prod requires --force")
	require.NoError(t, run(f.ctx, append(args, "--force"), &out))
	require.Equal(t, "deleted=1\n", out.String())
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	var remaining, expired int64
	require.NoError(t, q.Table("audit_log").Where("trace_id='prune-cli'").Count(&remaining).Error)
	require.NoError(t, q.Table("audit_log").Where("created_at < ?", cutoff).Count(&expired).Error)
	require.EqualValues(t, 2, remaining)
	require.Zero(t, expired)
	t.Log("prod without --force rejected; with --force deleted=1; before cutoff=0; equal/after cutoff=2")
}
