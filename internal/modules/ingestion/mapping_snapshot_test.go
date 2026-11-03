package ingestion

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

const statusMappingSpec = `{"schema_version":1,"resource":"product","fields":[{"target":"status","source":"$.state","transform":"map_status(value, 'product_status')"}],"status_tables":{"product_status":{"ACTIVE":"offline"}}}`

func TestPublishedStatusSnapshotSurvivesColdCompile(t *testing.T) {
	backend := &MemoryPublishBackend{Tables: map[string]map[string]string{"product_status": {"ACTIVE": "online"}}}
	published, _, err := (PublishGate{Backend: backend}).Publish(context.Background(), PublishRequest{VersionID: 3, Spec: []byte(statusMappingSpec)})
	require.NoError(t, err)
	var stored Spec
	require.NoError(t, json.Unmarshal(backend.Spec, &stored))
	require.Equal(t, "online", stored.StatusTables["product_status"]["ACTIVE"])
	backend.Tables["product_status"]["ACTIVE"] = "offline"
	// A new cache receives only persisted bytes, as in a separate worker.
	program, err := NewCompiledCache(1).GetOrCompile(3, backend.Checksum, backend.Spec)
	require.NoError(t, err)
	require.Equal(t, published.Checksum, program.Checksum)
	values, problems, err := program.Apply(json.RawMessage(`{"state":"ACTIVE"}`))
	require.NoError(t, err)
	require.Empty(t, problems)
	require.Equal(t, "online", values["status"])
	// jsonb rewrites whitespace and key order; the release digest must survive it.
	var object map[string]any
	require.NoError(t, json.Unmarshal(backend.Spec, &object))
	reformatted, err := json.MarshalIndent(object, "", "  ")
	require.NoError(t, err)
	restarted, err := CompileFromVersion(reformatted)
	require.NoError(t, err)
	require.Equal(t, published.Checksum, restarted.Checksum)
	digest, err := Checksum(backend.Spec)
	require.NoError(t, err)
	require.Equal(t, published.Checksum, digest)
}

func TestCompileFromVersionRequiresSnapshot(t *testing.T) {
	for _, snapshot := range []string{"", `,"status_tables":null`} {
		spec := []byte(`{"schema_version":1,"resource":"product","fields":[{"target":"status","source":"$.state","transform":"map_status(value, 'product_status')"}]` + snapshot + `}`)
		_, err := CompileFromVersion(spec)
		require.ErrorIs(t, err, ErrStatusSnapshotMissing)
		_, err = NewCompiledCache(1).GetOrCompile(1, "old-checksum", spec)
		require.ErrorIs(t, err, ErrStatusSnapshotMissing)
	}
	_, err := CompileFromVersion([]byte(`{"schema_version":1,"resource":"product","fields":[{"target":"title","source":"$.title"}],"status_tables":{}}`))
	require.NoError(t, err)
}

func TestSnapshotDryRunFailureDoesNotPublish(t *testing.T) {
	backend := &MemoryPublishBackend{
		Tables:  map[string]map[string]string{"product_status": {"ACTIVE": "online"}},
		Samples: []json.RawMessage{json.RawMessage(`{"state":"UNRECOGNIZED"}`)},
	}
	_, result, err := (PublishGate{Backend: backend}).Publish(context.Background(), PublishRequest{VersionID: 3, Spec: []byte(statusMappingSpec)})
	require.Error(t, err)
	require.Equal(t, 1, result.Checked)
	require.Zero(t, backend.ActiveID)
	require.Empty(t, backend.Spec)
	require.Empty(t, backend.Checksum)
}

func TestSnapshotRejectsNullSpec(t *testing.T) {
	_, err := CompileWithStatusTables([]byte(`null`), nil)
	require.Error(t, err)
	backend := &MemoryPublishBackend{}
	_, _, err = (PublishGate{Backend: backend}).Publish(context.Background(), PublishRequest{VersionID: 3, Spec: []byte(`null`)})
	require.Error(t, err)
	require.Zero(t, backend.ActiveID)
}

func TestVersionSnapshotUsedInCollectionsAndChecksumDetectsChanges(t *testing.T) {
	spec := []byte(`{"schema_version":1,"resource":"product","fields":[{"target":"title","source":"$.title"}],"collections":[{"target":"items","source":"$.items","key_field":"code","fields":[{"target":"code","source":"$.state","transform":"map_status(value,'states')"}]}],"status_tables":{"states":{"A":"online"}}}`)
	program, err := CompileFromVersion(spec)
	require.NoError(t, err)
	values, problems, err := program.Apply(json.RawMessage(`{"items":[{"state":"A"}]}`))
	require.NoError(t, err)
	require.Empty(t, problems)
	require.Equal(t, "online", values["items"].([]map[string]any)[0]["code"])
	var object map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(spec, &object))
	object["status_tables"] = json.RawMessage(`{"states":{"A":"offline"}}`)
	changed, err := json.Marshal(object)
	require.NoError(t, err)
	_, err = NewCompiledCache(1).GetOrCompile(7, program.Checksum, changed)
	require.ErrorContains(t, err, "checksum")
}
