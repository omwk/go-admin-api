package ingestion

import (
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"time"

	"github.com/shopspring/decimal"
)

func evalStringUnary(name string, value any) (string, error) {
	text, err := asString(value)
	if err != nil {
		return "", err
	}
	switch name {
	case "trim":
		return strings.TrimSpace(text), nil
	case "upper":
		return strings.ToUpper(text), nil
	case "lower":
		return strings.ToLower(text), nil
	default:
		return "", errors.New("unknown string function")
	}
}

func evalTimestamp(value any, milliseconds bool) (time.Time, error) {
	number, err := asInt64(value)
	if err != nil {
		return time.Time{}, err
	}
	if milliseconds {
		return time.UnixMilli(number).UTC(), nil
	}
	return time.Unix(number, 0).UTC(), nil
}

func evalTimestampString(value, layout any) (time.Time, error) {
	text, err := asString(value)
	if err != nil {
		return time.Time{}, err
	}
	format, err := asString(layout)
	if err != nil {
		return time.Time{}, err
	}
	parsed, err := time.ParseInLocation(format, text, time.UTC)
	if err != nil {
		return time.Time{}, err
	}
	return parsed.UTC(), nil
}

func evalStatus(value, tableName any, tables map[string]map[string]string) (any, error) {
	status, err := asString(value)
	if err != nil {
		return nil, err
	}
	table, err := asString(tableName)
	if err != nil {
		return nil, err
	}
	if mapped, ok := tables[table][status]; ok {
		return mapped, nil
	}
	return statusResult{Value: "unknown", Raw: status}, nil
}

func evalDefault(value, fallback any) any {
	if emptyValue(value) {
		return fallback
	}
	return value
}

func evalCoalesce(values ...any) any {
	for _, value := range values {
		if !emptyValue(value) {
			return value
		}
	}
	return nil
}

func evalConcat(values ...any) (string, error) {
	var builder strings.Builder
	for _, value := range values {
		text, err := asString(value)
		if err != nil {
			return "", err
		}
		builder.WriteString(text)
	}
	return builder.String(), nil
}

func evalSplit(value, separator any) ([]string, error) {
	text, err := asString(value)
	if err != nil {
		return nil, err
	}
	sep, err := asString(separator)
	if err != nil {
		return nil, err
	}
	return strings.Split(text, sep), nil
}

func evalJoin(value, separator any) (string, error) {
	sep, err := asString(separator)
	if err != nil {
		return "", err
	}
	var values []any
	switch typed := value.(type) {
	case []any:
		values = typed
	case []string:
		values = make([]any, len(typed))
		for i := range typed {
			values[i] = typed[i]
		}
	default:
		return "", errors.New("join expects an array")
	}
	parts := make([]string, len(values))
	for i, item := range values {
		parts[i], err = asString(item)
		if err != nil {
			return "", err
		}
	}
	return strings.Join(parts, sep), nil
}

func evalSubstring(value, start, length any) (string, error) {
	text, err := asString(value)
	if err != nil {
		return "", err
	}
	first, err := asInt64(start)
	if err != nil {
		return "", err
	}
	count, err := asInt64(length)
	if err != nil {
		return "", err
	}
	runes := []rune(text)
	if first < 0 || first > int64(len(runes)) || count < 0 {
		return "", nil
	}
	end := first + count
	if end > int64(len(runes)) {
		end = int64(len(runes))
	}
	return string(runes[first:end]), nil
}

func evalReplace(value, old, replacement any) (string, error) {
	text, err := asString(value)
	if err != nil {
		return "", err
	}
	from, err := asString(old)
	if err != nil {
		return "", err
	}
	to, err := asString(replacement)
	if err != nil {
		return "", err
	}
	return strings.ReplaceAll(text, from, to), nil
}

func evalRegexExtract(value, pattern, group any) (string, error) {
	text, err := asString(value)
	if err != nil {
		return "", err
	}
	patternText, err := asString(pattern)
	if err != nil {
		return "", err
	}
	groupNumber, err := asInt64(group)
	if err != nil {
		return "", err
	}
	re, err := regexp.Compile(patternText)
	if err != nil {
		return "", err
	}
	match := re.FindStringSubmatch(text)
	if match == nil || groupNumber < 0 || groupNumber >= int64(len(match)) {
		return "", nil
	}
	return match[groupNumber], nil
}

func evalJSONRaw(value any) ([]byte, error) {
	return json.Marshal(value)
}

func evalDecimal(value any) (decimal.Decimal, error) {
	return asDecimal(value)
}
