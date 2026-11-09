package bootstrap

import (
	"context"
	"errors"
	"time"

	"github.com/omwk/go-admin-api/internal/kernel/config"
	"github.com/omwk/go-admin-api/internal/kernel/errs"
	"github.com/omwk/go-admin-api/internal/modules/gateway"
	"github.com/redis/go-redis/v9"
)

type gatewayNonces struct{ client *redis.Client }

func newGatewayNonces(ctx context.Context, cfg config.Redis) (*gatewayNonces, error) {
	if cfg.Addr == "" || cfg.PoolSize <= 0 {
		return nil, errors.New("gateway Redis configuration is required")
	}
	client := redis.NewClient(&redis.Options{Addr: cfg.Addr, Password: cfg.Password, DB: cfg.DB,
		PoolSize: cfg.PoolSize, MinIdleConns: cfg.MinIdleConns, MaxRetries: -1,
		DialTimeout: time.Second, ReadTimeout: time.Second, WriteTimeout: time.Second,
		PoolTimeout: time.Second, ContextTimeoutEnabled: true})
	bounded, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	if err := client.Ping(bounded).Err(); err != nil {
		return nil, errors.Join(gateway.Unavailable(), client.Close())
	}
	return &gatewayNonces{client: client}, nil
}

func (n *gatewayNonces) Claim(ctx context.Context, appID, nonce string, ttl time.Duration) (bool, error) {
	if ttl != gateway.NonceTTL || len(appID) != 26 || len(nonce) != 32 {
		return false, gateway.Invalid()
	}
	bounded, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	fresh, err := n.client.SetNX(bounded, "gateway:app:"+appID+":nonce:"+nonce, "1", ttl).Result()
	if err != nil {
		return false, gateway.Unavailable()
	}
	return fresh, nil
}

func (n *gatewayNonces) Close() error { return n.client.Close() }

func gatewayAuthenticationError() error { return errs.New(80102, 401, "应用凭证不可用") }

var _ gateway.Nonces = (*gatewayNonces)(nil)
