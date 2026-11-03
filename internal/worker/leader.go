package worker

import (
	"context"
	"errors"
	"time"

	"github.com/omwk/go-admin-api/internal/kernel/cache"
)

// Leader coordinates one scheduler process through a Redis lease.
type Leader struct {
	cache *cache.Cache
	key   string
	ttl   time.Duration
	lease *cache.Lease
}

func NewLeader(shared *cache.Cache, key string, ttl time.Duration) (*Leader, error) {
	if shared == nil || key == "" || ttl < time.Second {
		return nil, errors.New("leader lease settings are invalid")
	}
	return &Leader{cache: shared, key: key, ttl: ttl}, nil
}

func (l *Leader) Acquire(ctx context.Context) (bool, error) {
	lease, err := l.cache.Lock(ctx, l.key, l.ttl)
	if errors.Is(err, cache.ErrLocked) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	l.lease = lease
	return true, nil
}

func (l *Leader) Release(ctx context.Context) error {
	if l.lease == nil {
		return nil
	}
	_, err := l.lease.Release(ctx)
	l.lease = nil
	return err
}

// Renew periodically extends the current leader lease until ctx is cancelled.
func (l *Leader) Renew(ctx context.Context) error {
	if l == nil || l.lease == nil {
		return errors.New("leader lease is not held")
	}
	interval := l.ttl / 3
	if interval < time.Second {
		interval = time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			ok, err := l.lease.Renew(ctx, l.ttl)
			if err != nil {
				return err
			}
			if !ok {
				return errors.New("leader lease lost")
			}
		}
	}
}
