package billing

import (
	"context"
	"strings"

	"github.com/oklog/ulid/v2"
)

// SavePlan validates an offer before its versioned, audited update.
func (s *Service) SavePlan(ctx context.Context, a Actor, p Plan) (Plan, error) {
	if strings.TrimSpace(p.Code) == "" || len(p.Code) > 64 || strings.TrimSpace(p.Name) == "" || len(p.Name) > 200 || p.PriceCents <= 0 || p.UnitCount <= 0 || !validQuota(p.QuotaType) || p.ValidDays < 0 || p.ValidDays > 36500 || p.RouteID < 0 || p.Currency != Currency || (p.Status != "active" && p.Status != "disabled") {
		return Plan{}, Invalid()
	}
	return s.store.SavePlan(ctx, a, p)
}

// Plans lists the tenant's offers without modifying sold terms.
func (s *Service) Plans(ctx context.Context, a Actor) ([]Plan, error) { return s.store.Plans(ctx, a) }

// Grant atomically purchases or gifts units once per human command identity.
func (s *Service) Grant(ctx context.Context, a Actor, in GrantInput, key string) (Entitlement, error) {
	if _, err := ulid.ParseStrict(in.AppID); err != nil {
		return Entitlement{}, Invalid()
	}
	if a.TenantID <= 0 || a.UserID <= 0 || key == "" || len(key) > 128 || in.AutoRenew || in.PlanID < 0 {
		return Entitlement{}, Invalid()
	}
	if in.PlanID == 0 && (in.Units <= 0 || !validQuota(in.QuotaType) || (in.Kind != "free" && in.Kind != "paid") || in.RouteID < 0 || strings.TrimSpace(in.SourceRef) == "" || len(in.SourceRef) > 200) {
		return Entitlement{}, Invalid()
	}
	hash := fingerprint(in)
	at := s.now().UTC()
	if in.StartsAt.IsZero() {
		in.StartsAt = at
	}
	var out Entitlement
	err := s.store.Transaction(ctx, func(tx context.Context) error {
		var err error
		out, err = s.store.Grant(tx, a, in, fingerprint(a, key), hash, at)
		return err
	})
	return out, err
}

// Entitlements lists the actor's application grants, including expired history.
func (s *Service) Entitlements(ctx context.Context, a Actor, app string) ([]Entitlement, error) {
	return s.store.Entitlements(ctx, a, app)
}

// UnitTxns exposes the immutable evidence of an owned entitlement.
func (s *Service) UnitTxns(ctx context.Context, a Actor, id int64) ([]UnitTxn, error) {
	return s.store.UnitTxns(ctx, a, id)
}

// Expire removes only unreserved expired units, retaining legal in-flight reservations.
func (s *Service) Expire(ctx context.Context) (int, error) {
	var count int
	err := s.store.Transaction(ctx, func(tx context.Context) error {
		var err error
		count, err = s.store.ExpireUnits(tx, s.now().UTC())
		return err
	})
	return count, err
}

// Receipt reads the original one-source charge snapshot.
func (s *Service) Receipt(ctx context.Context, tenant int64, call string) (Charge, error) {
	return s.store.Charge(ctx, tenant, call)
}

func validQuota(kind string) bool { return kind == "calls" || kind == "units" }
