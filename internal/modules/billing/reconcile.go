package billing

import (
	"context"
	"errors"
	"time"
)

// Reconciler records dated facts, then delivers persistent pending alerts outside transactions.
type Reconciler struct {
	store ReconcileStore
	now   func() time.Time
	alert func(context.Context, Difference) error
}

// NewReconciler requires durable observations, a clock and an alert sink.
func NewReconciler(store ReconcileStore, now func() time.Time, alert func(context.Context, Difference) error) (*Reconciler, error) {
	if store == nil || now == nil || alert == nil {
		return nil, errors.New("reconciliation dependencies required")
	}
	return &Reconciler{store: store, now: now, alert: alert}, nil
}

// Run recomputes a UTC service day from source facts, including zero-value count discrepancies.
func (s *Reconciler) Run(ctx context.Context, a Actor, date, trigger string) (ReconcileRun, error) {
	return s.run(ctx, a, date, trigger, "")
}

// Manual uses a durable command identity while committing each observation batch separately.
func (s *Reconciler) Manual(ctx context.Context, a Actor, date, key string) (ReconcileRun, error) {
	if key == "" || len(key) > 128 || a.UserID <= 0 {
		return ReconcileRun{}, Invalid()
	}
	return s.run(ctx, a, date, "manual", fingerprint(key))
}

func (s *Reconciler) run(ctx context.Context, a Actor, date, trigger, key string) (ReconcileRun, error) {
	day, err := time.Parse("2006-01-02", date)
	at := s.now().UTC()
	if err != nil || day.After(at) || a.TenantID <= 0 || (trigger != "daily" && trigger != "manual") {
		return ReconcileRun{}, Invalid()
	}
	run, err := s.store.BeginRun(ctx, ReconcileRun{TenantID: a.TenantID, ServiceDate: day, PostingDate: utcDay(at), CutoffAt: at, ActorID: a.UserID, Trigger: trigger, Status: "running", CommandKey: key})
	if err != nil {
		return run, err
	}
	if run.Status != "completed" {
		run, err = s.scan(ctx, run)
	}
	if err != nil {
		return run, err
	}
	return run, s.DeliverAlerts(ctx, run.TenantID)
}

func (s *Reconciler) scan(ctx context.Context, run ReconcileRun) (ReconcileRun, error) {
	for {
		facts, err := s.store.Facts(ctx, run, run.LastCallID)
		if err != nil {
			return run, err
		}
		if len(facts) == 0 {
			break
		}
		observations := []Difference{}
		for _, f := range facts {
			observations = append(observations, CheckCall(f, run.CutoffAt.Unix())...)
		}
		run.LastCallID = facts[len(facts)-1].CallID
		if err := s.store.Observe(ctx, run, observations, run.LastCallID, int64(len(facts))); err != nil {
			return run, err
		}
	}
	controls, err := s.store.Controls(ctx, run)
	if err != nil {
		return run, err
	}
	observations := []Difference{}
	for _, c := range controls {
		if c.Expected != c.Actual {
			observations = append(observations, Difference{AppID: c.AppID, Currency: c.Currency, Kind: c.Kind, Expected: c.Expected, Actual: c.Actual, ReferenceID: c.ReferenceID})
		}
	}
	if err := s.store.Observe(ctx, run, observations, run.LastCallID, 0); err != nil {
		return run, err
	}
	return s.store.CompleteRun(ctx, run, s.now().UTC())
}

// DeliverAlerts retries every unacknowledged observation without clearing open differences.
func (s *Reconciler) DeliverAlerts(ctx context.Context, tenant int64) error {
	for after := int64(0); ; {
		rows, err := s.store.PendingAlerts(ctx, tenant, after)
		if err != nil {
			return err
		}
		if len(rows) == 0 {
			return nil
		}
		for _, d := range rows {
			if err := s.alert(ctx, d); err != nil {
				return err
			}
			if err := s.store.AcknowledgeAlert(ctx, d.ID); err != nil {
				return err
			}
			after = d.ID
		}
	}
}

// Differences lists dated observations through the caller's application ownership scope.
func (s *Reconciler) Differences(ctx context.Context, a Actor, date string) ([]Difference, error) {
	return s.store.Differences(ctx, a, date)
}

// Compensate restores provable missing charge evidence and records an immutable credential.
func (s *Reconciler) Compensate(ctx context.Context, a Actor, id int64, reason, key string) (Compensation, error) {
	if a.UserID <= 0 || id <= 0 || len(reason) == 0 || len(reason) > 500 || key == "" || len(key) > 128 {
		return Compensation{}, Invalid()
	}
	return s.store.Compensate(ctx, a, id, reason, fingerprint(a, key), s.now().UTC())
}

// Compensations exposes evidence only for a difference owned by the caller.
func (s *Reconciler) Compensations(ctx context.Context, a Actor, id int64) ([]Compensation, error) {
	return s.store.Compensations(ctx, a, id)
}

func utcDay(at time.Time) time.Time {
	at = at.UTC()
	return time.Date(at.Year(), at.Month(), at.Day(), 0, 0, 0, 0, time.UTC)
}
