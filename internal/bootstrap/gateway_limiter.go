package bootstrap

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/omwk/go-admin-api/internal/kernel/config"
	"github.com/omwk/go-admin-api/internal/modules/gateway"
	"github.com/redis/go-redis/v9"
)

// Redis TIME gives every process the same refill clock. Rejection writes no bucket.
const gatewayBucketScript = `
local clock = redis.call('TIME')
local now = tonumber(clock[1]) + tonumber(clock[2]) / 1000000
local buckets = {}
local blocked, longest, selected, ratio = false, 0, 1, 2
for i,key in ipairs(KEYS) do
  local rate, burst = tonumber(ARGV[i*2-1]), tonumber(ARGV[i*2])
  local state = redis.call('HMGET',key,'tokens','last_refill')
  local tokens, last = tonumber(state[1]), tonumber(state[2])
  if (state[1] and not tokens) or (state[2] and not last) then
    return redis.error_reply('invalid gateway bucket')
  end
  tokens = math.min(burst,(tokens or burst) + math.max(0,now-(last or now))*rate)
  buckets[i] = {tokens,burst,rate}
  if tokens < 1 then
    local wait = (1-tokens)/rate
    if not blocked or wait > longest then selected,longest = i,wait end
    blocked = true
  elseif not blocked and (tokens-1)/burst < ratio then
    selected,ratio = i,(tokens-1)/burst
  end
end
if blocked then
  return {0,selected,buckets[selected][2],math.floor(buckets[selected][1]),math.ceil(longest*1000)}
end
for i,key in ipairs(KEYS) do
  redis.call('HSET',key,'tokens',buckets[i][1]-1,'last_refill',now)
  redis.call('PEXPIRE',key,math.max(1000,math.ceil(2*buckets[i][2]/buckets[i][3]*1000)))
end
return {1,selected,buckets[selected][2],math.floor(buckets[selected][1]-1),0}
`

type gatewayLimiter struct {
	client *redis.Client
	policy config.Gateway
}

func newGatewayLimiter(client *redis.Client, policy config.Gateway) (*gatewayLimiter, error) {
	if policy == (config.Gateway{}) {
		policy = config.DefaultGateway()
	}
	if client == nil {
		return nil, errors.New("gateway rate limiter Redis required")
	}
	for _, value := range []int{policy.GlobalQPS, policy.GlobalBurst, policy.ClientQPS, policy.ClientBurst, policy.RouteQPS, policy.RouteBurst} {
		if value <= 0 || value > 1000000000 {
			return nil, errors.New("gateway rate policy must be positive and bounded")
		}
	}
	return &gatewayLimiter{client: client, policy: policy}, nil
}

func (l *gatewayLimiter) Take(ctx context.Context, p gateway.Principal, route int64) (gateway.RateDecision, error) {
	if p.TenantID <= 0 || p.ClientID <= 0 {
		return gateway.RateDecision{}, gateway.Unavailable()
	}
	client := "gateway:rl:client:" + strconv.FormatInt(p.TenantID, 10) + ":" + strconv.FormatInt(p.ClientID, 10)
	keys := []string{"gateway:rl:global", client}
	kinds := []string{"global", "client"}
	args := []any{l.policy.GlobalQPS, l.policy.GlobalBurst, l.policy.ClientQPS, l.policy.ClientBurst}
	if route > 0 {
		routeKey := strconv.FormatInt(p.TenantID, 10) + ":" + strconv.FormatInt(route, 10)
		keys = append(keys, "gateway:rl:route:"+routeKey, client+":route:"+strconv.FormatInt(route, 10))
		kinds = append(kinds, "route", "client_route")
		args = append(args, l.policy.RouteQPS, l.policy.RouteBurst, l.policy.RouteQPS, l.policy.RouteBurst)
	}
	bounded, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	values, err := l.client.Eval(bounded, gatewayBucketScript, keys, args...).Int64Slice()
	if err != nil || len(values) != 5 || values[1] < 1 || values[1] > int64(len(kinds)) {
		return gateway.RateDecision{}, gateway.Unavailable()
	}
	decision := gateway.RateDecision{LimitType: kinds[values[1]-1], Limit: values[2], Remaining: values[3], RetryAfter: time.Duration(values[4]) * time.Millisecond}
	if values[0] == 0 {
		return decision, &gateway.LimitError{Decision: decision}
	}
	return decision, nil
}

var _ gateway.RateLimiter = (*gatewayLimiter)(nil)
