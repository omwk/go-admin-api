package cache

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"time"

	"github.com/redis/go-redis/v9"
)

// ErrLocked means another owner currently holds the lease.
var ErrLocked = errors.New("lock is already held")

var releaseScript = redis.NewScript(`
if redis.call('GET', KEYS[1]) == ARGV[1] then
  return redis.call('DEL', KEYS[1])
end
return 0
`)

var renewScript = redis.NewScript(`
if redis.call('GET', KEYS[1]) == ARGV[1] then
  return redis.call('PEXPIRE', KEYS[1], ARGV[2])
end
return 0
`)

// Lease identifies a lock owner without exposing its random ownership value.
type Lease struct {
	cache      *Cache
	key, owner string
}

// Lock acquires a lease atomically using SET NX PX.
func (c *Cache) Lock(ctx context.Context, key string, ttl time.Duration) (*Lease, error) {
	if key == "" || ttl < time.Millisecond {
		return nil, errors.New("lock key and positive millisecond ttl are required")
	}
	value := make([]byte, 32)
	if _, err := rand.Read(value); err != nil {
		return nil, errors.New("generate lock ownership value")
	}
	owner := base64.RawURLEncoding.EncodeToString(value)
	ctx, cancel := context.WithTimeout(ctx, c.opTimeout)
	defer cancel()
	err := c.client.Do(ctx, "SET", key, owner, "NX", "PX", ttl.Milliseconds()).Err()
	if errors.Is(err, redis.Nil) {
		return nil, ErrLocked
	}
	if err != nil {
		return nil, errors.New("acquire redis lock failed")
	}
	return &Lease{cache: c, key: key, owner: owner}, nil
}

// Release deletes the lease only when Redis still stores this owner's value.
func (l *Lease) Release(ctx context.Context) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, l.cache.opTimeout)
	defer cancel()
	n, err := releaseScript.Run(ctx, l.cache.client, []string{l.key}, l.owner).Int()
	if err != nil {
		return false, errors.New("release redis lock failed")
	}
	return n == 1, nil
}

// Renew extends the lease only while this owner still holds it.
func (l *Lease) Renew(ctx context.Context, ttl time.Duration) (bool, error) {
	if l == nil || ttl < time.Millisecond {
		return false, errors.New("lease and positive ttl are required")
	}
	ctx, cancel := context.WithTimeout(ctx, l.cache.opTimeout)
	defer cancel()
	n, err := renewScript.Run(ctx, l.cache.client, []string{l.key}, l.owner, ttl.Milliseconds()).Int()
	if err != nil {
		return false, errors.New("renew redis lock failed")
	}
	return n == 1, nil
}
