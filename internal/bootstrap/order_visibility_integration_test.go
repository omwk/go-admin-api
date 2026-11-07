package bootstrap

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/omwk/go-admin-api/internal/modules/identity"
	"github.com/omwk/go-admin-api/internal/modules/ingestion"
	"github.com/omwk/go-admin-api/internal/modules/order"
	syncmodule "github.com/omwk/go-admin-api/internal/modules/sync"
	"github.com/stretchr/testify/require"
)

func TestOrderVisibilityIntegrationExplicitDeleteReappearReplayAndHistory(t *testing.T) {
	f, platform := newOrderSyncFixture(t, 1)
	query := attachOrderQueries(t, f)
	normal := platform.details["order-00"]
	f.execute(t, f.run(t))
	before := query.stats(t)
	require.EqualValues(t, 1, query.page(t, "/orders").Total)
	deleted := json.RawMessage(`{"external_order_id":"order-00","deleted":true,"vanish_reason":"platform_deleted"}`)
	platform.details["order-00"] = deleted
	f.execute(t, f.run(t))
	detail := query.detail(t, query.page(t, "/orders?include_vanished=true").List[0].ID)
	require.NotNil(t, detail.VanishedAt)
	require.Equal(t, "platform_deleted", *detail.VanishReason)
	require.Zero(t, query.page(t, "/orders").Total)
	require.Equal(t, before, query.stats(t))
	assertVisibilityHistory(t, query, detail.ID, []string{"vanished"})
	f.execute(t, f.run(t))
	assertVisibilityHistory(t, query, detail.ID, []string{"vanished"})
	replayOrderRaw(t, f, normal)
	require.NotNil(t, query.detail(t, detail.ID).VanishedAt, "replay of normal raw cannot restore visibility")
	platform.details["order-00"] = normal
	f.execute(t, f.run(t))
	restored := query.detail(t, detail.ID)
	require.Nil(t, restored.VanishedAt)
	require.Nil(t, restored.VanishReason)
	require.EqualValues(t, 1, query.page(t, "/orders").Total)
	assertVisibilityHistory(t, query, detail.ID, []string{"vanished", "active"})
	replayOrderRaw(t, f, deleted)
	require.NotNil(t, query.detail(t, detail.ID).VanishedAt, "explicit deletion raw can change visibility during replay")
	assertVisibilityHistory(t, query, detail.ID, []string{"vanished", "active", "vanished"})
	require.Equal(t, before, query.stats(t))
}

func attachOrderQueries(t *testing.T, f *syncFixture) *orderQueryFixture {
	t.Helper()
	queries, err := order.NewQueryService(NewOrderQueryStore(f.database))
	require.NoError(t, err)
	profiles, err := identity.NewProfileService(newProfileStore(f.database))
	require.NoError(t, err)
	require.NoError(t, order.RegisterRoutes(f.router, f.identityFixture.service, profiles, queries))
	return &orderQueryFixture{credentialFixture: f.credentialFixture, queries: queries}
}

func assertVisibilityHistory(t *testing.T, f *orderQueryFixture, id int64, expected []string) {
	t.Helper()
	detail := f.detail(t, id)
	var actual []string
	previous := "active"
	for _, h := range detail.History {
		if h.Dimension != "visibility" {
			continue
		}
		require.Equal(t, previous, h.FromStatus)
		require.NotNil(t, h.Source)
		require.Positive(t, h.Source.RawID)
		require.Equal(t, detail.Raw.MappingChecksum, h.Source.MappingChecksum)
		actual = append(actual, h.ToStatus)
		previous = h.ToStatus
	}
	require.Equal(t, expected, actual)
}

func replayOrderRaw(t *testing.T, f *syncFixture, payload json.RawMessage) {
	t.Helper()
	refs, err := rawServiceFor(t, f.database, 1, f.shop.PlatformID).SaveRaw(f.ctx, f.shop.ID, "order", []ingestion.RawItem{{ExternalID: "order-00", Payload: payload}})
	require.NoError(t, err)
	q, cancel := f.database.WithContext(f.ctx)
	var row struct{ Spec json.RawMessage }
	require.NoError(t, q.Table("mapping_version").Select("spec").Where("id=?", f.mappingID).Take(&row).Error)
	cancel()
	mapping, err := ingestion.Compile(row.Spec)
	require.NoError(t, err)
	handler := f.engineStore.handlers["order"]
	item := syncmodule.InterpretReplay(1, f.shop.PlatformID, f.shop.ID, mapping, refs[0], payload, handler)
	require.Empty(t, item.Reason)
	require.True(t, item.Mapped.Replay)
	require.NoError(t, f.database.Transaction(f.ctx, func(ctx context.Context) error {
		q, cancel := f.database.WithContext(ctx)
		defer cancel()
		stats, err := handler.Upsert(ctx, q, item.Mapped)
		require.Zero(t, stats.Quarantined)
		return err
	}))
}
