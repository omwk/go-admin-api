package bootstrap

import (
	"sync"
	"time"
)

const gatewayStateTTL = 5 * time.Second

// Snapshots contain encrypted material only. Primary versions are checked before
// every hit; a cache can never decide whether a revoked identity is admitted.
type gatewayStateCache struct {
	mu      sync.Mutex
	entries map[string]gatewayStateSnapshot
	clock   func() time.Time
}

type gatewayStateSnapshot struct {
	version string
	until   time.Time
	value   any
}

func (c *gatewayStateCache) now() time.Time {
	if c.clock != nil {
		return c.clock()
	}
	return time.Now()
}

func (c *gatewayStateCache) get(key, version string) (any, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	snapshot, ok := c.entries[key]
	if !ok || snapshot.version != version || !c.now().Before(snapshot.until) {
		delete(c.entries, key)
		return nil, false
	}
	return snapshot.value, true
}

func (c *gatewayStateCache) put(key, version string, value any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries == nil || len(c.entries) >= 4096 {
		c.entries = make(map[string]gatewayStateSnapshot)
	}
	c.entries[key] = gatewayStateSnapshot{version: version, until: c.now().Add(gatewayStateTTL), value: value}
}

func (c *gatewayStateCache) invalidate() {
	c.mu.Lock()
	defer c.mu.Unlock()
	clear(c.entries)
}
