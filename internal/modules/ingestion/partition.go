package ingestion

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// Month is a validated UTC calendar month; its zero value is invalid.
type Month struct {
	start time.Time
	valid bool
}

// ParseMonth accepts exactly YYYY-MM, excluding years outside PostgreSQL's
// supported application range and the terminal month without a valid upper bound.
func ParseMonth(value string) (Month, error) {
	start, err := time.Parse("2006-01", value)
	if err != nil || start.Year() < 1 || value >= "9999-12" {
		return Month{}, errors.New("invalid partition month")
	}
	return Month{start: start, valid: true}, nil
}

// String returns the SQL maintenance function's validated month parameter.
func (m Month) String() string { return m.start.Format("2006-01") }

// Bounds returns the inclusive lower and exclusive upper bounds in UTC.
func (m Month) Bounds() (time.Time, time.Time) { return m.start, m.start.AddDate(0, 1, 0) }

// Valid reports whether this value was constructed by ParseMonth.
func (m Month) Valid() bool { return m.valid }

// PartitionStore is deliberately separate from raw persistence and uses a
// maintenance login. An application login must not have schema CREATE rights.
type PartitionStore interface {
	Ensure(context.Context, Month) (bool, error)
	Covered(context.Context, Month) (bool, error)
}

// PartitionAlert reports a missing or unverifiable month without payload data.
type PartitionAlert func(context.Context, Month, error)

// PartitionMaintainer checks startup coverage and prebuilds two future months.
type PartitionMaintainer struct {
	store PartitionStore
	alert PartitionAlert
}

// NewPartitionMaintainer requires a maintenance port and a coverage alarm sink.
func NewPartitionMaintainer(store PartitionStore, alert PartitionAlert) (*PartitionMaintainer, error) {
	if store == nil || alert == nil {
		return nil, errors.New("partition store and alert are required")
	}
	return &PartitionMaintainer{store: store, alert: alert}, nil
}

// Maintain repairs the current UTC month and the next two, and verifies their
// attached bounds. It attempts every month even if one fails.
func (m *PartitionMaintainer) Maintain(ctx context.Context, now time.Time) error {
	utc := now.UTC()
	start := time.Date(utc.Year(), utc.Month(), 1, 0, 0, 0, 0, time.UTC)
	var failures []error
	for offset := 0; offset <= 2; offset++ {
		month, err := ParseMonth(start.AddDate(0, offset, 0).Format("2006-01"))
		if err != nil {
			return err
		}
		if err := m.maintainMonth(ctx, month); err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}

func (m *PartitionMaintainer) maintainMonth(ctx context.Context, month Month) error {
	covered, err := m.store.Covered(ctx, month)
	if err != nil || !covered {
		m.alert(ctx, month, errors.Join(ErrPartitionMissing, err))
	}
	if _, err := m.store.Ensure(ctx, month); err != nil {
		m.alert(ctx, month, err)
		return fmt.Errorf("ensure raw partition %s: %w", month, err)
	}
	covered, err = m.store.Covered(ctx, month)
	if err != nil || !covered {
		failure := errors.Join(ErrPartitionMissing, err)
		m.alert(ctx, month, failure)
		return fmt.Errorf("verify raw partition %s: %w", month, failure)
	}
	return nil
}

// Run repairs coverage immediately and then at each UTC midnight. Failures are
// returned so the process supervisor can alert/restart rather than silently skip.
func (m *PartitionMaintainer) Run(ctx context.Context) error {
	for {
		if err := m.Maintain(ctx, time.Now()); err != nil {
			return err
		}
		now := time.Now().UTC()
		next := time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, time.UTC)
		timer := time.NewTimer(next.Sub(now))
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}
