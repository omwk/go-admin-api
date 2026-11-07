package bootstrap

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/omwk/go-admin-api/internal/kernel/logger"
	"github.com/omwk/go-admin-api/internal/modules/ingestion"
	"github.com/stretchr/testify/require"
)

func piiSyncCanary(t *testing.T, f *syncFixture, p *orderTestPlatform, marker string) {
	t.Helper()
	p.pii = true
	var payload map[string]any
	require.NoError(t, json.Unmarshal(p.details["order-00"], &payload))
	payload["receiver_name"], payload["phone"], payload["address"] = "张合成", "1-"+marker, marker
	encoded, err := json.Marshal(payload)
	require.NoError(t, err)
	p.details["order-00"] = encoded
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	var row struct{ Spec json.RawMessage }
	require.NoError(t, q.Table("mapping_version").Select("spec").Where("id=?", f.mappingID).Take(&row).Error)
	var spec ingestion.Spec
	require.NoError(t, json.Unmarshal(row.Spec, &spec))
	for _, name := range []string{"receiver_name", "phone", "address"} {
		spec.Fields = append(spec.Fields, ingestion.FieldSpec{Target: name, Source: "$." + name})
	}
	encoded, err = json.Marshal(spec)
	require.NoError(t, err)
	compiled, err := ingestion.Compile(encoded)
	require.NoError(t, err)
	require.NoError(t, q.Table("mapping_version").Where("id=?", f.mappingID).Updates(map[string]any{"spec": json.RawMessage(encoded), "checksum": compiled.Checksum}).Error)
}

func assertPIIAbsentFromDatabase(t *testing.T, f *credentialFixture, marker string) {
	t.Helper()
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	var tables []string
	require.NoError(t, q.Raw("SELECT tablename FROM pg_tables WHERE schemaname=current_schema() ORDER BY tablename").Scan(&tables).Error)
	require.NotEmpty(t, tables)
	for _, table := range tables {
		quoted := `"` + strings.ReplaceAll(table, `"`, `""`) + `"`
		var leaked bool
		require.NoError(t, q.Raw(fmt.Sprintf("SELECT EXISTS(SELECT 1 FROM %s r WHERE strpos(row_to_json(r)::text,?)>0)", quoted), marker).Scan(&leaked).Error)
		require.False(t, leaked, "synthetic PII present in table %s", table)
	}
	t.Logf("PII canary absent from all %d database tables, including raw, address, audit and export materialization", len(tables))
}

func TestOrderPIIIntegrationCanaryAcrossAcquisitionReplayProjectionAndExport(t *testing.T) {
	// REQ-ORD-006/007/008: the synthetic marker travels through the real acquisition path.
	s, platform := newOrderSyncFixture(t, 1)
	log, err := logger.New(s.logs, "debug", "json")
	require.NoError(t, err)
	s.ctx = logger.With(s.ctx, log)
	marker := "PII-CANARY-123"
	piiSyncCanary(t, s, platform, marker)
	s.execute(t, s.run(t))
	require.EqualValues(t, 1, orderTableCount(t, s, "sales_order", "true"))
	f := orderExportFixtureFor(t, orderQueryFixtureFor(t, s.credentialFixture))
	page := f.page(t, "/orders")
	require.Len(t, page.List, 1)
	id := page.List[0].ID
	require.Equal(t, "张*", page.List[0].Receiver.ReceiverName)
	require.Equal(t, "1**********", page.List[0].Receiver.Phone)
	f.detail(t, id)
	denied, _ := f.piiRequest(t, id)
	require.Equal(t, 403, denied.Code)
	require.False(t, strings.Contains(denied.Body.String(), marker))
	orderPIIReauth(t, f.orderQueryFixture)
	allowed, _ := f.piiRequest(t, id)
	require.Equal(t, 200, allowed.Code)
	require.True(t, strings.Contains(allowed.Body.String(), marker), "authorized detail must decrypt the canary")
	request := replayRequest(s)
	request.Resource = "order"
	run, err := s.engineStore.CreateReplay(s.ctx, request, nil)
	require.NoError(t, err)
	require.EqualValues(t, 1, run.Total)
	run, err = s.engineStore.ProcessReplay(s.ctx, 1, run.ID, 0)
	require.NoError(t, err)
	require.Equal(t, "success", run.Status)
	require.EqualValues(t, 1, run.Stats.SkippedSame)
	job := f.create(t)
	require.NoError(t, f.files.Execute(f.ctx, 1, job.ID))
	csv, err := json.Marshal(f.csv(t, job.ID))
	require.NoError(t, err)
	require.False(t, strings.Contains(string(csv), marker))
	assertPIIAbsentFromDatabase(t, f.credentialFixture, marker)
	require.False(t, strings.Contains(f.logs.text(), marker))
	require.False(t, strings.Contains(f.sqlLogs.text(), marker))
	require.Contains(t, f.logs.text(), "HTTP request")
	t.Log("canary absent from masked list/detail, errors, application/SQL logs and CSV; only reauthenticated detail discloses; encrypted raw replay succeeds")
}
