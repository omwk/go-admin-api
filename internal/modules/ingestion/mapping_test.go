package ingestion

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/expr-lang/expr"
	"github.com/stretchr/testify/require"
)

func TestCompileRejectsStaticSpecErrors(t *testing.T) {
	tests := []struct {
		name string
		spec string
	}{
		{"duplicate target", `{"schema_version":1,"resource":"product","fields":[{"target":"title","source":"$.a"},{"target":"title","source":"$.b"}]}`},
		{"field and collection duplicate target", `{"schema_version":1,"resource":"product","fields":[{"target":"skus","source":"$.a"}],"collections":[{"target":"skus","source":"$.items","key_field":"id","fields":[{"target":"external_sku_id","source":"$.id"}]}]}`},
		{"unknown target", `{"schema_version":1,"resource":"product","fields":[{"target":"not_registered","source":"$.a"}]}`},
		{"reserved ext target", `{"schema_version":1,"resource":"product","fields":[{"target":"ext._ingestion.source_order","source":"$.a"}]}`},
		{"unknown function", `{"schema_version":1,"resource":"product","fields":[{"target":"title","source":"$.a","transform":"not_registered(value)"}]}`},
		{"unknown variable", `{"schema_version":1,"resource":"product","fields":[{"target":"title","source":"$.a","transform":"other"}]}`},
		{"map status arity", `{"schema_version":1,"resource":"product","fields":[{"target":"status","source":"$.a","transform":"map_status(value)"}]}`},
		{"money cents arity", `{"schema_version":1,"resource":"product","fields":[{"target":"price_cents","source":"$.a","transform":"money_cents(value, 1)"}]}`},
		{"invalid expression type", `{"schema_version":1,"resource":"product","fields":[{"target":"title","source":"$.a","transform":"substr(value, 'bad', 1)"}]}`},
		{"required default", `{"schema_version":1,"resource":"product","fields":[{"target":"title","source":"$.a","required":true,"default":"fallback"}]}`},
		{"multiple JSON values", `{"schema_version":1,"resource":"product","fields":[{"target":"title","source":"$.a"}]} {"schema_version":1}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := Compile([]byte(test.spec))
			require.Error(t, err)
		})
	}
}

func TestCompileRejectsInvalidJSONPathAndDepth(t *testing.T) {
	deep := "$"
	for i := 0; i < maxMappingPathDepth; i++ {
		deep += ".a"
	}
	tooDeep := deep + ".a"
	for _, source := range []string{"$.a[", tooDeep} {
		spec := `{"schema_version":1,"resource":"product","fields":[{"target":"title","source":"` + source + `"}]}`
		_, err := Compile([]byte(spec))
		require.Error(t, err)
	}
	_, err := Compile([]byte(`{"schema_version":1,"resource":"product","fields":[{"target":"title","source":"a.b"}]}`))
	require.Error(t, err)
	if !strings.Contains(err.Error(), "json path") {
		t.Fatalf("expected json path error, got %v", err)
	}
}

func TestBuiltinFunctionsCompileAndRun(t *testing.T) {
	tests := []struct {
		name       string
		expression string
		value      any
		want       any
	}{
		{"trim", "trim(value)", "  x ", "x"},
		{"upper", "upper(value)", "x", "X"},
		{"lower", "lower(value)", "X", "x"},
		{"int", "int(value)", json.Number("2"), int(2)},
		{"decimal", "decimal(value)", "12.50", "12.5"},
		{"money_cents", "money_cents(value)", "12.345", int64(1235)},
		{"ts_unix", "ts_unix(value)", json.Number("1700000000"), time.Unix(1700000000, 0).UTC()},
		{"ts_ms", "ts_ms(value)", json.Number("1700000000000"), time.UnixMilli(1700000000000).UTC()},
		{"ts_str", "ts_str(value, '2006-01-02')", "2024-01-02", time.Date(2024, time.January, 2, 0, 0, 0, 0, time.UTC)},
		{"map_status", "map_status(value, 'status')", "active", "online"},
		{"default", "default(value, 'fallback')", nil, "fallback"},
		{"coalesce", "coalesce(value, 'fallback')", nil, "fallback"},
		{"concat", "concat(value, '-', 'b')", "a", "a-b"},
		{"split", "split(value, ',')", "a,b", []string{"a", "b"}},
		{"join", "join(value, '-')", []any{"a", "b"}, "a-b"},
		{"substr", "substr(value, 1, 2)", "abcd", "bc"},
		{"replace", "replace(value, 'a', 'b')", "a1", "b1"},
		{"regex_extract", "regex_extract(value, '(x+)', 1)", "abxxc", "xx"},
		{"to_bool", "to_bool(value)", "Y", true},
		{"json_raw", "json_raw(value)", map[string]any{"a": json.Number("1")}, []byte(`{"a":1}`)},
	}
	tables := map[string]map[string]string{"status": {"active": "online"}}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			program, err := compileExpression(test.expression, tables)
			require.NoError(t, err)
			got, err := expr.Run(program, map[string]any{"value": test.value})
			require.NoError(t, err)
			if test.name == "decimal" {
				require.Equal(t, test.want, got.(interface{ String() string }).String())
				return
			}
			require.Equal(t, test.want, got)
		})
	}
}

func TestMoneyExponentKeepsNegativeSign(t *testing.T) {
	spec := []byte(`{"schema_version":1,"resource":"product","fields":[{"target":"price_cents","source":"$.price","transform":"money_cents(value)"}]}`)
	c, err := Compile(spec)
	require.NoError(t, err)
	got, fieldErrors, err := c.Apply(json.RawMessage(`{"price":"-1.005e0"}`))
	require.NoError(t, err)
	require.Empty(t, fieldErrors)
	require.Equal(t, int64(-101), got["price_cents"])
}

func TestCompiledApplyProductAndCollection(t *testing.T) {
	spec := `{"schema_version":1,"resource":"product","key_field":"external_product_id","fields":[{"target":"external_product_id","source":"$.id","required":true},{"target":"title","source":"$.name","required":true},{"target":"price_cents","source":"$.price","transform":"money_cents(value)"},{"target":"status","source":"$.state","transform":"map_status(value, 'product_status')"},{"target":"currency","source":"$.currency"}],"collections":[{"target":"skus","source":"$.skus","key_field":"external_sku_id","fields":[{"target":"external_sku_id","source":"$.id","required":true},{"target":"title","source":"$.name"},{"target":"price_cents","source":"$.price","transform":"money_cents(value)"}]}]}`
	tables := map[string]map[string]string{"product_status": {"ACTIVE": "online"}}
	c, err := CompileWithStatusTables([]byte(spec), tables)
	require.NoError(t, err)
	got, fieldErrors, err := c.Apply(json.RawMessage(`{"id":"p1","name":" A ","price":"12.345","state":"ACTIVE","currency":"CNY","skus":[{"id":"s1","name":"Red","price":"1.005"}]}`))
	require.NoError(t, err)
	require.Empty(t, fieldErrors)
	require.Equal(t, int64(1235), got["price_cents"])
	require.Equal(t, "online", got["status"])
	require.Len(t, got["skus"], 1)
}

func TestRequiredMissingAndUnknownListingStatusAreFatal(t *testing.T) {
	spec := `{"schema_version":1,"resource":"product","fields":[{"target":"title","source":"$.title","required":true},{"target":"status","source":"$.status","transform":"map_status(value, 'status')"}]}`
	c, err := Compile([]byte(spec))
	require.NoError(t, err)
	_, fieldErrors, err := c.Apply(json.RawMessage(`{"status":"paused"}`))
	require.NoError(t, err)
	require.Len(t, fieldErrors, 2)
	require.Equal(t, "missing_required", fieldErrors[0].Kind)
	require.Equal(t, "invalid_value", fieldErrors[1].Kind)
}

func TestCompiledCacheInvalidatesChecksum(t *testing.T) {
	spec := []byte(`{"schema_version":1,"resource":"product","fields":[{"target":"title","source":"$.title"}]}`)
	c, err := Compile(spec)
	require.NoError(t, err)
	cache := NewCompiledCache(100)
	cache.Put(7, c.Checksum, c)
	got, ok := cache.Get(7, c.Checksum)
	require.True(t, ok)
	require.Same(t, c, got)
	_, ok = cache.Get(7, "changed")
	require.False(t, ok)
	changed, err := CompileWithStatusTables(spec, map[string]map[string]string{"status": {"new": "online"}})
	require.NoError(t, err)
	require.NotEqual(t, c.Checksum, changed.Checksum)
}

func TestPublishGateDryRunAndCAS(t *testing.T) {
	spec := []byte(`{"schema_version":1,"resource":"product","fields":[{"target":"title","source":"$.title","required":true}]}`)
	backend := &MemoryPublishBackend{Samples: []json.RawMessage{json.RawMessage(`{"title":"ok"}`)}}
	gate := PublishGate{Backend: backend}
	compiled, result, err := gate.Publish(context.Background(), PublishRequest{VersionID: 3, Spec: spec})
	require.NoError(t, err)
	require.Equal(t, 1, result.Checked)
	require.Equal(t, int64(3), backend.ActiveID)
	require.NotEmpty(t, compiled.Checksum)
}
