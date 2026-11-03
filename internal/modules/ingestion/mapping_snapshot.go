package ingestion

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// ErrStatusSnapshotMissing requires republishing legacy versions. Workers must
// never substitute mutable sys_setting values for a missing release snapshot.
var ErrStatusSnapshotMissing = errors.New("published mapping spec is missing status_tables snapshot; republish the mapping")

// CompileFromVersion rebuilds a published mapping using only persisted spec.
// Even a release without status transforms must contain an explicit {} snapshot.
func CompileFromVersion(specJSON []byte) (*Compiled, error) {
	spec, err := decodeSpec(specJSON)
	if err != nil {
		return nil, err
	}
	if spec.StatusTables == nil {
		return nil, ErrStatusSnapshotMissing
	}
	return compileSpec(specJSON, spec)
}

func decodeSpec(raw []byte) (Spec, error) {
	var spec Spec
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&spec); err != nil {
		return Spec{}, fmt.Errorf("decode mapping spec: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return Spec{}, errors.New("mapping spec contains multiple JSON values")
		}
		return Spec{}, fmt.Errorf("decode mapping spec: %w", err)
	}
	return spec, nil
}

func snapshotSpec(raw []byte, tables map[string]map[string]string) ([]byte, error) {
	if _, err := decodeSpec(raw); err != nil {
		return nil, err
	}
	// Preserve numeric defaults exactly while replacing any draft-supplied
	// snapshot with the trusted publication input.
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil {
		return nil, err
	}
	if object == nil {
		return nil, errors.New("mapping spec must be an object")
	}
	snapshot, err := json.Marshal(cloneStatusTables(tables))
	if err != nil {
		return nil, err
	}
	object["status_tables"] = snapshot
	return json.Marshal(object)
}
