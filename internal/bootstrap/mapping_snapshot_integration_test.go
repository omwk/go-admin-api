package bootstrap

import (
	"context"
	"encoding/json"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/omwk/go-admin-api/internal/kernel/config"
	"github.com/omwk/go-admin-api/internal/kernel/db"
	"github.com/omwk/go-admin-api/internal/kernel/scope"
	"github.com/omwk/go-admin-api/internal/modules/ingestion"
	"github.com/omwk/go-admin-api/internal/testenv"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm/clause"
)

func TestMappingSnapshotIntegrationPublishAndWorker(t *testing.T) {
	f := newSyncFixture(t, syncDataset(t, 1))
	f.execute(t, f.run(t)) // Supply a real raw record for publication preflight.
	profile, request := seedSnapshotDraft(t, f)
	program, result, err := PublishMapping(f.ctx, f.database, profile, 0, request)
	require.NoError(t, err)
	require.Equal(t, 1, result.Checked)
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	var stored struct {
		Spec        json.RawMessage
		Checksum    string
		PublishedBy int64
	}
	require.NoError(t, q.Table("mapping_version").Where("id=?", request.VersionID).Take(&stored).Error)
	var spec ingestion.Spec
	require.NoError(t, json.Unmarshal(stored.Spec, &spec))
	require.Equal(t, "online", spec.StatusTables["product_status"]["online"])
	require.Equal(t, f.actor.UserID, stored.PublishedBy)
	require.Equal(t, program.Checksum, stored.Checksum)
	var active int64
	require.NoError(t, q.Table("mapping_profile").Select("active_version_id").Where("id=?", profile).Take(&active).Error)
	require.Equal(t, request.VersionID, active)
	runSnapshotWorkerProcess(t, f, request.VersionID, stored.Checksum)
	require.NoError(t, f.database.Exec(f.ctx, `UPDATE sys_setting SET value='{"online":"offline"}' WHERE group_key='status_map'`))
	_, _, err = PublishMapping(f.ctx, f.database, profile, request.VersionID, request)
	require.Error(t, err) // Published rows cannot be rewritten with a new snapshot.
	// A worker must still compile and apply even when sys_setting is unavailable.
	require.NoError(t, f.database.Exec(f.ctx, "ALTER TABLE sys_setting RENAME TO unavailable_status_settings"))
	runSnapshotWorkerProcess(t, f, request.VersionID, stored.Checksum)
	require.NoError(t, f.database.Exec(f.ctx, `UPDATE sync_job SET options=jsonb_set(options,'{mapping_version_id}',to_jsonb(?::bigint)) WHERE id=?`, request.VersionID, f.jobID))
	f.rebuild(t) // Actual orchestration path starts with a fresh compiled cache.
	f.execute(t, f.run(t))
	q, finalCancel := f.database.WithContext(f.ctx)
	defer finalCancel()
	var status string
	require.NoError(t, q.Table("listing").Select("status").Where("shop_id=?", f.shop.ID).Take(&status).Error)
	require.Equal(t, "online", status)
}

func TestMappingSnapshotIntegrationRejectsStaleDraft(t *testing.T) {
	f := newSyncFixture(t, syncDataset(t, 1))
	profile, request := seedSnapshotDraft(t, f)
	require.NoError(t, f.database.Exec(f.ctx, `UPDATE mapping_version SET spec=jsonb_set(spec,'{key_field}','"changed"') WHERE id=?`, request.VersionID))
	_, _, err := PublishMapping(f.ctx, f.database, profile, 0, request)
	require.ErrorContains(t, err, "draft changed")
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	var status string
	require.NoError(t, q.Table("mapping_version").Select("status").Where("id=?", request.VersionID).Take(&status).Error)
	require.Equal(t, "draft", status)
}

func TestMappingSnapshotIntegrationCASRollsBackAndRejectsLegacy(t *testing.T) {
	f := newSyncFixture(t, syncDataset(t, 1))
	profile, request := seedSnapshotDraft(t, f)
	_, _, err := PublishMapping(f.ctx, f.database, profile, f.mappingID, request)
	require.Error(t, err) // Profile has no active version; expected version is stale.
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	var row struct {
		Spec             json.RawMessage
		Checksum, Status string
	}
	require.NoError(t, q.Table("mapping_version").Where("id=?", request.VersionID).Take(&row).Error)
	require.Equal(t, "draft", row.Status)
	require.Equal(t, "draft-checksum", row.Checksum)
	var spec map[string]any
	require.NoError(t, json.Unmarshal(row.Spec, &spec))
	require.NotContains(t, spec, "status_tables")
	_, _, err = PublishMapping(scope.WithAccess(f.ctx, scope.Access{}), f.database, profile, 0, request)
	require.Error(t, err)
	foreign := scope.WithAccess(f.ctx, scope.Access{Tenant: 2, User: f.actor.UserID, Roles: []scope.Role{{Kind: "all"}}})
	_, _, err = PublishMapping(foreign, f.database, profile, 0, request)
	require.Error(t, err)
	run := f.run(t)
	require.NoError(t, f.database.Exec(f.ctx, "UPDATE mapping_version SET spec=spec-'status_tables' WHERE id=?", f.mappingID))
	f.rebuild(t)
	_, err = f.engineStore.LoadWork(f.ctx, f.jobID, run.ID)
	require.ErrorIs(t, err, ingestion.ErrStatusSnapshotMissing)
}

func seedSnapshotDraft(t *testing.T, f *syncFixture) (int64, ingestion.PublishRequest) {
	t.Helper()
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	var row struct {
		ProfileID int64
		Spec      json.RawMessage
	}
	require.NoError(t, q.Table("mapping_version").Select("profile_id,spec").Where("id=?", f.mappingID).Take(&row).Error)
	var object map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(row.Spec, &object))
	delete(object, "status_tables")
	var fields []ingestion.FieldSpec
	require.NoError(t, json.Unmarshal(object["fields"], &fields))
	for i := range fields {
		if fields[i].Target == "status" {
			fields[i].Transform = "map_status(value, 'product_status')"
		}
	}
	// jsonb rewrites exponents; numeric defaults must keep the same digest.
	fields = append(fields, ingestion.FieldSpec{Target: "ext.default_quantity", Source: "$.absent", Default: json.RawMessage(`1e2`)})
	var err error
	object["fields"], err = json.Marshal(fields)
	require.NoError(t, err)
	raw, err := json.Marshal(object)
	require.NoError(t, err)
	version := map[string]any{"tenant_id": 1, "profile_id": row.ProfileID, "version_no": 2, "status": "draft", "spec": json.RawMessage(raw), "checksum": "draft-checksum"}
	require.NoError(t, q.Table("mapping_version").Clauses(clause.Returning{}).Create(version).Error)
	require.NoError(t, f.database.Exec(f.ctx, `INSERT INTO sys_setting (tenant_id,group_key,item_key,value) VALUES (1,'status_map','product_status','{"online":"online"}')`))
	return row.ProfileID, ingestion.PublishRequest{VersionID: version["id"].(int64), Spec: raw}
}

func runSnapshotWorkerProcess(t *testing.T, f *syncFixture, id int64, checksum string) {
	t.Helper()
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	var schema string
	require.NoError(t, q.Raw("SELECT current_schema()").Scan(&schema).Error)
	executable, err := os.Executable()
	require.NoError(t, err)
	command := exec.CommandContext(f.ctx, executable, "-test.run=^TestMappingSnapshotIntegrationWorkerProcess$", "-test.v")
	command.Env = append(os.Environ(), "MAPPING_SNAPSHOT_WORKER_SCHEMA="+schema, "MAPPING_SNAPSHOT_WORKER_ID="+itoa(id), "MAPPING_SNAPSHOT_WORKER_CHECKSUM="+checksum)
	output, err := command.CombinedOutput()
	require.NoError(t, err, "%s", output)
	require.Contains(t, string(output), "PASS")
}

// A separate worker process reads the release from PostgreSQL. No API, publish
// backend, sys_setting lookup, or parent compiler memory is available here.
func TestMappingSnapshotIntegrationWorkerProcess(t *testing.T) {
	schema := os.Getenv("MAPPING_SNAPSHOT_WORKER_SCHEMA")
	if schema == "" {
		t.Skip("only runs as a worker subprocess")
	}
	dsn := testenv.Lookup("TEST_DATABASE_URL")
	if strings.Contains(dsn, "://") {
		parsed, err := url.Parse(dsn)
		require.NoError(t, err)
		query := parsed.Query()
		query.Set("search_path", schema+",public")
		parsed.RawQuery = query.Encode()
		dsn = parsed.String()
	} else {
		dsn += " search_path=" + schema + ",public"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	database, err := db.Open(ctx, config.DB{DSN: dsn, MaxOpen: 2, MaxIdle: 1, ConnMaxLifetime: time.Minute, TxnTimeout: testenv.Timeouts(t).DB.TxnTimeout})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, database.Close()) })
	q, stop := database.WithContext(ctx)
	defer stop()
	var row struct {
		Spec     json.RawMessage
		Checksum string
	}
	require.NoError(t, q.Table("mapping_version").Select("spec,checksum").Where("id=? AND status='published'", os.Getenv("MAPPING_SNAPSHOT_WORKER_ID")).Take(&row).Error)
	program, err := ingestion.CompileFromVersion(row.Spec)
	require.NoError(t, err)
	require.Equal(t, os.Getenv("MAPPING_SNAPSHOT_WORKER_CHECKSUM"), program.Checksum)
	require.Equal(t, row.Checksum, program.Checksum)
	values, problems, err := program.Apply(syncDataset(t, 1)[0])
	require.NoError(t, err)
	require.Empty(t, problems)
	require.Equal(t, "online", values["status"])
}
