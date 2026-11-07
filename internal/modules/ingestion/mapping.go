package ingestion

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/expr-lang/expr"
	"github.com/expr-lang/expr/vm"
)

var canonicalTargets = map[string]bool{
	"external_product_id": true, "title": true, "price_cents": true, "currency": true,
	"status": true, "stock_total": true, "external_sku_id": true, "sku_id": true,
	"skus": true, "items": true,
	"price": true, "stock": true, "qty": true, "barcode": true, "code": true,
	"description": true, "brand": true, "category_id": true, "placed_at": true,
	"total_amount_cents": true, "external_order_id": true, "name": true,
}
var targetPattern = regexp.MustCompile(`^[a-z][a-z0-9_]*(\.[a-z][a-z0-9_]*)*$`)

type compiledField struct {
	spec      FieldSpec
	path      compiledPath
	transform *vm.Program
}
type compiledCollection struct {
	spec   CollectionSpec
	path   compiledPath
	fields []compiledField
}

// Compiled is an immutable, goroutine-safe mapping program.
type Compiled struct {
	spec            Spec
	rules           []compiledField
	collections     []compiledCollection
	statusTables    map[string]map[string]string
	Checksum        string
	FunctionVersion string
}

// Resource returns the frozen mapping's canonical resource.
func (c *Compiled) Resource() string { return c.spec.Resource }

// Compile validates and compiles a mapping without mutable status tables.
func Compile(specJSON []byte) (*Compiled, error) {
	spec, err := decodeSpec(specJSON)
	if err != nil {
		return nil, err
	}
	return compileSpec(specJSON, spec)
}

// CompileWithStatusTables compiles a mapping and freezes its status snapshot.
func CompileWithStatusTables(specJSON []byte, tables map[string]map[string]string) (*Compiled, error) {
	final, err := snapshotSpec(specJSON, tables)
	if err != nil {
		return nil, err
	}
	return CompileFromVersion(final)
}

func compileSpec(specJSON []byte, spec Spec) (*Compiled, error) {
	if err := validateOrderSpec(spec); err != nil {
		return nil, err
	}
	if spec.SchemaVersion <= 0 || spec.Resource == "" || len(spec.Fields) == 0 {
		return nil, errors.New("mapping schema_version, resource and fields are required")
	}
	if !validMappingResource(spec.Resource) {
		return nil, fmt.Errorf("unknown mapping resource %q", spec.Resource)
	}
	compiled := &Compiled{spec: spec, statusTables: cloneStatusTables(spec.StatusTables), FunctionVersion: FunctionSetVersion}
	seen := make(map[string]bool)
	for _, field := range spec.Fields {
		rule, err := compileField(field, seen, compiled.statusTables)
		if err != nil {
			return nil, err
		}
		compiled.rules = append(compiled.rules, rule)
	}
	collectionSeen := make(map[string]bool)
	for _, collection := range spec.Collections {
		if !validTarget(collection.Target) || collection.Target == "" || seen[collection.Target] || collectionSeen[collection.Target] {
			return nil, fmt.Errorf("invalid or duplicate collection target %q", collection.Target)
		}
		collectionSeen[collection.Target] = true
		path, err := compilePath(collection.Source)
		if err != nil {
			return nil, err
		}
		if collection.KeyField == "" {
			return nil, fmt.Errorf("collection %q key_field is required", collection.Target)
		}
		cc := compiledCollection{spec: collection, path: path}
		fieldSeen := make(map[string]bool)
		for _, field := range collection.Fields {
			rule, err := compileField(field, fieldSeen, compiled.statusTables)
			if err != nil {
				return nil, err
			}
			cc.fields = append(cc.fields, rule)
		}
		compiled.collections = append(compiled.collections, cc)
	}
	checksum, err := mappingChecksum(specJSON)
	if err != nil {
		return nil, err
	}
	compiled.Checksum = checksum
	return compiled, nil
}

func compileField(field FieldSpec, seen map[string]bool, tables map[string]map[string]string) (compiledField, error) {
	if field.Target == "" || !validTarget(field.Target) {
		return compiledField{}, fmt.Errorf("unknown target %q", field.Target)
	}
	if seen[field.Target] {
		return compiledField{}, fmt.Errorf("duplicate target %q", field.Target)
	}
	seen[field.Target] = true
	if field.Source == "" {
		return compiledField{}, fmt.Errorf("source is required for target %q", field.Target)
	}
	path, err := compilePath(field.Source)
	if err != nil {
		return compiledField{}, fmt.Errorf("target %s: %w", field.Target, err)
	}
	if field.Required && len(field.Default) > 0 && string(field.Default) != "null" {
		return compiledField{}, fmt.Errorf("required target %q cannot have a default", field.Target)
	}
	rule := compiledField{spec: field, path: path}
	if field.Transform != "" {
		program, err := compileExpression(field.Transform, tables)
		if err != nil {
			return compiledField{}, fmt.Errorf("target %s: %w", field.Target, err)
		}
		rule.transform = program
	}
	if len(field.Default) > 0 && !json.Valid(field.Default) {
		return compiledField{}, fmt.Errorf("default for target %q is invalid JSON", field.Target)
	}
	return rule, nil
}

func validTarget(target string) bool {
	if strings.HasPrefix(target, "ext.") {
		name := strings.TrimPrefix(target, "ext.")
		return len(name) > 0 && !strings.HasPrefix(name, ReservedMappingExtKey) && targetPattern.MatchString(name)
	}
	if strings.Contains(target, ".") || !targetPattern.MatchString(target) {
		return false
	}
	return canonicalTargets[target] || orderHeaderTargets[target] || orderItemTargets[target]
}

const ReservedMappingExtKey = "_ingestion"

// Apply interprets one raw JSON record without writing anywhere.
func (c *Compiled) Apply(raw json.RawMessage) (map[string]any, []FieldError, error) {
	if c == nil {
		return nil, nil, errors.New("compiled mapping is nil")
	}
	root, err := decodeJSON(raw)
	if err != nil {
		return nil, nil, fmt.Errorf("decode raw payload: %w", err)
	}
	result := make(map[string]any)
	var fieldErrors []FieldError
	for _, rule := range c.rules {
		value, ok := rule.path.get(root)
		if !ok || value == nil {
			if rule.spec.Required {
				fieldErrors = append(fieldErrors, FieldError{Target: rule.spec.Target, Source: rule.spec.Source, Kind: "missing_required", Message: "required source is missing", Fatal: true})
				continue
			}
			if len(rule.spec.Default) > 0 && string(rule.spec.Default) != "null" {
				value, err = decodeJSON(rule.spec.Default)
				if err != nil {
					return nil, nil, err
				}
			} else {
				continue
			}
		}
		if rule.transform != nil {
			value, err = expr.Run(rule.transform, map[string]any{"value": value})
			if err != nil {
				fieldErrors = append(fieldErrors, FieldError{Target: rule.spec.Target, Source: rule.spec.Source, Kind: "type_mismatch", Message: err.Error(), Fatal: true})
				continue
			}
		}
		if status, ok := value.(statusResult); ok {
			value = status.Value
			rawTarget := "ext.platform_status_raw"
			if c.spec.Resource == string(ResourceOrder) {
				rawTarget = "platform_status"
			}
			if err := putTarget(result, rawTarget, status.Raw); err != nil {
				return nil, nil, err
			}
			if rule.spec.Target == "status" && c.spec.Resource == string(ResourceProduct) {
				fieldErrors = append(fieldErrors, FieldError{Target: rule.spec.Target, Source: rule.spec.Source, Kind: "invalid_value", Message: "unknown listing status", Fatal: true})
			}
		}
		if err := putTarget(result, rule.spec.Target, value); err != nil {
			return nil, nil, err
		}
	}
	for _, collection := range c.collections {
		c.applyCollection(root, result, &fieldErrors, collection)
	}
	return result, fieldErrors, nil
}

func (c *Compiled) applyCollection(root any, result map[string]any, fieldErrors *[]FieldError, collection compiledCollection) {
	value, ok := collection.path.get(root)
	if !ok || value == nil {
		return
	}
	items, ok := value.([]any)
	if !ok {
		*fieldErrors = append(*fieldErrors, FieldError{Target: collection.spec.Target, Source: collection.spec.Source, Kind: "type_mismatch", Message: "collection source is not an array", Fatal: true})
		return
	}
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		row := make(map[string]any)
		for _, rule := range collection.fields {
			fieldValue, found := rule.path.get(item)
			if !found || fieldValue == nil {
				if rule.spec.Required {
					*fieldErrors = append(*fieldErrors, FieldError{Target: collection.spec.Target + "." + rule.spec.Target, Source: rule.spec.Source, Kind: "missing_required", Message: "required source is missing", Fatal: true})
					continue
				}
				if len(rule.spec.Default) > 0 && string(rule.spec.Default) != "null" {
					decoded, decodeErr := decodeJSON(rule.spec.Default)
					if decodeErr != nil {
						*fieldErrors = append(*fieldErrors, FieldError{Target: collection.spec.Target + "." + rule.spec.Target, Source: rule.spec.Source, Kind: "type_mismatch", Message: decodeErr.Error(), Fatal: true})
						continue
					}
					fieldValue = decoded
				} else {
					continue
				}
			}
			var err error
			if rule.transform != nil {
				fieldValue, err = expr.Run(rule.transform, map[string]any{"value": fieldValue})
				if err != nil {
					*fieldErrors = append(*fieldErrors, FieldError{Target: collection.spec.Target + "." + rule.spec.Target, Source: rule.spec.Source, Kind: "type_mismatch", Message: err.Error(), Fatal: true})
					continue
				}
			}
			if status, ok := fieldValue.(statusResult); ok {
				fieldValue = status.Value
			}
			if err := putTarget(row, rule.spec.Target, fieldValue); err != nil {
				*fieldErrors = append(*fieldErrors, FieldError{Target: collection.spec.Target + "." + rule.spec.Target, Source: rule.spec.Source, Kind: "mapping_error", Message: err.Error(), Fatal: true})
			}
		}
		out = append(out, row)
	}
	result[collection.spec.Target] = out
}

func putTarget(result map[string]any, target string, value any) error {
	if strings.HasPrefix(target, "ext.") {
		ext, _ := result["ext"].(map[string]any)
		if ext == nil {
			ext = make(map[string]any)
			result["ext"] = ext
		}
		ext[strings.TrimPrefix(target, "ext.")] = value
		return nil
	}
	result[target] = value
	return nil
}

// ApplyMapped attaches source metadata to an Apply result for a target writer.
func (c *Compiled) ApplyMapped(raw json.RawMessage, metadata Mapped) (Mapped, []FieldError, error) {
	values, fieldErrors, err := c.Apply(raw)
	if err != nil {
		return Mapped{}, nil, err
	}
	metadata.Values = values
	metadata.Collections = make(map[string][]map[string]any)
	for key, value := range values {
		if rows, ok := value.([]map[string]any); ok {
			metadata.Collections[key] = rows
			delete(metadata.Values, key)
		}
	}
	metadata.MappingChecksum = c.Checksum
	return metadata, fieldErrors, nil
}

func cloneStatusTables(source map[string]map[string]string) map[string]map[string]string {
	result := make(map[string]map[string]string, len(source))
	for key, table := range source {
		result[key] = make(map[string]string, len(table))
		for item, value := range table {
			result[key][item] = value
		}
	}
	return result
}
func mappingChecksum(specJSON []byte) (string, error) {
	var spec any
	decoder := json.NewDecoder(strings.NewReader(string(specJSON)))
	decoder.UseNumber()
	if err := decoder.Decode(&spec); err != nil {
		return "", err
	}
	// Hash only immutable release inputs. JSONB normalizes numbers, key order
	// and whitespace, so use the same semantic normalization as raw payloads.
	payload := struct {
		Spec      any    `json:"spec"`
		Functions string `json:"functions"`
	}{normalizeJSON(spec), FunctionSetVersion}
	canonical, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(canonical)
	return hex.EncodeToString(sum[:]), nil
}

func validMappingResource(resource string) bool {
	switch ResourceType(resource) {
	case ResourceProduct, ResourceOrder, ResourceInventory, ResourceCategory:
		return true
	}
	return false
}
