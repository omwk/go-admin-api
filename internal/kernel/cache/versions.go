package cache

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

var versionScript = redis.NewScript(`
local v = redis.call('GET', KEYS[1])
if v then return {v, 'existing'} end
redis.call('SET', KEYS[1], ARGV[1])
return {ARGV[1], 'fresh'}
`)

func randomVersion() (string, error) {
	var value [8]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", errors.New("generate authorization generation")
	}
	return strconv.FormatUint((binary.BigEndian.Uint64(value[:])>>4)+1, 10), nil
}

// Version atomically initializes a missing generation without a TTL; fresh reads bypass payloads.
func (c *Cache) Version(ctx context.Context, key string) (string, bool, error) {
	if key == "" {
		return "", false, errors.New("version key is required")
	}
	initial, err := randomVersion()
	if err != nil {
		return "", false, err
	}
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	values, err := versionScript.Run(ctx, c.client, []string{key}, initial).StringSlice()
	if err != nil || len(values) != 2 {
		return "", false, errors.New("read authorization generation failed")
	}
	if n, err := strconv.ParseInt(values[0], 10, 64); err != nil || n <= 0 {
		return "", false, errors.New("invalid authorization generation")
	}
	return values[0], values[1] == "fresh", nil
}

// IncrementResult preserves each key's outcome even when a pipeline partly fails.
type IncrementResult struct {
	Key     string
	Version int64
	Err     error
}

// IncrBatch issues at most 500 non-expiring version increments in one pipeline.
// SET NX precedes each INCR so an evicted version never restarts at a cached v0/v1.
func (c *Cache) IncrBatch(ctx context.Context, keys []string) []IncrementResult {
	results := make([]IncrementResult, len(keys))
	if len(keys) > 500 {
		for i, key := range keys {
			results[i] = IncrementResult{Key: key, Err: errors.New("version batch exceeds 500")}
		}
		return results
	}
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	pipeline := c.client.Pipeline()
	commands := make([]*redis.IntCmd, len(keys))
	initializers := make([]*redis.BoolCmd, len(keys))
	for i, key := range keys {
		results[i].Key = key
		initial, err := randomVersion()
		if err != nil || key == "" {
			results[i].Err = errors.New("invalid authorization increment")
			continue
		}
		initializers[i] = pipeline.SetNX(ctx, key, initial, 0)
		commands[i] = pipeline.Incr(ctx, key)
	}
	_, batchErr := pipeline.Exec(ctx)
	for i, command := range commands {
		if command == nil {
			continue
		}
		value, err := command.Result()
		if err == nil {
			err = initializers[i].Err()
		}
		if err != nil {
			results[i].Err = errors.New("authorization increment failed")
		} else {
			results[i].Version = value
		}
	}
	// Per-command results are authoritative; batchErr also accounts for canceled empty pipelines.
	if batchErr != nil && len(keys) == 0 {
		return nil
	}
	return results
}
