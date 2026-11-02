package ingestion

import (
	"encoding/json"
	"testing"
)

var benchmarkSpec = []byte(`{"schema_version":1,"resource":"product","fields":[{"target":"external_product_id","source":"$.id","required":true},{"target":"title","source":"$.name"},{"target":"price_cents","source":"$.price","transform":"money_cents(value)"},{"target":"status","source":"$.status","transform":"map_status(value, 'status')"}],"collections":[{"target":"skus","source":"$.skus","key_field":"external_sku_id","fields":[{"target":"external_sku_id","source":"$.id","required":true},{"target":"price_cents","source":"$.price","transform":"money_cents(value)"}]}]}`)

var benchmarkRaw = json.RawMessage(`{"id":"p-1","name":"Product","price":"12.345","status":"active","skus":[{"id":"s-1","price":"1.005"},{"id":"s-2","price":"2.500"}]}`)

func BenchmarkCompileCold(b *testing.B) {
	tables := map[string]map[string]string{"status": {"active": "online"}}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := CompileWithStatusTables(benchmarkSpec, tables); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkApplyHotCache(b *testing.B) {
	tables := map[string]map[string]string{"status": {"active": "online"}}
	compiled, err := CompileWithStatusTables(benchmarkSpec, tables)
	if err != nil {
		b.Fatal(err)
	}
	cache := NewCompiledCache(100)
	cache.Put(1, compiled.Checksum, compiled)
	compiled, ok := cache.Get(1, compiled.Checksum)
	if !ok {
		b.Fatal("compiled mapping was not cached")
	}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, _, err := compiled.Apply(benchmarkRaw); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkEndToEnd100k(b *testing.B) {
	tables := map[string]map[string]string{"status": {"active": "online"}}
	compiled, err := CompileWithStatusTables(benchmarkSpec, tables)
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for n := 0; n < 100000; n++ {
			if _, _, err := compiled.Apply(benchmarkRaw); err != nil {
				b.Fatal(err)
			}
		}
	}
}
