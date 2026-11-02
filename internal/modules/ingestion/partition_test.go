package ingestion

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type partitionTestStore struct {
	ensured []string
	covered bool
	broken  bool
	err     error
}

func (s *partitionTestStore) Ensure(_ context.Context, month Month) (bool, error) {
	s.ensured = append(s.ensured, month.String())
	if s.err != nil {
		return false, s.err
	}
	s.covered = !s.broken
	return true, nil
}

func (s *partitionTestStore) Covered(context.Context, Month) (bool, error) {
	return s.covered, nil
}

func TestMonthValidationAndBounds(t *testing.T) {
	require.False(t, Month{}.Valid())
	for _, value := range []string{"0001-01", "2040-02", "2040-12", "9999-11"} {
		month, err := ParseMonth(value)
		require.NoError(t, err)
		require.True(t, month.Valid())
		require.Equal(t, value, month.String())
		start, end := month.Bounds()
		require.Equal(t, time.UTC, start.Location())
		require.Equal(t, 1, start.Day())
		require.Equal(t, start.AddDate(0, 1, 0), end)
	}
	for _, value := range []string{"", "0000-01", "2040-2", "2040-00", "2040-13", "2040-02-01", "9999-12"} {
		_, err := ParseMonth(value)
		require.Error(t, err, value)
	}
}

func TestPartitionMaintainerFailuresAlertAndContinue(t *testing.T) {
	failure := errors.New("maintenance unavailable")
	for _, tc := range []struct {
		name  string
		store partitionTestStore
		want  error
	}{
		{"ensure", partitionTestStore{err: failure}, failure},
		{"still missing", partitionTestStore{broken: true}, ErrPartitionMissing},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var alerts []error
			maintainer, err := NewPartitionMaintainer(&tc.store, func(_ context.Context, _ Month, err error) { alerts = append(alerts, err) })
			require.NoError(t, err)
			err = maintainer.Maintain(context.Background(), time.Date(2040, 12, 31, 0, 0, 0, 0, time.UTC))
			require.ErrorIs(t, err, tc.want)
			require.Equal(t, []string{"2040-12", "2041-01", "2041-02"}, tc.store.ensured)
			require.Len(t, alerts, 6)
			require.ErrorIs(t, alerts[0], ErrPartitionMissing)
			require.ErrorIs(t, alerts[1], tc.want)
		})
	}
}

func TestPartitionMaintainerRunChecksBeforeWaitingAndCancels(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	store := &partitionTestStore{}
	maintainer, err := NewPartitionMaintainer(store, func(context.Context, Month, error) { cancel() })
	require.NoError(t, err)
	require.ErrorIs(t, maintainer.Run(ctx), context.Canceled)
	require.Len(t, store.ensured, 3)
	store = &partitionTestStore{covered: true}
	maintainer, err = NewPartitionMaintainer(store, func(context.Context, Month, error) { t.Fatal("unexpected alert") })
	require.NoError(t, err)
	// A canceled caller must return immediately instead of waiting for midnight.
	require.ErrorIs(t, maintainer.Run(ctx), context.Canceled)
}
