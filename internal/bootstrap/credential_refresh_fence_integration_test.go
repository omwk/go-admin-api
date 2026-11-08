package bootstrap

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/omwk/go-admin-api/internal/modules/shop"
	"github.com/omwk/go-admin-api/internal/modules/shop/refresh"
	"github.com/omwk/go-admin-api/internal/modules/shop/refresh/port"
	"github.com/stretchr/testify/require"
)

func (f *credentialRefreshFixture) replaceOrDisable(t *testing.T, action string) {
	t.Helper()
	if action == "disable" {
		_, err := f.service.DisableCredential(f.ctx, f.actor, f.target.CredentialID)
		require.NoError(t, err)
		return
	}
	value, err := json.Marshal(f.randomValue(t))
	require.NoError(t, err)
	_, err = f.service.RotateCredential(f.ctx, f.actor, f.target.CredentialID,
		shop.CredentialRotateRequest{Values: map[string]json.RawMessage{"private_value": value}})
	require.NoError(t, err)
}

func TestCredentialRefreshIntegrationManualMutationFencesOldAttempt(t *testing.T) {
	for _, action := range []string{"rotate", "disable"} {
		for _, phase := range []string{refresh.PhaseIntent, refresh.PhaseSending, refresh.PhaseConsumed} {
			t.Run(action+"/"+phase, func(t *testing.T) {
				f := newCredentialRefreshFixture(t)
				if phase == refresh.PhaseConsumed {
					f.executor = f.newExecutor(t, &credentialRefreshFaults{Store: f.refreshStore, failPublish: true})
				}
				run := f.prepare(t)
				handler := f.platform.handle
				if phase == refresh.PhaseSending {
					f.platform.handle = func(ctx context.Context, r port.Request) (port.Result, error) {
						f.replaceOrDisable(t, action)
						return handler(ctx, r)
					}
				} else if phase == refresh.PhaseIntent {
					f.replaceOrDisable(t, action)
				}
				_, err := f.executor.Execute(f.ctx, f.target, run.ID)
				if phase == refresh.PhaseConsumed {
					require.Error(t, err)
					f.replaceOrDisable(t, action)
				} else {
					require.NoError(t, err)
				}
				f.executor = f.newExecutor(t, f.refreshStore)
				final, err := f.executor.Recover(f.ctx, f.target, run.ID)
				require.NoError(t, err)
				require.Equal(t, "skipped", final.Status)
				require.Nil(t, final.OutputVersionID)
				row, _ := f.readStored(t, f.target.CredentialID, 1)
				require.Greater(t, row.RefreshGeneration, run.Generation)
				if action == "disable" {
					require.Equal(t, "disabled", row.Status)
				} else {
					require.Equal(t, "active", row.Status)
					require.NotEqual(t, f.target.InputVersionID, *row.CurrentVersionID)
					require.Equal(t, 5, row.RetryBudget)
					require.Zero(t, row.ConsecutiveFailures)
				}
				calls := int32(1)
				if phase == refresh.PhaseIntent {
					calls = 0
				}
				require.Equal(t, calls, f.platform.calls.Load())
				f.assertSafe(t)
			})
		}
	}
}
