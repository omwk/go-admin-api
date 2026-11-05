package cache

import (
	"context"
	"errors"
	"time"

	"github.com/omwk/go-admin-api/internal/kernel/config"
	"github.com/redis/go-redis/v9"
)

// Cache owns the Redis connection pool.
type Cache struct {
	client    *redis.Client
	opTimeout time.Duration
}

// Open configures the pool and checks Redis before returning it.
func Open(ctx context.Context, cfg config.Redis, budgets ...config.Cache) (*Cache, error) {
	if cfg.Addr == "" || cfg.PoolSize <= 0 || cfg.MinIdleConns < 0 || cfg.MinIdleConns > cfg.PoolSize {
		return nil, errors.New("redis address or pool settings are invalid")
	}
	budget := config.Cache{}
	if len(budgets) > 0 {
		budget = budgets[0]
	}
	timeout := budget.OperationTimeout()
	c := &Cache{opTimeout: timeout, client: redis.NewClient(&redis.Options{
		Addr: cfg.Addr, Password: cfg.Password, DB: cfg.DB,
		PoolSize: cfg.PoolSize, MinIdleConns: cfg.MinIdleConns,
		DialTimeout: timeout, ReadTimeout: timeout, WriteTimeout: timeout,
		PoolTimeout: timeout, MaxRetries: -1, ContextTimeoutEnabled: true,
	})}
	if err := c.Ping(ctx); err != nil {
		return nil, errors.Join(err, c.Close())
	}
	return c, nil
}

// Ping checks Redis within the configured operation budget.
func (c *Cache) Ping(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, c.opTimeout)
	defer cancel()
	if err := c.client.Ping(ctx).Err(); err != nil {
		return errors.New("redis ping failed")
	}
	return nil
}

// Get returns a cached value and whether the key exists.
func (c *Cache) Get(ctx context.Context, key string) (string, bool, error) {
	if key == "" {
		return "", false, errors.New("redis key is required")
	}
	ctx, cancel := context.WithTimeout(ctx, c.opTimeout)
	defer cancel()
	value, err := c.client.Get(ctx, key).Result()
	if errors.Is(err, redis.Nil) {
		return "", false, nil
	}
	if err != nil {
		return "", false, errors.New("redis get failed")
	}
	return value, true, nil
}

// Set stores a value; ttl zero means the key has no expiration.
func (c *Cache) Set(ctx context.Context, key string, value string, ttl time.Duration) error {
	if key == "" || ttl < 0 || (ttl > 0 && ttl < time.Millisecond) {
		return errors.New("redis key and valid ttl are required")
	}
	ctx, cancel := context.WithTimeout(ctx, c.opTimeout)
	defer cancel()
	if err := c.client.Set(ctx, key, value, ttl).Err(); err != nil {
		return errors.New("redis set failed")
	}
	return nil
}

// Del removes a key and returns the number of keys removed.
func (c *Cache) Del(ctx context.Context, key string) (int64, error) {
	if key == "" {
		return 0, errors.New("redis key is required")
	}
	ctx, cancel := context.WithTimeout(ctx, c.opTimeout)
	defer cancel()
	count, err := c.client.Del(ctx, key).Result()
	if err != nil {
		return 0, errors.New("redis delete failed")
	}
	return count, nil
}

// Incr increments an integer key, creating it with value one when absent.
func (c *Cache) Incr(ctx context.Context, key string) (int64, error) {
	if key == "" {
		return 0, errors.New("redis key is required")
	}
	ctx, cancel := context.WithTimeout(ctx, c.opTimeout)
	defer cancel()
	value, err := c.client.Incr(ctx, key).Result()
	if err != nil {
		return 0, errors.New("redis increment failed")
	}
	return value, nil
}

// Expire changes a key's lifetime. Non-positive TTLs are rejected without a write.
func (c *Cache) Expire(ctx context.Context, key string, ttl time.Duration) (bool, error) {
	if key == "" || ttl < time.Millisecond {
		return false, errors.New("redis key and positive millisecond ttl are required")
	}
	ctx, cancel := context.WithTimeout(ctx, c.opTimeout)
	defer cancel()
	exists, err := c.client.PExpire(ctx, key, ttl).Result()
	if err != nil {
		return false, errors.New("redis expire failed")
	}
	return exists, nil
}

// Close releases the Redis pool.
func (c *Cache) Close() error { return c.client.Close() }
