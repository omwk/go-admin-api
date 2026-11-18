package bootstrap

import (
	"testing"

	"github.com/omwk/go-admin-api/internal/kernel/notifier/channel"
	"github.com/stretchr/testify/require"
)

func TestSeedAccountTemplatesIntegrationRefreshBuiltinsAndPreserveCustom(t *testing.T) {
	// BE-IDN-016 §4 #7: CLI seed refreshes known codes, including tenant overrides.
	database, ctx := isolatedIdentityDB(t)
	password := accountPassword(t)
	require.NoError(t, SeedIdentity(ctx, database, password))
	q, cancel := database.WithContext(ctx)
	defer cancel()
	tenant := int64(1)
	override := channel.Template{TenantID: &tenant, Code: "identity.verify", Locale: "zh-CN", Subject: "old", Body: "old", Format: "html", Enabled: false}
	custom := channel.Template{TenantID: &tenant, Code: "identity.custom", Locale: "zh-CN", Subject: "Custom subject", Body: "Custom body", Format: "html", Enabled: true}
	require.NoError(t, q.Table("notify_template").Create(&override).Error)
	require.NoError(t, q.Table("notify_template").Create(&custom).Error)
	require.NoError(t, q.Table("notify_template").Where("tenant_id IS NULL").Updates(map[string]any{"subject": "old", "body": "old", "format": "html", "enabled": false}).Error)
	for i := 0; i < 2; i++ {
		require.NoError(t, SeedIdentity(ctx, database, password))
		for _, want := range seededNotificationTemplates() {
			var got channel.Template
			require.NoError(t, q.Table("notify_template").Select(templateColumns).Where("tenant_id IS NULL AND code=? AND locale=?", want.Code, want.Locale).Take(&got).Error)
			require.Equal(t, want.Subject, got.Subject)
			require.Equal(t, want.Body, got.Body)
			require.Equal(t, want.Format, got.Format)
			require.False(t, got.Enabled)
		}
		var got channel.Template
		require.NoError(t, q.Table("notify_template").Select(templateColumns).Where("id=?", override.ID).Take(&got).Error)
		for _, want := range seededNotificationTemplates() {
			if want.Code == override.Code {
				require.Equal(t, want.Subject, got.Subject)
				require.Equal(t, want.Body, got.Body)
				require.Equal(t, want.Format, got.Format)
			}
		}
		require.False(t, got.Enabled)
		got = channel.Template{}
		require.NoError(t, q.Table("notify_template").Select(templateColumns).Where("id=?", custom.ID).Take(&got).Error)
		require.Equal(t, custom, got)
	}
	t.Log("seed x2: all 14 built-ins refreshed; tenant built-in override refreshed; enabled flags and identity.custom unchanged")
}
