//go:build gatewaybench

package bootstrap

import (
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type billingFixedArrival struct {
	TargetQPS        int              `json:"target_qps"`
	ActualQPS        float64          `json:"actual_qps"`
	Latency          gatewayBenchStat `json:"latency"`
	ScheduledLatency gatewayBenchStat `json:"scheduled_latency"`
	DispatchDelay    gatewayBenchStat `json:"dispatch_delay"`
	Errors           int              `json:"errors"`
}

func (b *gatewayBench) fixedArrival(t *testing.T, endpoint string, n int) billingFixedArrival {
	t.Helper()
	transport := &http.Transport{MaxIdleConns: 128, MaxIdleConnsPerHost: 128, MaxConnsPerHost: 128}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 10 * time.Second}
	latency, scheduled, delays := make([]time.Duration, n), make([]time.Duration, n), make([]time.Duration, n)
	bad := make([]bool, n)
	var workers sync.WaitGroup
	start := time.Now()
	for i := 0; i < n; i++ {
		due := start.Add(time.Duration(i) * time.Second / 50)
		timer := time.NewTimer(max(0, time.Until(due)))
		select {
		case <-timer.C:
		case <-b.f.ctx.Done():
			timer.Stop()
			t.Fatal("capacity context ended")
		}
		workers.Add(1)
		go func(i int, due time.Time) {
			defer workers.Done()
			delays[i] = time.Since(due)
			request, err := b.request(endpoint)
			if err != nil {
				bad[i] = true
				return
			}
			var ok bool
			latency[i], ok = gatewayBenchHTTP(client, request)
			scheduled[i] = time.Since(due)
			bad[i] = !ok
		}(i, due)
	}
	workers.Wait()
	result := billingFixedArrival{TargetQPS: 50, ActualQPS: float64(n) / time.Since(start).Seconds()}
	for _, failed := range bad {
		if failed {
			result.Errors++
		}
	}
	require.Zero(t, result.Errors, "fixed arrival HTTP failures must not be hidden")
	result.Latency, result.ScheduledLatency, result.DispatchDelay = gatewayBenchSummary(latency), gatewayBenchSummary(scheduled), gatewayBenchSummary(delays)
	return result
}
