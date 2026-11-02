package connector

import (
	"errors"
	"sort"
	"sync"
)

// Registry stores immutable platform adapters.
type Registry struct {
	mu    sync.RWMutex
	items map[string]Platform
}

// NewRegistry creates an empty adapter registry.
func NewRegistry() *Registry { return &Registry{items: make(map[string]Platform)} }

// Register rejects empty or duplicate platform codes.
func (r *Registry) Register(platform Platform) error {
	if r == nil || platform == nil || platform.Code() == "" {
		return errors.New("platform and code are required")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.items[platform.Code()]; ok {
		return errors.New("platform already registered")
	}
	r.items[platform.Code()] = platform
	return nil
}

// Get returns a registered adapter.
func (r *Registry) Get(code string) (Platform, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	platform, ok := r.items[code]
	return platform, ok
}

// List returns platform codes in deterministic order.
func (r *Registry) List() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	codes := make([]string, 0, len(r.items))
	for code := range r.items {
		codes = append(codes, code)
	}
	sort.Strings(codes)
	return codes
}
