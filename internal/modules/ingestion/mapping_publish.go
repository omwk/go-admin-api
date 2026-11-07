package ingestion

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
)

// PublishBackend supplies the immutable inputs and atomic active-version swap.
// Implementations must keep DryRun read-only and perform CASActive in one DB
// transaction after rechecking the draft row. Persist final spec and checksum
// before CAS; a failed CAS must roll back the version write.
type PublishBackend interface {
	StatusSnapshot(context.Context) (map[string]map[string]string, error)
	DryRunSamples(context.Context, int) ([]json.RawMessage, error)
	CASActive(context.Context, int64, []byte, string) error
}

// PublishRequest identifies a draft mapping version.
type PublishRequest struct {
	VersionID int64
	Spec      []byte
}

// DryRunResult contains the result of the release preflight.
type DryRunResult struct {
	Checked int
	Errors  []FieldError
}

// PublishGate enforces the release ordering before activating a mapping.
type PublishGate struct{ Backend PublishBackend }

// Publish executes schema/target/path/expr validation, a 20-record dry-run,
// then the backend's compare-and-swap active-version update.
func (g PublishGate) Publish(ctx context.Context, request PublishRequest) (*Compiled, DryRunResult, error) {
	if g.Backend == nil || request.VersionID <= 0 || len(request.Spec) == 0 {
		return nil, DryRunResult{}, errors.New("publish backend, version and spec are required")
	}
	tables, err := g.Backend.StatusSnapshot(ctx)
	if err != nil {
		return nil, DryRunResult{}, fmt.Errorf("snapshot status tables: %w", err)
	}
	final, err := snapshotSpec(request.Spec, tables)
	if err != nil {
		return nil, DryRunResult{}, err
	}
	compiled, err := CompileFromVersion(final)
	if err != nil {
		return nil, DryRunResult{}, err
	}
	samples, err := g.Backend.DryRunSamples(ctx, 20)
	if err != nil {
		return nil, DryRunResult{}, fmt.Errorf("load dry-run samples: %w", err)
	}
	if len(samples) > 20 {
		samples = samples[:20]
	}
	defer func() {
		for _, sample := range samples {
			clear(sample)
		}
	}()
	result := DryRunResult{Checked: len(samples)}
	for _, sample := range samples {
		_, fieldErrors, applyErr := compiled.Apply(sample)
		if compiled.spec.Resource == string(ResourceOrder) {
			if applyErr != nil {
				applyErr = errors.New("order mapping validation failed")
			}
			for i := range fieldErrors {
				fieldErrors[i].Message = "order mapping validation failed"
			}
		}
		if applyErr != nil {
			return nil, result, applyErr
		}
		result.Errors = append(result.Errors, fieldErrors...)
	}
	for _, fieldErr := range result.Errors {
		if fieldErr.Fatal {
			return nil, result, fmt.Errorf("dry-run failed for %s: %s", fieldErr.Target, fieldErr.Message)
		}
	}
	if err := g.Backend.CASActive(ctx, request.VersionID, final, compiled.Checksum); err != nil {
		return nil, result, fmt.Errorf("activate mapping version: %w", err)
	}
	return compiled, result, nil
}

// MemoryPublishBackend is a deterministic backend for unit tests and local
// tools. It deliberately exposes no write operation for dry-run samples.
type MemoryPublishBackend struct {
	mu       sync.Mutex
	Tables   map[string]map[string]string
	Samples  []json.RawMessage
	ActiveID int64
	Checksum string
	Spec     json.RawMessage
}

func (m *MemoryPublishBackend) StatusSnapshot(context.Context) (map[string]map[string]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return cloneStatusTables(m.Tables), nil
}
func (m *MemoryPublishBackend) DryRunSamples(context.Context, int) ([]json.RawMessage, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	samples := make([]json.RawMessage, len(m.Samples))
	for i := range m.Samples {
		samples[i] = append(json.RawMessage(nil), m.Samples[i]...)
	}
	return samples, nil
}
func (m *MemoryPublishBackend) CASActive(_ context.Context, id int64, spec []byte, checksum string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if id <= 0 || checksum == "" {
		return errors.New("invalid active mapping")
	}
	m.Spec = append(json.RawMessage(nil), spec...)
	m.ActiveID, m.Checksum = id, checksum
	return nil
}
