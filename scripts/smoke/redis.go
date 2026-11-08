package main

import (
	"context"
	"errors"
	"os"
	"slices"
	"strconv"
	"time"

	"github.com/hibiken/asynq"
	"github.com/redis/go-redis/v9"
)

const ownerKey = "s3:smoke:owner"

func redisOptions(database int) *redis.Options {
	return &redis.Options{Addr: os.Getenv("CH_REDIS_ADDR"), Password: os.Getenv("CH_REDIS_PASSWORD"), DB: database,
		DialTimeout: time.Second, ReadTimeout: time.Second, WriteTimeout: time.Second}
}

func reserveRedis(ctx context.Context) (any, error) {
	configured, err := strconv.Atoi(os.Getenv("CH_REDIS_DB"))
	if err != nil {
		return nil, err
	}
	for database := 1; database < 16; database++ {
		if database == configured {
			continue
		}
		c := redis.NewClient(redisOptions(database))
		// Atomically claim only a completely empty logical DB. Never flush Redis.
		claimed, claimErr := c.Eval(ctx, `if redis.call('DBSIZE') ~= 0 then return 0 end
redis.call('SET', KEYS[1], ARGV[1]); return 1`, []string{ownerKey}, os.Getenv("SMOKE_SCHEMA")).Int()
		closeErr := c.Close()
		if claimErr != nil || closeErr != nil {
			return nil, errors.Join(claimErr, closeErr)
		}
		if claimed == 1 {
			return map[string]any{"db": database, "claimed": true}, nil
		}
	}
	return nil, errors.New("no empty Redis logical DB; use an isolated Redis server")
}

func cleanRedis(ctx context.Context) (result any, err error) {
	database, err := strconv.Atoi(os.Getenv("CH_REDIS_DB"))
	if err != nil || database <= 0 {
		return nil, errors.New("invalid reserved Redis database")
	}
	c := redis.NewClient(redisOptions(database))
	defer func() { err = errors.Join(err, c.Close()) }()
	owner, err := c.Get(ctx, ownerKey).Result()
	if err != nil || owner != os.Getenv("SMOKE_SCHEMA") {
		return nil, errors.New("Redis ownership mismatch; refusing cleanup")
	}
	iter := c.Scan(ctx, 0, "*", 100).Iterator()
	for iter.Next(ctx) {
		if iter.Val() != ownerKey {
			if err := c.Del(ctx, iter.Val()).Err(); err != nil {
				return nil, err
			}
		}
	}
	if err := iter.Err(); err != nil {
		return nil, err
	}
	if err := c.Del(ctx, ownerKey).Err(); err != nil {
		return nil, err
	}
	remaining, err := c.DBSize(ctx).Result()
	return map[string]any{"remaining_keys": remaining}, err
}

func observeWorker() (result any, err error) {
	database, err := strconv.Atoi(os.Getenv("CH_REDIS_DB"))
	if err != nil {
		return nil, err
	}
	pid, err := strconv.Atoi(os.Getenv("SMOKE_WORKER_PID"))
	if err != nil {
		return nil, err
	}
	opts := redisOptions(database)
	i := asynq.NewInspector(asynq.RedisClientOpt{Addr: opts.Addr, Password: opts.Password, DB: database,
		DialTimeout: time.Second, ReadTimeout: time.Second, WriteTimeout: time.Second})
	defer func() { err = errors.Join(err, i.Close()) }()
	servers, err := i.Servers()
	if err != nil {
		return nil, err
	}
	for _, server := range servers {
		if server.PID != pid {
			continue
		}
		queues := make([]string, 0, len(server.Queues))
		for queue := range server.Queues {
			queues = append(queues, queue)
		}
		slices.Sort(queues)
		return map[string]any{"found": true, "queues": queues, "status": server.Status}, nil
	}
	return map[string]any{"found": false, "queues": []string{}}, nil
}
