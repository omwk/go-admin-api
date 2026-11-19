package bootstrap

import (
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAdminNotificationTemplateNameIntegrationDeliveries(t *testing.T) {
	// BE-IDN-017 §3 #1/#2: display names win; legacy and deleted templates remain readable.
	f := newNotificationFixtureF3(t)
	require.NoError(t, seedNotificationTemplates(f.ctx, f.database))
	require.NoError(t, f.database.Exec(f.ctx, `INSERT INTO notify_template(tenant_id,code,name,subject,body,format) VALUES
 (1,'custom.named','自定义显示名','标题 {{.Name}}','内容','text'),
 (1,'custom.subject','','旧标题 {{.Name}}','内容','text'),
 (1,'custom.code','','','内容','text')`))
	want := map[string]string{
		"identity.verify": "验证注册邮箱",
		"custom.named":    "自定义显示名",
		"custom.subject":  "旧标题 {{.Name}}",
		"custom.code":     "custom.code",
		"custom.deleted":  "custom.deleted",
	}
	for code := range want {
		require.NoError(t, f.database.Exec(f.ctx, "INSERT INTO notify_delivery(tenant_id,channel,template_code,status) VALUES(1,'email',?,'sent')", code))
	}
	total, rows := readableDeliveryPage(t, f, "")
	require.EqualValues(t, len(want), total)
	require.Len(t, rows, len(want))
	got := map[string]string{}
	for _, row := range rows {
		got[row.TemplateCode] = row.TemplateName
	}
	require.Equal(t, want, got)
	t.Log("BE-IDN-017 §3 #1/#2: identity.verify -> 验证注册邮箱; custom.named -> 自定义显示名; empty name -> 旧标题 {{.Name}}; empty subject or deleted template -> code")
}

func TestAdminNotificationTemplateNameIntegrationCRUD(t *testing.T) {
	// BE-IDN-017 §3 #3: PUT creates and updates the same tenant/locale override.
	f := newNotificationFixtureF3(t)
	in := map[string]any{"locale": "zh-CN", "subject": "标题 {{.Name}}", "body": "正文 {{.Name}}", "format": "text", "enabled": true}
	var id int64
	for _, tc := range []struct {
		label, name string
		present     bool
		status      int
	}{
		{"create", "显示名", true, 200},
		{"update", "修改显示名", true, 200},
		{"empty", "", true, 200},
		{"unicode_limit", strings.Repeat("名", 128), true, 200},
		{"unicode_over_limit", strings.Repeat("名", 129), true, 400},
		{"ascii_over_limit", strings.Repeat("n", 129), true, 400},
		{"omitted", "", false, 200},
	} {
		t.Run(tc.label, func(t *testing.T) {
			delete(in, "name")
			if tc.present {
				in["name"] = tc.name
			}
			w := f.request(t, http.MethodPut, "/templates/custom.name", "", in)
			require.Equal(t, tc.status, w.Code, w.Body.String())
			if tc.status != 200 {
				require.Contains(t, w.Body.String(), `"code":10001`)
				return
			}
			var got struct {
				ID                  int64
				Name, Subject, Body string
			}
			notificationDataF3(t, w, &got)
			require.Equal(t, tc.name, got.Name)
			require.Equal(t, in["subject"], got.Subject)
			require.Equal(t, in["body"], got.Body)
			if id == 0 {
				id = got.ID
			}
			require.Positive(t, id)
			require.Equal(t, id, got.ID)
			w = f.request(t, http.MethodGet, "/templates?page_size=100", "", nil)
			require.Equal(t, 200, w.Code, w.Body.String())
			var page struct {
				List []struct {
					ID   int64
					Name string
				}
			}
			notificationDataF3(t, w, &page)
			var names []string
			for _, row := range page.List {
				if row.ID == id {
					names = append(names, row.Name)
				}
			}
			require.Equal(t, []string{tc.name}, names)
		})
	}
	f.assertPrivacy(t, f.token)
	t.Log("BE-IDN-017 §3 #3: create/update/clear/omitted name round-trip through PUT and GET; 128 Chinese characters accepted; 129 Chinese/ASCII rejected with 400/10001; subject/body unchanged")
}

func TestAdminNotificationTemplateNameMigrationIntegrationThirtyFour(t *testing.T) {
	// BE-IDN-017 §3 #4: the new column defaults to empty without rewriting subjects/bodies.
	database, ctx := isolatedIdentityDB(t)
	migrationAt(ctx, t, database, 33)
	require.NoError(t, database.Exec(ctx, `INSERT INTO notify_template(tenant_id,code,subject,body,format)
 VALUES(1,'custom.legacy','标题 {{.Name}}','正文 {{.Name}}','text')`))
	q, cancel := database.WithContext(ctx)
	defer cancel()
	for i := 0; i < 2; i++ {
		state, err := database.Migrate(ctx, "up")
		require.NoError(t, err)
		require.EqualValues(t, latestBootstrapMigration(t), state.Version)
		state = migrationAt(ctx, t, database, 34)
		require.EqualValues(t, 34, state.Version)
		require.False(t, state.Dirty)
		var named int64
		require.NoError(t, q.Table("notify_template").Where("name <> '' OR name IS NULL").Count(&named).Error)
		require.Zero(t, named)
		var column struct{ DataType, IsNullable, ColumnDefault string }
		require.NoError(t, q.Raw("SELECT data_type,is_nullable,column_default FROM information_schema.columns WHERE table_schema=current_schema() AND table_name='notify_template' AND column_name='name'").Scan(&column).Error)
		require.Equal(t, "text", column.DataType)
		require.Equal(t, "NO", column.IsNullable)
		require.Equal(t, "''::text", column.ColumnDefault)
		var legacy struct{ Subject, Body, Name string }
		require.NoError(t, q.Table("notify_template").Select("subject,body,name").Where("code='custom.legacy'").Take(&legacy).Error)
		require.Equal(t, "标题 {{.Name}}", legacy.Subject)
		require.Equal(t, "正文 {{.Name}}", legacy.Body)
		require.Empty(t, legacy.Name)
		if i == 0 {
			require.NoError(t, database.Exec(ctx, "UPDATE notify_template SET name='显示名' WHERE code='custom.legacy'"))
			state, err = database.Migrate(ctx, "down")
			require.NoError(t, err)
			require.EqualValues(t, 33, state.Version)
			require.False(t, state.Dirty)
			var columns int64
			require.NoError(t, q.Raw("SELECT count(*) FROM information_schema.columns WHERE table_schema=current_schema() AND table_name='notify_template' AND column_name='name'").Scan(&columns).Error)
			require.Zero(t, columns)
		}
	}
	t.Log("BE-IDN-017 §3 #4: database.Migrate(up/down/up): 33 -> 34 -> 33 -> 34; name=text NOT NULL DEFAULT ''; existing rows name=''; legacy subject/body unchanged")
}
