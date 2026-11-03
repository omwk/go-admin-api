package ingestion

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestStructureCanonicalTypesAndFingerprint(t *testing.T) {
	a, err := CollectStructure(json.RawMessage(`{"a":[1,"x",null,true],"nested":{"x":1},"nested.x":2}`))
	require.NoError(t, err)
	require.Equal(t, "bool|null|number|string", a["$.a[]"])
	require.Equal(t, "number", a[`$["nested.x"]`])
	require.Equal(t, "number", a["$.nested.x"])
	b, err := CollectStructure(json.RawMessage(`{"nested.x":9,"nested":{"x":5},"a":[false,null,"y",7,8]}`))
	require.NoError(t, err)
	first, err := a.Fingerprint()
	require.NoError(t, err)
	second, err := b.Fingerprint()
	require.NoError(t, err)
	require.Equal(t, first, second)
	b.Merge(Structure{"$.a[]": "object", "$.extra": "array"})
	require.Equal(t, "bool|null|number|object|string", b["$.a[]"])
	_, err = CollectStructure(json.RawMessage(`{"broken":`))
	require.Error(t, err)
}

func TestStructureDriftAndRequiredCollectionElements(t *testing.T) {
	program, err := Compile([]byte(`{"schema_version":1,"resource":"product","fields":[{"target":"title","source":"$.title","required":true}],"collections":[{"target":"skus","source":"$.skus","key_field":"id","fields":[{"target":"external_sku_id","source":"$.id","required":true}]}]}`))
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"$.title", "$.skus[].id"}, program.RequiredPaths())
	missing, err := program.MissingRequiredPaths(json.RawMessage(`{"title":null,"skus":[{"id":"one"},{}]}`))
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"$.title", "$.skus[].id"}, missing)
	changes := CompareStructure(Structure{"$.title": "string", "$.price": "number"}, Structure{"$.extra": "bool", "$.price": "string"}, program.RequiredPaths())
	require.ElementsMatch(t, []Drift{
		{Kind: "field_added", Path: "$.extra", NewType: "bool", Severity: "info"},
		{Kind: "field_removed", Path: "$.title", OldType: "string", Severity: "critical"},
		{Kind: "type_changed", Path: "$.price", OldType: "number", NewType: "string", Severity: "warning"},
	}, changes)
	require.Equal(t, []Drift{{Kind: "type_changed", Path: "$.title", OldType: "string", NewType: "number", Severity: "critical"}}, CompareStructure(Structure{"$.title": "string"}, Structure{"$.title": "number"}, program.RequiredPaths()))
}
