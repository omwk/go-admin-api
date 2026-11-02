package ingestion

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/PaesslerAG/jsonpath"
)

const maxMappingPathDepth = 32

type compiledPath struct {
	raw   string
	depth int
}

func compilePath(raw string) (compiledPath, error) {
	raw = strings.TrimSpace(raw)
	if raw != "$" && !strings.HasPrefix(raw, "$.") {
		return compiledPath{}, fmt.Errorf("json path must start with $")
	}
	if raw == "$." || strings.HasSuffix(raw, ".") {
		return compiledPath{}, fmt.Errorf("json path has empty segment")
	}
	depth := pathDepth(raw)
	if depth > maxMappingPathDepth {
		return compiledPath{}, fmt.Errorf("json path depth %d exceeds maximum %d", depth, maxMappingPathDepth)
	}
	if _, err := jsonpath.New(raw); err != nil {
		return compiledPath{}, fmt.Errorf("invalid json path: %w", err)
	}
	return compiledPath{raw: raw, depth: depth}, nil
}

func pathDepth(raw string) int {
	if raw == "$" {
		return 0
	}
	depth := 0
	inBracket := false
	for i := 2; i < len(raw); i++ {
		switch raw[i] {
		case '[':
			if !inBracket {
				depth++
				inBracket = true
			}
		case ']':
			inBracket = false
		case '.':
			if !inBracket {
				depth++
			}
		}
	}
	if depth == 0 {
		return 1
	}
	return depth + 1
}

func (p compiledPath) get(root any) (any, bool) {
	value, err := jsonpath.Get(p.raw, root)
	if err != nil {
		return nil, false
	}
	return value, true
}

func decodeJSON(raw json.RawMessage) (any, error) {
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	return value, nil
}
