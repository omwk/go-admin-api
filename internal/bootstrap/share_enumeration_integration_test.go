package bootstrap

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"math"
	"net/http/httptest"
	"os"
	"sort"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/omwk/go-admin-api/internal/modules/share"
	"github.com/stretchr/testify/require"
)

func TestShareIntegrationEnumerationResistance(t *testing.T) {
	// REQ-SHR-003: capacity scale is explicit; PR CI only runs the small sample.
	f := newShareAccessFixture(t)
	link := f.issue(t, f.publication())
	f.router = gin.New()
	share.RegisterPublicRoutes(f.router, f.links, 1)
	count := 1000
	if value := os.Getenv("CH_SHARE_ENUMERATION_COUNT"); value != "" {
		var err error
		count, err = strconv.Atoi(value)
		require.NoError(t, err)
		require.GreaterOrEqual(t, count, 1)
		require.LessOrEqual(t, count, 1000000)
	}
	tokens := make([]string, count)
	for i := range tokens {
		tokens[i] = randomShareProbe(t)
	}
	started := time.Now()
	counts := make(chan map[int]int, 128)
	var wg sync.WaitGroup
	for worker := range 128 {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			statuses := map[int]int{}
			for i := index; i < count; i += 128 {
				req := httptest.NewRequest("GET", "https://quote.test/api/v1/share/"+tokens[i], nil)
				req.RemoteAddr = fmt.Sprintf("192.0.2.%d:32000", index+1)
				w := httptest.NewRecorder()
				f.router.ServeHTTP(w, req)
				statuses[w.Code]++
			}
			counts <- statuses
		}(worker)
	}
	wg.Wait()
	close(counts)
	statuses := map[int]int{}
	for group := range counts {
		for code, n := range group {
			statuses[code] += n
		}
	}
	require.Zero(t, statuses[200])
	require.Equal(t, count, statuses[404]+statuses[429])
	t.Logf("enumeration requests=%d success=%d invalid=%d throttled=%d elapsed=%s", count, statuses[200], statuses[404], statuses[429], time.Since(started))
	for _, key := range f.server.Keys() {
		require.NotContains(t, key, "share:miss:")
		require.NotContains(t, key, "share:content:")
	}
	// A real issued token still works after adversarial traffic once the entry window refills.
	f.server.FastForward(time.Minute)
	f.server.SetTime(f.now.Add(time.Minute))
	require.Equal(t, 200, f.anonymous(t, "GET", link.URL, "198.51.100.1", nil, nil).Code)
}

func randomShareProbe(t *testing.T) string {
	t.Helper()
	var bytes [16]byte
	_, err := rand.Read(bytes[:])
	require.NoError(t, err)
	return "v1" + base64.RawURLEncoding.EncodeToString(bytes[:])
}

func TestShareIntegrationInvalidAndValidTokenTiming(t *testing.T) {
	// REQ-SHR-003: statistical acceptance is opt-in; shared PR runners are noisy.
	// Use 160 interleaved samples/group, two-sided Welch-normal p > 0.05, and
	// retain the 99% difference bound within 10% of the smaller mean.
	// Local strict run 2026-11-08: valid mean=25.310ms, invalid mean=25.352ms,
	// z=0.115, p=0.9088, 99% difference bound=0.985ms (passed).
	if os.Getenv("SHARE_TIMING_STRICT") != "1" {
		t.Skip("set SHARE_TIMING_STRICT=1 for the strict 160-sample timing validation")
	}
	f := newShareAccessFixture(t)
	link := f.issue(t, f.publication())
	f.router = gin.New()
	share.RegisterPublicRoutes(f.router, f.links, 1)
	valid, invalid := []float64{}, []float64{}
	// Interleave and alternate order so drift cannot systematically favor either group.
	for i := range 160 {
		paths := []string{link.URL, "/api/v1/share/" + randomShareProbe(t)}
		if i%2 == 1 {
			paths[0], paths[1] = paths[1], paths[0]
		}
		for _, path := range paths {
			start := time.Now()
			w := f.anonymous(t, "GET", path, fmt.Sprintf("198.51.100.%d", i%16+1), nil, nil)
			elapsed := float64(time.Since(start)) / float64(time.Millisecond)
			if path == link.URL {
				require.Equal(t, 200, w.Code)
				valid = append(valid, elapsed)
			} else {
				require.Equal(t, 404, w.Code)
				invalid = append(invalid, elapsed)
			}
		}
	}
	vm, vv := shareSample(valid)
	im, iv := shareSample(invalid)
	se := math.Sqrt(vv/float64(len(valid)) + iv/float64(len(invalid)))
	z := math.Abs(vm-im) / se
	p := math.Erfc(z / math.Sqrt2)
	margin := math.Abs(vm-im) + 2.576*se
	t.Logf("timing n=%d/group valid mean=%.3fms p50=%.3fms p95=%.3fms invalid mean=%.3fms p50=%.3fms p95=%.3fms delta=%.3fms Welch-normal z=%.3f p=%.4f 99%% difference bound=%.3fms", len(valid), vm, sharePercentile(valid, .5), sharePercentile(valid, .95), im, sharePercentile(invalid, .5), sharePercentile(invalid, .95), vm-im, z, p, margin)
	require.Greater(t, p, 0.05, "token timing must not differ at 5%% significance")
	// A 99% confidence interval must fit within a predeclared 10% practical margin.
	require.Less(t, margin, 0.1*math.Min(vm, im))
}

func shareSample(values []float64) (float64, float64) {
	mean := 0.0
	for _, v := range values {
		mean += v
	}
	mean /= float64(len(values))
	variance := 0.0
	for _, v := range values {
		variance += (v - mean) * (v - mean)
	}
	return mean, variance / float64(len(values)-1)
}

func sharePercentile(values []float64, q float64) float64 {
	copyValues := append([]float64(nil), values...)
	sort.Float64s(copyValues)
	return copyValues[int(q*float64(len(copyValues)-1))]
}
