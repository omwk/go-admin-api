package cache

import (
	"context"
	"errors"
	"math"
	"time"

	"github.com/redis/go-redis/v9"
)

var bucketScript = redis.NewScript(`
local clock = redis.call('TIME')
local now = tonumber(clock[1]) * 1000 + math.floor(tonumber(clock[2]) / 1000)
local rate, burst = tonumber(ARGV[1]), tonumber(ARGV[2])
local state = redis.call('HMGET', KEYS[1], 'tokens', 'last_refill')
local tokens, last = tonumber(state[1]) or burst, tonumber(state[2]) or now
now = math.max(now, last)
tokens = math.min(burst, tokens + (now - last) * rate / 1000)
local allowed, retry = 0, 0
if tokens >= 1 then
  allowed, tokens = 1, tokens - 1
else
  retry = math.ceil((1 - tokens) * 1000 / rate)
end
redis.call('HSET', KEYS[1], 'tokens', tokens, 'last_refill', now, 'rate', rate, 'burst', burst)
redis.call('PEXPIRE', KEYS[1], math.max(1, math.ceil(burst * 1000 / rate)))
return {allowed, math.floor(tokens), retry}
`)

// TokenBucket spends one token using the Redis clock and one atomic Lua script.
// Rate is tokens per second; retryAfter is zero for accepted requests.
func (c *Cache) TokenBucket(ctx context.Context, key string, rate float64, burst int) (allowed bool, remaining int, retryAfter time.Duration, err error) {
	if key == "" || rate <= 0 || math.IsNaN(rate) || math.IsInf(rate, 0) || burst < 1 {
		return false, 0, 0, errors.New("bucket key, positive rate and burst are required")
	}
	if float64(burst)/rate > (24 * time.Hour).Seconds() {
		return false, 0, 0, errors.New("bucket refill period must not exceed 24 hours")
	}
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	values, err := bucketScript.Run(ctx, c.client, []string{key}, rate, burst).Int64Slice()
	if err != nil {
		return false, 0, 0, errors.New("redis token bucket failed")
	}
	if len(values) != 3 {
		return false, 0, 0, errors.New("invalid redis bucket response")
	}
	return values[0] == 1, int(values[1]), time.Duration(values[2]) * time.Millisecond, nil
}
