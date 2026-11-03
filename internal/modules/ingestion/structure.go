package ingestion

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Structure contains paths and sorted type sets, never sample values.
type Structure map[string]string

// CollectStructure reads original JSON, including records rejected by mapping.
func CollectStructure(raw json.RawMessage) (Structure, error) {
	value, err := decodeJSON(raw)
	if err != nil {
		return nil, err
	}
	fields := Structure{}
	walkStructure(fields, "$", value)
	return fields, nil
}

func walkStructure(fields Structure, path string, value any) {
	kind := "null"
	switch v := value.(type) {
	case map[string]any:
		kind = "object"
		for key, child := range v {
			// Bracket quoting prevents dots, brackets and quotes in keys colliding.
			suffix := "[" + strconv.Quote(key) + "]"
			if structureKey.MatchString(key) {
				suffix = "." + key
			}
			walkStructure(fields, path+suffix, child)
		}
	case []any:
		kind = "array"
		for _, child := range v {
			walkStructure(fields, path+"[]", child)
		}
	case string:
		kind = "string"
	case json.Number:
		kind = "number"
	case bool:
		kind = "bool"
	}
	fields[path] = mergeTypes(fields[path], kind)
}

func mergeTypes(a, b string) string {
	set := map[string]bool{}
	for _, kind := range strings.Split(a+"|"+b, "|") {
		if kind != "" {
			set[kind] = true
		}
	}
	keys := make([]string, 0, len(set))
	for key := range set {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return strings.Join(keys, "|")
}

func (s Structure) Merge(other Structure) {
	for path, types := range other {
		s[path] = mergeTypes(s[path], types)
	}
}

func (s Structure) Fingerprint() (string, error) {
	data, err := json.Marshal(s)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

// MissingRequiredPaths checks each collection element before unioning paths.
func (c *Compiled) MissingRequiredPaths(raw json.RawMessage) ([]string, error) {
	root, err := decodeJSON(raw)
	if err != nil {
		return nil, err
	}
	missing := map[string]bool{}
	collectMissing := func(rules []compiledField, value any, prefix string) {
		for _, rule := range rules {
			if !rule.spec.Required {
				continue
			}
			v, found := rule.path.get(value)
			if !found || v == nil {
				missing[prefix+strings.TrimPrefix(canonicalStructurePath(rule.spec.Source), "$")] = true
			}
		}
	}
	collectMissing(c.rules, root, "$")
	for _, collection := range c.collections {
		value, _ := collection.path.get(root)
		items, ok := value.([]any)
		if !ok {
			continue
		}
		for _, item := range items {
			collectMissing(collection.fields, item, canonicalStructurePath(collection.spec.Source)+"[]")
		}
	}
	paths := make([]string, 0, len(missing))
	for path := range missing {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	return paths, nil
}

// RequiredPaths uses the same canonical path notation as raw structure collection.
func (c *Compiled) RequiredPaths() []string {
	paths := []string{}
	for _, field := range c.spec.Fields {
		if field.Required {
			paths = append(paths, canonicalStructurePath(field.Source))
		}
	}
	for _, collection := range c.spec.Collections {
		for _, field := range collection.Fields {
			if field.Required {
				paths = append(paths, canonicalStructurePath(collection.Source)+"[]"+strings.TrimPrefix(canonicalStructurePath(field.Source), "$"))
			}
		}
	}
	return paths
}

func canonicalStructurePath(source string) string {
	return structureIndex.ReplaceAllString(source, "[]")
}

var structureKey = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)
var structureIndex = regexp.MustCompile(`\[(?:[0-9]+|\*)\]`)
