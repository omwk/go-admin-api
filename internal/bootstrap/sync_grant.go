package bootstrap

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/oklog/ulid/v2"
	"github.com/omwk/go-admin-api/internal/modules/sync/repo"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var errGrantBusy = errors.New("scheduling grant unavailable")
var errGrantFence = errors.New("scheduling generation lost")

type grantKey struct{}

// The reserved options entry is changed only under the sync_job row lock.
type schedulingGrant struct {
	JobID    int64     `json:"job_id"`
	Kind     string    `json:"kind"`
	Class    string    `json:"class"`
	RunID    int64     `json:"run_id"`
	Owner    string    `json:"owner"`
	Epoch    int64     `json:"epoch"`
	Deadline time.Time `json:"deadline"`
	Bypasses int       `json:"bypasses"`
}

func lockSchedule(q *gorm.DB, job int64) (schedulingGrant, error) {
	var row struct{ Options json.RawMessage }
	err := q.Table("sync_job").Select("options").Where("id=?", job).Clauses(clause.Locking{Strength: "UPDATE"}).Take(&row).Error
	var options struct {
		Grant schedulingGrant `json:"_scheduler"`
	}
	if err == nil {
		err = json.Unmarshal(row.Options, &options)
	}
	options.Grant.JobID = job
	return options.Grant, err
}

func saveSchedule(q *gorm.DB, grant schedulingGrant) error {
	raw, err := json.Marshal(grant)
	if err != nil {
		return err
	}
	return syncChanged(q.Exec("UPDATE sync_job SET options=jsonb_set(options,'{_scheduler}',?::jsonb) WHERE id=?", string(raw), grant.JobID))
}

type scheduleCandidate struct {
	Kind  string
	ID    int64
	Class string
}

func readyCandidates(q *gorm.DB, job int64, now time.Time) ([]scheduleCandidate, error) {
	var rows []scheduleCandidate
	err := q.Raw(`SELECT 'sync' AS kind,r.id,CASE WHEN coalesce((r.stats->>'replay_id')::bigint,0)>0 THEN 'replay' ELSE 'sync' END AS class
 FROM sync_run r JOIN sync_job j ON j.id=r.job_id AND j.tenant_id=r.tenant_id
 WHERE j.id=? AND j.enabled AND r.finished_at IS NULL AND r.stats->>'pending'='true' AND r.ready_at<=?
 UNION ALL SELECT 'replay',r.id,'replay' FROM replay_run r JOIN sync_job j ON j.tenant_id=r.tenant_id AND j.shop_id=r.shop_id AND j.resource_type=r.resource_type
 WHERE j.id=? AND j.enabled AND r.finished_at IS NULL AND r.ready_at<=? ORDER BY kind,id`, job, now, job, now).Scan(&rows).Error
	return rows, err
}

func chooseCandidate(rows []scheduleCandidate, bypasses int) (scheduleCandidate, bool) {
	var sync, replay *scheduleCandidate
	for i := range rows {
		if rows[i].Class == "sync" && sync == nil {
			sync = &rows[i]
		}
		if rows[i].Class == "replay" && replay == nil {
			replay = &rows[i]
		}
	}
	if sync != nil && (replay == nil || bypasses < 4) {
		return *sync, replay != nil
	}
	if replay != nil {
		return *replay, true
	}
	return scheduleCandidate{}, false
}

func (s *syncEngineStore) acquireGrant(ctx context.Context, job int64, kind string, id int64) (schedulingGrant, error) {
	var grant schedulingGrant
	err := s.database.Transaction(ctx, func(ctx context.Context) error {
		q, cancel := s.database.WithContext(ctx)
		defer cancel()
		current, err := lockSchedule(q, job)
		if err != nil {
			return err
		}
		now := s.now().UTC()
		if current.Owner != "" && now.Before(current.Deadline) {
			return errGrantBusy
		}
		rows, err := readyCandidates(q, job, now)
		if err != nil {
			return err
		}
		selected, replayReady := chooseCandidate(rows, current.Bypasses)
		if selected.Kind != kind || selected.ID != id {
			return errGrantBusy
		}
		budget := 60 * time.Second
		debt := 0
		if selected.Class == "replay" {
			budget = s.settings.ReplayBatchBudget()
		} else if replayReady {
			debt = current.Bypasses + 1
		}
		grant = schedulingGrant{JobID: job, Kind: kind, Class: selected.Class, RunID: id, Owner: ulid.Make().String(), Epoch: current.Epoch + 1, Deadline: now.Add(budget), Bypasses: debt}
		if err := saveSchedule(q, grant); err != nil {
			return err
		}
		return syncChanged(q.Table(kind+"_run").Where("id=? AND finished_at IS NULL", id).Update("scheduling_generation", grant.Epoch))
	})
	return grant, err
}

func (s *syncEngineStore) releaseGrant(ctx context.Context, grant schedulingGrant) error {
	return s.database.Transaction(ctx, func(ctx context.Context) error {
		q, cancel := s.database.WithContext(ctx)
		defer cancel()
		current, err := lockSchedule(q, grant.JobID)
		if err != nil {
			return err
		}
		if current.Epoch != grant.Epoch || current.Owner != grant.Owner {
			return nil
		}
		current.Owner = ""
		return saveSchedule(q, current)
	})
}

func (s *syncEngineStore) fenceGrant(ctx context.Context, q *gorm.DB, kind string, id, epoch int64) error {
	grant, ok := ctx.Value(grantKey{}).(schedulingGrant)
	if !ok || grant.Kind != kind || grant.RunID != id || grant.Epoch != epoch {
		return errGrantFence
	}
	current, err := lockSchedule(q, grant.JobID)
	if err != nil {
		return err
	}
	if current.Epoch != epoch || current.Owner != grant.Owner || !s.now().Before(current.Deadline) {
		return errGrantFence
	}
	return ctx.Err()
}

func (s *syncEngineStore) withSyncGrant(ctx context.Context, job repo.Job, run repo.Run, fn func(context.Context) error) error {
	grant, err := s.acquireGrant(ctx, job.ID, "sync", run.ID)
	if errors.Is(err, errGrantBusy) {
		return s.emitRunWake(ctx, run, 5*time.Second, false)
	}
	if err != nil {
		return err
	}
	work, cancel := context.WithTimeout(context.WithValue(ctx, grantKey{}, grant), grant.Deadline.Sub(s.now()))
	err = fn(work)
	cancel()
	cleanup, stop := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	defer stop()
	return errors.Join(err, s.releaseGrant(cleanup, grant))
}
