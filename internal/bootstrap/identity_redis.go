package bootstrap

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"time"

	"github.com/omwk/go-admin-api/internal/kernel/cache"
	"github.com/omwk/go-admin-api/internal/kernel/config"
	"github.com/redis/go-redis/v9"
)

var retainRevocationScript = redis.NewScript(`
local current = redis.call('PTTL', KEYS[1])
if current == -1 then return 1 end
if current < tonumber(ARGV[1]) then
  redis.call('SET', KEYS[1], '1', 'PX', ARGV[1])
end
return 1
`)

type authRedis struct {
	cache   *cache.Cache
	markers *redis.Client
}

func newAuthRedis(ctx context.Context, shared *cache.Cache, cfg config.Redis) (*authRedis, error) {
	client := redis.NewClient(&redis.Options{
		Addr: cfg.Addr, Password: cfg.Password, DB: cfg.DB, PoolSize: cfg.PoolSize, MinIdleConns: cfg.MinIdleConns,
		DialTimeout: time.Second, ReadTimeout: time.Second, WriteTimeout: time.Second, PoolTimeout: time.Second,
		MaxRetries: -1, ContextTimeoutEnabled: true,
	})
	adapter := &authRedis{cache: shared, markers: client}
	if err := adapter.Ping(ctx); err != nil {
		return nil, errors.Join(err, client.Close())
	}
	return adapter, nil
}

func (r *authRedis) Ping(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	if err := r.markers.Ping(ctx).Err(); err != nil {
		return errors.New("authentication Redis unavailable")
	}
	return nil
}

func (r *authRedis) AllowLogin(ctx context.Context, ip string) (bool, error) {
	hash := sha256.Sum256([]byte(ip))
	allowed, _, _, err := r.cache.TokenBucket(ctx, "auth:v1:login:ip:sha256:"+hex.EncodeToString(hash[:]), 1.0/6, 10)
	return allowed, err
}

// AllowAccount gives each purpose and hashed dimension an independent five-request budget.
func (r *authRedis) AllowAccount(ctx context.Context, purpose, value string) (bool, error) {
	hash := sha256.Sum256([]byte(value))
	allowed, _, _, err := r.cache.TokenBucket(ctx, "auth:v1:account:"+purpose+":sha256:"+hex.EncodeToString(hash[:]), 1.0/120, 5)
	return allowed, err
}

func (r *authRedis) Revoked(ctx context.Context, key string) (bool, error) {
	_, found, err := r.cache.Get(ctx, key)
	return found, err
}

func (r *authRedis) Mark(ctx context.Context, key string, expires, now time.Time) error {
	ttl := expires.Sub(now)
	if ttl <= 0 {
		return nil
	}
	milliseconds := ttl.Milliseconds()
	if ttl%time.Millisecond != 0 {
		milliseconds++
	}
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	if err := retainRevocationScript.Run(ctx, r.markers, []string{key}, milliseconds).Err(); err != nil {
		return errors.New("write authentication revocation failed")
	}
	return nil
}
