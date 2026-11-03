package ingestion

import (
	"container/list"
	"errors"
	"sync"
)

type cacheEntry struct {
	id       int64
	checksum string
	compiled *Compiled
}

// CompiledCache is a bounded process-local LRU for published mappings.
type CompiledCache struct {
	mu       sync.Mutex
	capacity int
	items    map[int64]*list.Element
	order    *list.List
}

// NewCompiledCache creates a cache with the requested capacity. Capacity is
// capped at 100 to prevent an unbounded mapping program footprint.
func NewCompiledCache(capacity int) *CompiledCache {
	if capacity <= 0 || capacity > 100 {
		capacity = 100
	}
	return &CompiledCache{capacity: capacity, items: make(map[int64]*list.Element), order: list.New()}
}

// Get returns a checksum-matching immutable program.
func (c *CompiledCache) Get(versionID int64, checksum string) (*Compiled, bool) {
	if c == nil || versionID <= 0 || checksum == "" {
		return nil, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	element, ok := c.items[versionID]
	if !ok {
		return nil, false
	}
	entry := element.Value.(cacheEntry)
	if entry.checksum != checksum || entry.compiled == nil || entry.compiled.FunctionVersion != FunctionSetVersion {
		delete(c.items, versionID)
		c.order.Remove(element)
		return nil, false
	}
	c.order.MoveToFront(element)
	return entry.compiled, true
}

// Put stores a program only when its checksum is non-empty.
func (c *CompiledCache) Put(versionID int64, checksum string, compiled *Compiled) {
	if c == nil || versionID <= 0 || checksum == "" || compiled == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if element, ok := c.items[versionID]; ok {
		element.Value = cacheEntry{id: versionID, checksum: checksum, compiled: compiled}
		c.order.MoveToFront(element)
		return
	}
	element := c.order.PushFront(cacheEntry{id: versionID, checksum: checksum, compiled: compiled})
	c.items[versionID] = element
	for len(c.items) > c.capacity {
		oldest := c.order.Back()
		entry := oldest.Value.(cacheEntry)
		delete(c.items, entry.id)
		c.order.Remove(oldest)
	}
}

// GetOrCompile returns the cached version or compiles and caches the spec.
func (c *CompiledCache) GetOrCompile(versionID int64, checksum string, spec []byte) (*Compiled, error) {
	if compiled, ok := c.Get(versionID, checksum); ok {
		return compiled, nil
	}
	compiled, err := CompileFromVersion(spec)
	if err != nil {
		return nil, err
	}
	if checksum != "" && compiled.Checksum != checksum {
		return nil, errors.New("mapping checksum does not match compiled spec")
	}
	c.Put(versionID, compiled.Checksum, compiled)
	return compiled, nil
}

// Checksum computes the same release checksum without compiling a program.
func Checksum(spec []byte) (string, error) {
	if _, err := decodeSpec(spec); err != nil {
		return "", err
	}
	return mappingChecksum(spec)
}
