package scope

import (
	"encoding/json"
	"errors"
	"sort"
	"strings"
)

// BootstrapIssuer is the only issuer token accepted by IssueWorkAccess.
// It is intentionally a marker value; callers should obtain WorkAccess only
// after bootstrap has re-read the durable job, run and shop relationships.
type BootstrapIssuer struct{}

// WorkAccess is the immutable, worker-only authorization capability.
// Fields remain private so request and queue JSON cannot manufacture one.
type WorkAccess struct {
	tenantID       int64
	jobID          int64
	runID          int64
	shopID         int64
	resource       string
	mappingVersion int64
	methods        map[string]struct{}
	generation     int64
}

// IssueWorkAccess constructs a capability from bootstrap-verified facts.
func IssueWorkAccess(_ BootstrapIssuer, tenantID, jobID, runID, shopID int64, resource string, mappingVersion, generation int64, methods []string) (WorkAccess, error) {
	if tenantID <= 0 || jobID <= 0 || runID <= 0 || shopID <= 0 || mappingVersion <= 0 || generation <= 0 {
		return WorkAccess{}, errors.New("work access identifiers are required")
	}
	resource = strings.TrimSpace(resource)
	if !SupportedResource(resource) {
		return WorkAccess{}, errors.New("work access resource is unsupported")
	}
	if len(methods) == 0 {
		return WorkAccess{}, errors.New("work access methods are required")
	}
	allowed := make(map[string]struct{}, len(methods))
	for _, method := range methods {
		method = strings.TrimSpace(method)
		if method == "" {
			return WorkAccess{}, errors.New("work access method is required")
		}
		allowed[method] = struct{}{}
	}
	return WorkAccess{tenantID: tenantID, jobID: jobID, runID: runID, shopID: shopID,
		resource: resource, mappingVersion: mappingVersion, methods: allowed, generation: generation}, nil
}

// SupportedResource defines the platform-neutral resources accepted by workers.
func SupportedResource(resource string) bool {
	switch strings.TrimSpace(resource) {
	case "product", "order", "inventory", "category":
		return true
	default:
		return false
	}
}

func (a WorkAccess) TenantID() int64       { return a.tenantID }
func (a WorkAccess) JobID() int64          { return a.jobID }
func (a WorkAccess) RunID() int64          { return a.runID }
func (a WorkAccess) ShopID() int64         { return a.shopID }
func (a WorkAccess) Resource() string      { return a.resource }
func (a WorkAccess) MappingVersion() int64 { return a.mappingVersion }
func (a WorkAccess) Generation() int64     { return a.generation }

// Allows returns whether a verified operation is present in this capability.
func (a WorkAccess) Allows(method string) bool {
	_, ok := a.methods[strings.TrimSpace(method)]
	return ok
}

// Methods returns a copy so callers cannot mutate the capability.
func (a WorkAccess) Methods() []string {
	result := make([]string, 0, len(a.methods))
	for method := range a.methods {
		result = append(result, method)
	}
	sort.Strings(result)
	return result
}

// MarshalJSON prevents capabilities from being put into request or queue payloads.
func (WorkAccess) MarshalJSON() ([]byte, error) {
	return nil, errors.New("work access cannot be serialized")
}

// UnmarshalJSON prevents capabilities from being accepted from untrusted payloads.
func (*WorkAccess) UnmarshalJSON([]byte) error {
	return errors.New("work access cannot be deserialized")
}

var _ json.Marshaler = WorkAccess{}
var _ json.Unmarshaler = (*WorkAccess)(nil)
