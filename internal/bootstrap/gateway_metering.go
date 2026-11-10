package bootstrap

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/omwk/go-admin-api/internal/kernel/config"
	"github.com/omwk/go-admin-api/internal/kernel/db"
	"github.com/omwk/go-admin-api/internal/modules/gateway"
)

func newGatewayCalls(database *db.DB, cfg *config.Config) (*gateway.CallService, error) {
	key, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(cfg.Crypto.MasterKey, "base64:"))
	if err != nil || len(key) != 32 {
		return nil, errors.New("gateway observation key configuration required")
	}
	defer clear(key)
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte("gateway:private-observation:v1"))
	charges, err := newGatewayBilling(database, time.Now)
	if err != nil {
		return nil, err
	}
	return gateway.NewCallService(&gatewayStore{database: database}, time.Now,
		gateway.CallPolicy{Timeout: 5 * time.Second, MaxAttempts: 2, HashKey: mac.Sum(nil)}, charges)
}

func pollGatewayCalls(ctx context.Context, calls *gateway.CallReconciler, log *slog.Logger) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		if _, err := calls.Reconcile(ctx); err != nil && ctx.Err() == nil {
			log.ErrorContext(ctx, "gateway call reconciliation failed", "module", "gateway", "action", "reconcile")
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
