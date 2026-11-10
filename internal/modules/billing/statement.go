package billing

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"hash"
	"math"
	"strings"
	"time"

	"github.com/oklog/ulid/v2"
	"github.com/omwk/go-admin-api/internal/kernel/errs"
)

// StatementService rebuilds drafts and freezes reviewed consumption evidence.
type StatementService struct {
	store StatementStore
	now   func() time.Time
}

// NewStatementService requires a durable store and an explicit UTC clock.
func NewStatementService(store StatementStore, now func() time.Time) (*StatementService, error) {
	if store == nil || now == nil {
		return nil, errors.New("statement dependencies required")
	}
	return &StatementService{store: store, now: now}, nil
}

// Generate never changes an issued period or bills the same source twice.
func (s *StatementService) Generate(ctx context.Context, a Actor, app, month string) (Statement, error) {
	start, err := time.Parse("2006-01", month)
	if err != nil || start.After(s.now().UTC()) || a.UserID <= 0 {
		return Statement{}, Invalid()
	}
	if _, err := ulid.ParseStrict(app); err != nil {
		return Statement{}, Invalid()
	}
	var out Statement
	err = s.store.Transaction(ctx, func(tx context.Context) error {
		var err error
		out, err = s.store.BeginDraft(tx, a, app, start, s.now().UTC())
		if err != nil {
			return err
		}
		if out.Status != "draft" {
			return TerminalConflict()
		}
		if err := s.store.ClearLines(tx, out); err != nil {
			return err
		}
		out.CutoffAt = s.now().UTC()
		out.TotalCents, out.TotalUnits, out.SourceHash, err = s.scanSources(tx, out, true)
		if err != nil {
			return err
		}
		if err := s.store.SaveDraft(tx, &out); err != nil {
			return err
		}
		return s.store.AuditStatement(tx, a, out, "billing.statement.generate")
	})
	return out, err
}

func (s *StatementService) scanSources(ctx context.Context, st Statement, save bool) (int64, int64, string, error) {
	var cents, units int64
	digest := sha256.New()
	for after := ""; ; {
		rows, err := s.store.SourceLines(ctx, st, after)
		if err != nil {
			return 0, 0, "", err
		}
		if len(rows) == 0 {
			break
		}
		for _, line := range rows {
			var ok bool
			cents, ok = statementAdd(cents, line.AmountCents)
			if !ok {
				return 0, 0, "", Invalid()
			}
			units, ok = statementAdd(units, line.Units)
			if !ok || units < 0 {
				return 0, 0, "", Invalid()
			}
			if err := hashStatementLine(digest, line); err != nil {
				return 0, 0, "", err
			}
		}
		if save {
			if err := s.store.InsertLines(ctx, st, rows); err != nil {
				return 0, 0, "", err
			}
		}
		after = rows[len(rows)-1].SourceKey
	}
	return cents, units, hex.EncodeToString(digest.Sum(nil)), nil
}

func hashStatementLine(digest hash.Hash, line StatementLine) error {
	line.ID, line.TenantID, line.StatementID = 0, 0, 0
	raw, err := json.Marshal(line)
	if err != nil {
		return Unavailable()
	}
	if _, err := digest.Write(raw); err != nil {
		return Unavailable()
	}
	return nil
}

func statementAdd(a, b int64) (int64, bool) {
	if b > 0 && a > math.MaxInt64-b || b < 0 && a < math.MinInt64-b {
		return 0, false
	}
	return a + b, true
}

// Issue requires current reconciliation coverage and an unchanged source set.
func (s *StatementService) Issue(ctx context.Context, a Actor, id int64, version int, reason string) (Statement, error) {
	if id <= 0 || version <= 0 || strings.TrimSpace(reason) == "" || len(reason) > 500 {
		return Statement{}, Invalid()
	}
	var out Statement
	err := s.store.Transaction(ctx, func(tx context.Context) error {
		var err error
		out, err = s.store.OwnedStatement(tx, a, id)
		if err != nil {
			return err
		}
		if out.Status != "draft" {
			return TerminalConflict()
		}
		if out.Version != version {
			return errs.New(10409, 409, "资源版本冲突")
		}
		if out.PeriodEnd.After(s.now().UTC()) {
			return TerminalConflict()
		}
		cents, units, source, err := s.scanSources(tx, out, false)
		if err != nil {
			return err
		}
		if cents != out.TotalCents || units != out.TotalUnits || source != out.SourceHash {
			return TerminalConflict()
		}
		if err := s.store.ReadyToIssue(tx, out); err != nil {
			return err
		}
		return s.store.MarkIssued(tx, &out, a, reason, s.now().UTC())
	})
	return out, err
}

// Detail exposes one bounded page of the independent financial evidence.
func (s *StatementService) Detail(ctx context.Context, a Actor, id, after int64, size int) (StatementDetail, error) {
	if id <= 0 || after < 0 {
		return StatementDetail{}, Invalid()
	}
	f, err := normalizeStatementFilter(StatementFilter{PageSize: size})
	if err != nil {
		return StatementDetail{}, err
	}
	st, err := s.store.OwnedStatement(ctx, a, id)
	if err != nil {
		return StatementDetail{}, err
	}
	lines, err := s.store.StatementLines(ctx, st, after, f.PageSize+1)
	out := StatementDetail{Statement: st, Lines: lines}
	if len(lines) > f.PageSize {
		out.Lines = lines[:f.PageSize]
		out.NextCursor = out.Lines[f.PageSize-1].ID
	}
	return out, err
}

// List returns only bills belonging to applications owned by the actor.
func (s *StatementService) List(ctx context.Context, a Actor, f StatementFilter) (StatementPage, error) {
	f, err := normalizeStatementFilter(f)
	if err != nil {
		return StatementPage{}, err
	}
	return s.store.Statements(ctx, a, f)
}

func normalizeStatementFilter(f StatementFilter) (StatementFilter, error) {
	if f.Page == 0 {
		f.Page = 1
	}
	if f.PageSize == 0 {
		f.PageSize = 20
	}
	if f.Page < 1 || f.PageSize < 1 || f.PageSize > 100 || f.Page > 1000000 {
		return f, Invalid()
	}
	return f, nil
}
