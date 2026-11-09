//go:build gatewaybench

package bootstrap

import (
	"context"
	"encoding/json"
	"io"
	"math"
	"net/http"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type gatewayBenchStat struct {
	Samples int     `json:"samples"`
	P50     float64 `json:"p50_ms"`
	P95     float64 `json:"p95_ms"`
	P99     float64 `json:"p99_ms"`
}

type gatewayBenchLevel struct {
	Concurrency int              `json:"concurrency"`
	Direct      gatewayBenchStat `json:"direct"`
	Open        gatewayBenchStat `json:"open"`
	Overhead    gatewayBenchStat `json:"paired_overhead"`
	DirectQPS   float64          `json:"direct_qps"`
	OpenQPS     float64          `json:"open_qps"`
	Errors      int              `json:"errors"`
}

func gatewayBenchSummary(values []time.Duration) gatewayBenchStat {
	if len(values) == 0 {
		return gatewayBenchStat{}
	}
	samples := append([]time.Duration(nil), values...)
	sort.Slice(samples, func(i, j int) bool { return samples[i] < samples[j] })
	percentile := func(p float64) float64 {
		return float64(samples[int(math.Ceil(p*float64(len(samples))))-1]) / float64(time.Millisecond)
	}
	return gatewayBenchStat{Samples: len(samples), P50: percentile(.5), P95: percentile(.95), P99: percentile(.99)}
}

func (b *gatewayBench) measureHTTP(t *testing.T, directURL, openURL string, n, concurrency int) gatewayBenchLevel {
	t.Helper()
	transport := &http.Transport{MaxIdleConns: 2 * concurrency, MaxIdleConnsPerHost: concurrency}
	defer transport.CloseIdleConnections()
	httpClient := &http.Client{Transport: transport, Timeout: 10 * time.Second}
	// Warm both paths and pools; warmup is excluded from samples and throughput.
	for i := 0; i < 20; i++ {
		direct, err := http.NewRequestWithContext(b.f.ctx, "GET", directURL, nil)
		require.NoError(t, err)
		_, ok := gatewayBenchHTTP(httpClient, direct)
		require.True(t, ok, "direct warmup failed")
		r, err := b.request(openURL)
		require.NoError(t, err)
		_, ok = gatewayBenchHTTP(httpClient, r)
		require.True(t, ok, "open warmup failed")
	}
	direct, directElapsed, directErrors := b.sweep(httpClient, directURL, n, concurrency, false)
	open, openElapsed, openErrors := b.sweep(httpClient, openURL, n, concurrency, true)
	// Separate paired sampling avoids presenting a difference of unrelated percentiles.
	overhead := b.paired(t, httpClient, directURL, openURL, min(n, 500), concurrency)
	return gatewayBenchLevel{Concurrency: concurrency, Direct: gatewayBenchSummary(direct), Open: gatewayBenchSummary(open), Overhead: gatewayBenchSummary(overhead),
		DirectQPS: float64(n-directErrors) / directElapsed.Seconds(), OpenQPS: float64(n-openErrors) / openElapsed.Seconds(), Errors: directErrors + openErrors}
}

func (b *gatewayBench) sweep(httpClient *http.Client, endpoint string, n, concurrency int, signed bool) ([]time.Duration, time.Duration, int) {
	values := make([]time.Duration, n)
	failed := make([]bool, n)
	var workers sync.WaitGroup
	start := time.Now()
	for worker := 0; worker < concurrency; worker++ {
		workers.Add(1)
		go func(worker int) {
			defer workers.Done()
			for i := worker; i < n; i += concurrency {
				var req *http.Request
				var err error
				if signed {
					req, err = b.request(endpoint)
				} else {
					req, err = http.NewRequestWithContext(b.f.ctx, "GET", endpoint, nil)
				}
				if err != nil {
					failed[i] = true
					continue
				}
				var ok bool
				values[i], ok = gatewayBenchHTTP(httpClient, req)
				failed[i] = !ok
			}
		}(worker)
	}
	workers.Wait()
	elapsed := time.Since(start)
	valid, errors := make([]time.Duration, 0, n), 0
	for i, bad := range failed {
		if bad {
			errors++
		} else {
			valid = append(valid, values[i])
		}
	}
	return valid, elapsed, errors
}

func (b *gatewayBench) paired(t *testing.T, httpClient *http.Client, directURL, openURL string, n, concurrency int) []time.Duration {
	t.Helper()
	values := make([]time.Duration, n)
	failed := make([]bool, n)
	var workers sync.WaitGroup
	for worker := 0; worker < concurrency; worker++ {
		workers.Add(1)
		go func(worker int) {
			defer workers.Done()
			for i := worker; i < n; i += concurrency {
				direct, err := http.NewRequestWithContext(b.f.ctx, "GET", directURL, nil)
				if err != nil {
					failed[i] = true
					continue
				}
				open, err := b.request(openURL)
				if err != nil {
					failed[i] = true
					continue
				}
				requests := []*http.Request{direct, open}
				if i%2 == 1 {
					requests[0], requests[1] = requests[1], requests[0]
				}
				first, ok := gatewayBenchHTTP(httpClient, requests[0])
				if !ok {
					failed[i] = true
					continue
				}
				second, ok := gatewayBenchHTTP(httpClient, requests[1])
				if !ok {
					failed[i] = true
					continue
				}
				if i%2 == 0 {
					values[i] = second - first
				} else {
					values[i] = first - second
				}
			}
		}(worker)
	}
	workers.Wait()
	for _, bad := range failed {
		require.False(t, bad, "paired HTTP failure")
	}
	return values
}

func gatewayBenchHTTP(httpClient *http.Client, request *http.Request) (time.Duration, bool) {
	ctx, cancel := context.WithTimeout(request.Context(), 10*time.Second)
	defer cancel()
	start := time.Now()
	resp, err := httpClient.Do(request.WithContext(ctx))
	if err != nil {
		return time.Since(start), false
	}
	body, readErr := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	closeErr := resp.Body.Close()
	elapsed := time.Since(start)
	var envelope struct{ Code int }
	return elapsed, readErr == nil && closeErr == nil && resp.StatusCode == 200 && json.Unmarshal(body, &envelope) == nil && envelope.Code == 0
}
