package cache

import (
	"context"
	"errors"
	"time"

	"github.com/omwk/go-admin-api/internal/kernel/config"
	"github.com/redis/go-redis/v9"
)

// Cache owns the Redis connection pool.
type Cache struct{ client *redis.Client }

// Open configures the pool and checks Redis before returning it.
func Open(ctx context.Context, cfg config.Redis) (*Cache, error) {
	if cfg.Addr == "" || cfg.PoolSize <= 0 || cfg.MinIdleConns < 0 || cfg.MinIdleConns > cfg.PoolSize {
		return nil, errors.New("redis address or pool settings are invalid")
	}
	c := &Cache{client: redis.NewClient(&redis.Options{
		Addr: cfg.Addr, Password: cfg.Password, DB: cfg.DB,
		PoolSize: cfg.PoolSize, MinIdleConns: cfg.MinIdleConns,
		DialTimeout: time.Second, ReadTimeout: time.Second, WriteTimeout: time.Second,
		PoolTimeout: time.Second, MaxRetries: -1, ContextTimeoutEnabled: true,
	})}
	if err := c.Ping(ctx); err != nil {
		return nil, errors.Join(err, c.Close())
	}
	return c, nil
}

// Ping checks Redis with at most a one-second deadline.
func (c *Cache) Ping(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	if err := c.client.Ping(ctx).Err(); err != nil {
		return errors.New("redis ping failed")
	}
	return nil
}

// Close releases the Redis pool.
func (c *Cache) Close() error { return c.client.Close() }
