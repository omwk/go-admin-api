package bootstrap

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

var registrationBudgetScript = redis.NewScript(`
local current = tonumber(redis.call('GET', KEYS[1]) or '0')
if current >= tonumber(ARGV[1]) then return 0 end
local next = redis.call('INCR', KEYS[1])
if next == 1 then redis.call('PEXPIRE', KEYS[1], 3600000) end
return 1
`)

func (r *authRedis) AllowRegistration(ctx context.Context, tenant int64, limit int) (bool, error) {
	if tenant <= 0 || limit < 1 {
		return false, errors.New("invalid registration budget")
	}
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	allowed, err := registrationBudgetScript.Run(ctx, r.markers, []string{"auth:v1:registration:tenant:" + strconv.FormatInt(tenant, 10)}, limit).Int()
	if err != nil {
		return false, errors.New("registration rate limit unavailable")
	}
	return allowed == 1, nil
}
