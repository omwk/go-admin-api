package bootstrap

import (
	"github.com/omwk/go-admin-api/internal/kernel/errs"
	"gorm.io/gorm"
)

// Every transition acquiring a running slot uses this transaction-scoped lock.
// Redis is deliberately absent: a cache outage cannot change admission limits.
func lockExportAdmission(q *gorm.DB) error {
	return q.Exec("SELECT pg_advisory_xact_lock(1163415636, 1)").Error
}

func checkExportAdmission(q *gorm.DB, tenant, user, exclude int64, pending bool) error {
	var counts struct{ Global, Tenant, Requester, Pending int64 }
	err := q.Table("export_job").Select(`
count(*) FILTER (WHERE status='running') AS global,
count(*) FILTER (WHERE status='running' AND tenant_id=?) AS tenant,
count(*) FILTER (WHERE status='running' AND requested_by=?) AS requester,
count(*) FILTER (WHERE status='pending' AND tenant_id=?) AS pending`, tenant, user, tenant).
		Where("id<>? AND status IN ('pending','running')", exclude).Scan(&counts).Error
	if err != nil {
		return err
	}
	limits := []struct {
		name    string
		count   int64
		maximum int64
	}{
		{"tenant_pending", counts.Pending, 10},
		{"requester_running", counts.Requester, 1},
		{"tenant_running", counts.Tenant, 1},
		{"global_running", counts.Global, 2},
	}
	for _, limit := range limits {
		if limit.name == "tenant_pending" && !pending {
			continue
		}
		if limit.count >= limit.maximum {
			err := errs.New(errs.ErrTooManyRequests, 429, "导出额度超限: "+limit.name)
			err.Details = map[string]any{"limit": limit.name, "maximum": limit.maximum}
			return err
		}
	}
	return nil
}
