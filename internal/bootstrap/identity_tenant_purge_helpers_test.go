package bootstrap

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/omwk/go-admin-api/internal/kernel/crypto"
	"github.com/omwk/go-admin-api/internal/kernel/notifier/channel"
	"github.com/omwk/go-admin-api/internal/modules/identity"
	"github.com/omwk/go-admin-api/internal/modules/identity/model"
	"github.com/stretchr/testify/require"
)

func seedTenantPurge(t *testing.T, f *tenantFixture, code string) identity.ProvisionTenantResult {
	t.Helper()
	result := f.provision(t, f.template(t, code+"-template").ID, code)
	f.accept(t, result, code+"-admin")
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	var user model.User
	require.NoError(t, q.Select("id,dept_id,password_hash").Where("tenant_id=?", result.TenantID).Take(&user).Error)
	removed := model.User{TenantID: result.TenantID, Username: code + "-removed", DepartmentID: user.DepartmentID, PasswordHash: user.PasswordHash, Status: "disabled"}
	require.NoError(t, q.Create(&removed).Error)
	require.NoError(t, q.Delete(&removed).Error)
	keyring, err := crypto.NewKeyring(map[int][]byte{1: f.secret}, 1)
	require.NoError(t, err)
	sealed, err := channel.SealJSON(keyring, channel.PersonalChannelAAD(result.TenantID, user.ID, "bark"), "https://example.invalid/"+personalRandom(t))
	require.NoError(t, err)
	require.NoError(t, q.Exec("INSERT INTO sys_user_channel(tenant_id,user_id,channel,endpoint_enc) VALUES(?,?,'bark',?)", result.TenantID, user.ID, sealed).Error)
	for _, statement := range []string{
		"INSERT INTO sys_role_dept(tenant_id,role_id,dept_id) SELECT ?,r.id,d.id FROM sys_role r JOIN sys_dept d ON d.tenant_id=r.tenant_id WHERE r.tenant_id=? LIMIT 1",
		"INSERT INTO notification_preference(tenant_id,user_id,template,channel,enabled) SELECT ?,id,'purge-fixture','email',true FROM sys_user WHERE tenant_id=? AND deleted_at IS NULL LIMIT 1",
	} {
		require.NoError(t, q.Exec(statement, result.TenantID, result.TenantID).Error)
	}
	for _, statement := range []string{
		`INSERT INTO sys_setting(tenant_id,group_key,item_key,value) VALUES(?,'system','system_name','"purge"')`,
		`INSERT INTO outbox_event(tenant_id,aggregate_type,aggregate_id,event_type) VALUES(?,'fixture','1','fixture.cleanup')`,
		`INSERT INTO idempotency_key(tenant_id,scope,key,request_hash) VALUES(?,'fixture','cleanup','fixture')`,
		`INSERT INTO channel_provider(tenant_id,channel,kind,name,config) VALUES(?,'email','smtp','purge-fixture','{}')`,
		`INSERT INTO notify_template(tenant_id,code,subject,body,format) VALUES(?,'purge-fixture','fixture','fixture','text')`,
		`INSERT INTO notify_delivery(tenant_id,channel,template_code,status) VALUES(?,'email','purge-fixture','pending')`,
		`INSERT INTO notify_outbox(id,tenant_id,channel,template_code,payload,status) SELECT id,tenant_id,channel,template_code,'{}','pending' FROM notify_delivery WHERE tenant_id=?`,
		`INSERT INTO notification(tenant_id,channel,target,template) VALUES(?,'inbox','fixture','purge-fixture')`,
	} {
		require.NoError(t, q.Exec(statement, result.TenantID).Error)
	}
	return result
}

func tenantPurgeCounts(t *testing.T, f *tenantFixture, id int64) map[string]int64 {
	t.Helper()
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	counts := map[string]int64{}
	for _, table := range tenantPurgeTables() {
		column := "tenant_id"
		if table == "tenant" {
			column = "id"
		}
		var count int64
		require.NoError(t, q.Table(table).Where(column+"=?", id).Count(&count).Error)
		counts[table] = count
	}
	return counts
}

func tenantPurgeFingerprint(t *testing.T, f *tenantFixture, id int64) map[string]string {
	t.Helper()
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	prints := map[string]string{}
	for _, table := range tenantPurgeTables() {
		column := "tenant_id"
		if table == "tenant" {
			column = "id"
		}
		var digest string
		filter, args := column+"=?", []any{id}
		if id == 0 {
			// Global notification providers and templates use NULL tenant_id.
			filter, args = column+" IS NULL", nil
		}
		statement := "SELECT md5(coalesce(jsonb_agg(to_jsonb(t) ORDER BY to_jsonb(t)::text)::text,'[]')) FROM " + table + " t WHERE " + filter
		require.NoError(t, q.Raw(statement, args...).Scan(&digest).Error)
		prints[table] = digest
	}
	return prints
}

func archiveTenantForPurge(t *testing.T, f *tenantFixture, id int64) {
	t.Helper()
	w, _ := f.request(t, "POST", tenantPath(id)+"/archive", nil, "")
	require.Equal(t, 200, w.Code, w.Body.String())
}

func purgeGrace(t *testing.T, f *tenantFixture, days int) {
	t.Helper()
	settings := &systemSettingStore{database: f.database}
	f.service.UseSettings(settings)
	require.NoError(t, settings.SaveSetting(f.ctx, 1, f.actor.User.ID, "tenant", "purge_grace_days", json.RawMessage(fmt.Sprint(days))))
}

func logTenantPurgeCounts(t *testing.T, label string, counts map[string]int64) {
	t.Helper()
	data, err := json.Marshal(counts)
	require.NoError(t, err)
	t.Logf("%s=%s", label, data)
}
