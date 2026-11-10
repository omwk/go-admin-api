//go:build gatewaybench

package bootstrap

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/oklog/ulid/v2"
	"github.com/omwk/go-admin-api/internal/kernel/config"
	"github.com/omwk/go-admin-api/internal/modules/gateway"
	"github.com/omwk/go-admin-api/scripts/gateway/client"
	"github.com/stretchr/testify/require"
)

type gatewayBench struct {
	f       *gatewayHTTPFixture
	nonces  *gatewayNonces
	auth    *gateway.Authenticator
	limits  *gatewayLimiter
	calls   *gateway.CallService
	p       gateway.Principal
	status  gateway.OpenOrderStatus
	open    http.Handler
	billing bool
}

type gatewayBenchReport struct {
	At                 time.Time                   `json:"at"`
	Platform           string                      `json:"platform"`
	CPUs               int                         `json:"cpus"`
	Samples            int                         `json:"samples_per_level"`
	DatabasePool       int                         `json:"database_pool"`
	RedisPool          int                         `json:"redis_pool"`
	ProbeQPS           int                         `json:"probe_bucket_qps"`
	HMACBatch          int                         `json:"hmac_operations_per_sample"`
	Levels             []gatewayBenchLevel         `json:"levels"`
	Phases             map[string]gatewayBenchStat `json:"phases"`
	MaxObservedOpenQPS float64                     `json:"max_observed_open_qps"`
}

// This capacity test is absent from normal builds, with a second explicit gate.
// It does not make timing/statistical assertions in PR CI.
func TestGatewayOverhead(t *testing.T) {
	if os.Getenv("CH_GATEWAY_BENCHMARK") != "1" {
		t.Skip("explicit CH_GATEWAY_BENCHMARK=1 required")
	}
	n, err := strconv.Atoi(os.Getenv("CH_GATEWAY_BENCHMARK_SAMPLES"))
	require.NoError(t, err)
	require.True(t, n >= 100 && n <= 100000, "samples must be 100..100000")
	b := newGatewayBench(t)
	baseline := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		status, err := b.snapshot(r.Context(), b.p, b.status.ID)
		if err != nil {
			w.WriteHeader(500)
			return
		}
		if err := json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": status}); err != nil {
			return
		}
	}))
	defer baseline.Close()
	open := httptest.NewServer(b.open)
	defer open.Close()
	report := gatewayBenchReport{At: time.Now().UTC(), Platform: runtime.GOOS + "/" + runtime.GOARCH + " " + runtime.Version(),
		CPUs: runtime.NumCPU(), Samples: n, DatabasePool: 10, RedisPool: 32, ProbeQPS: 100000, HMACBatch: 1000}
	for _, raw := range strings.Split(os.Getenv("CH_GATEWAY_BENCHMARK_CONCURRENCY"), ",") {
		concurrency, err := strconv.Atoi(raw)
		require.NoError(t, err)
		require.True(t, concurrency > 0 && concurrency <= 128)
		level := b.measureHTTP(t, baseline.URL, open.URL+b.f.publicPath(), n, concurrency)
		report.Levels = append(report.Levels, level)
		if level.Errors == 0 {
			report.MaxObservedOpenQPS = max(report.MaxObservedOpenQPS, level.OpenQPS)
		}
		t.Logf("concurrency=%d open_p99_ms=%.3f overhead_p99_ms=%.3f open_qps=%.2f errors=%d", concurrency, level.Open.P99, level.Overhead.P99, level.OpenQPS, level.Errors)
	}
	report.Phases = b.measurePhases(t, min(n, 500))
	raw, err := json.MarshalIndent(report, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(os.Getenv("CH_GATEWAY_BENCHMARK_OUTPUT"), raw, 0o600))
	for _, level := range report.Levels {
		require.Zero(t, level.Errors, "HTTP failures must not be hidden by percentiles")
	}
}

func newGatewayBench(t *testing.T) *gatewayBench {
	t.Helper()
	f := newGatewayHTTPFixture(t)
	db, err := strconv.Atoi(os.Getenv("CH_REDIS_DB"))
	require.NoError(t, err)
	require.True(t, db > 0, "benchmark requires a reserved non-development Redis DB")
	nonces, err := newGatewayNonces(f.ctx, config.Redis{Addr: os.Getenv("CH_REDIS_ADDR"), Password: os.Getenv("CH_REDIS_PASSWORD"), DB: db, PoolSize: 32, MinIdleConns: 4})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, nonces.Close()) })
	auth, err := gateway.NewAuthenticator(f.gate, nonces, f.sealer, func(*http.Request) string { return "192.0.2.1" }, time.Now)
	require.NoError(t, err)
	policy := config.Gateway{GlobalQPS: 100000, GlobalBurst: 100000, ClientQPS: 100000, ClientBurst: 100000, RouteQPS: 100000, RouteBurst: 100000}
	limits, err := newGatewayLimiter(nonces.client, policy)
	require.NoError(t, err)
	calls, err := gateway.NewCallService(f.gate, time.Now, gateway.CallPolicy{Timeout: 5 * time.Second, MaxAttempts: 2, HashKey: shopRandomBytes(t, 32)})
	require.NoError(t, err)
	p := gateway.Principal{TenantID: f.client.TenantID, ClientID: f.client.ID, OwnerUserID: f.client.OwnerUserID, SecretID: f.key.ID, AppID: f.client.AppID}
	reader, err := NewGatewayOrderReader(f.database)
	require.NoError(t, err)
	status, err := reader.OrderStatus(f.ctx, p, f.grant.PublicID)
	require.NoError(t, err)
	b := &gatewayBench{f: f, nonces: nonces, auth: auth, limits: limits, calls: calls, p: p, status: status}
	b.open, err = gateway.NewOpenHandler(auth, f.gate, gatewayStatusFunc(b.snapshot), calls, limits, time.Now)
	require.NoError(t, err)
	return b
}

// snapshot removes domain lookup cost from both sides using a real, pre-read result.
// This port exists only in the opt-in harness; all gateway state gates remain real.
func (b *gatewayBench) snapshot(ctx context.Context, _ gateway.Principal, _ string) (gateway.OpenOrderStatus, error) {
	return b.status, ctx.Err()
}

func (b *gatewayBench) request(endpoint string) (*http.Request, error) {
	r, err := client.NewRequest(b.f.ctx, "GET", endpoint, b.f.client.AppID, b.f.key.Secret, nil)
	if err != nil || !b.billing {
		return r, err
	}
	r.Header.Set("X-Signature-Version", "2")
	r.Header.Set("Idempotency-Key", ulid.Make().String())
	canonical, err := gateway.SigningRequest(r, r.Header.Get("X-Timestamp"), r.Header.Get("X-Nonce"), nil)
	if err != nil {
		return nil, err
	}
	mac := hmac.New(sha256.New, []byte(b.f.key.Secret))
	mac.Write([]byte(canonical))
	r.Header.Set("X-Signature", base64.StdEncoding.EncodeToString(mac.Sum(nil)))
	return r, nil
}

func (b *gatewayBench) measurePhases(t *testing.T, n int) map[string]gatewayBenchStat {
	t.Helper()
	request, err := b.request("http://127.0.0.1" + b.f.publicPath())
	require.NoError(t, err)
	canonical, err := gateway.SigningRequest(request, request.Header.Get("X-Timestamp"), request.Header.Get("X-Nonce"), nil)
	require.NoError(t, err)
	signature, err := base64.StdEncoding.DecodeString(request.Header.Get("X-Signature"))
	require.NoError(t, err)
	result := map[string]gatewayBenchStat{}
	ops := map[string]func() error{
		"hmac_sha256_batch_mean": func() error {
			mac := hmac.New(sha256.New, []byte(b.f.key.Secret))
			mac.Write([]byte(canonical))
			if !hmac.Equal(mac.Sum(nil), signature) {
				return gateway.Invalid()
			}
			return nil
		},
		"nonce_redis": func() error {
			nonce := ulid.Make().String() + "ABCDEF"
			fresh, err := b.nonces.Claim(b.f.ctx, b.p.AppID, nonce, gateway.NonceTTL)
			if err == nil && !fresh {
				return gateway.Invalid()
			}
			return err
		},
		"rate_limit_redis": func() error { _, err := b.limits.Take(b.f.ctx, b.p, b.f.route.ID); return err },
		"authentication_db_and_nonce": func() error {
			r, err := b.request("http://127.0.0.1" + b.f.publicPath())
			if err != nil {
				return err
			}
			_, err = b.auth.Authenticate(b.f.ctx, r)
			return err
		},
		"metering_db_admission_and_terminal": func() error {
			call := b.calls.NewCall(b.p, b.f.route, ulid.Make().String(), "", "192.0.2.1")
			_, err := b.calls.Run(b.f.ctx, call, func(ctx context.Context) (gateway.OpenOrderStatus, error) { return b.snapshot(ctx, b.p, b.status.ID) })
			return err
		},
	}
	for _, name := range []string{"hmac_sha256_batch_mean", "nonce_redis", "rate_limit_redis", "authentication_db_and_nonce", "metering_db_admission_and_terminal"} {
		samples := make([]time.Duration, n)
		repeat := 1
		if name == "hmac_sha256_batch_mean" {
			repeat = 1000
		}
		for i := range samples {
			start := time.Now()
			for j := 0; j < repeat; j++ {
				if err := ops[name](); err != nil {
					t.Fatalf("phase %s failed", name)
				}
			}
			samples[i] = time.Since(start) / time.Duration(repeat)
		}
		result[name] = gatewayBenchSummary(samples)
	}
	return result
}
