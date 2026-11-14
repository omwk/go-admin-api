package bootstrap

import (
	"context"
	"errors"
	"net/url"
	"time"

	"github.com/omwk/go-admin-api/internal/modules/identity"
	"github.com/redis/go-redis/v9"
)

const tenantLoginFailureLimit = 100

var countLoginFailureScript = redis.NewScript(`
local account = redis.call('INCR', KEYS[1])
if redis.call('PTTL', KEYS[1]) < 0 then redis.call('PEXPIRE', KEYS[1], ARGV[1]) end
local tenant = redis.call('INCR', KEYS[2])
if redis.call('PTTL', KEYS[2]) < 0 then redis.call('PEXPIRE', KEYS[2], 60000) end
return account
`)

var _ identity.LoginLimits = (*authRedis)(nil)

func loginFailureKey(code, account string) string {
	return "login:fail:" + url.QueryEscape(code) + ":" + url.QueryEscape(account)
}

func loginTenantRateKey(code string) string { return "login:rate:" + url.QueryEscape(code) }

func (r *authRedis) AllowTenantLogin(ctx context.Context, code string) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	count, err := r.markers.Get(ctx, loginTenantRateKey(code)).Int()
	if errors.Is(err, redis.Nil) {
		return true, nil
	}
	if err != nil {
		return false, errors.New("read tenant login limit failed")
	}
	return count < tenantLoginFailureLimit, nil
}

func (r *authRedis) RecordLoginFailure(ctx context.Context, code, account string, window time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	if err := countLoginFailureScript.Run(ctx, r.markers, []string{loginFailureKey(code, account), loginTenantRateKey(code)}, window.Milliseconds()).Err(); err != nil {
		return errors.New("write login failure counters failed")
	}
	return nil
}

func (r *authRedis) ClearLoginFailures(ctx context.Context, code, account string) error {
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	if err := r.markers.Del(ctx, loginFailureKey(code, account)).Err(); err != nil {
		return errors.New("clear login failure counter failed")
	}
	return nil
}
