package db

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestQueryTimeoutBudgetAndEarlierParentDeadline(t *testing.T) {
	for _, budget := range []time.Duration{0, 15 * time.Second} {
		database := dryRunDB(t)
		database.config.TxnTimeout = budget
		want := budget
		if want == 0 {
			want = 3 * time.Second
		}
		before := time.Now()
		query, cancel := database.WithContext(context.Background())
		deadline, ok := query.Statement.Context.Deadline()
		require.True(t, ok)
		require.False(t, deadline.Before(before.Add(want)))
		require.False(t, deadline.After(time.Now().Add(want)))
		cancel()
		require.ErrorIs(t, query.Statement.Context.Err(), context.Canceled)
		parent, stop := context.WithTimeout(context.Background(), time.Second)
		parentDeadline, _ := parent.Deadline()
		query, cancel = database.WithContext(parent)
		deadline, ok = query.Statement.Context.Deadline()
		require.True(t, ok)
		require.Equal(t, parentDeadline, deadline)
		cancel()
		require.NoError(t, parent.Err())
		stop()
	}
}
