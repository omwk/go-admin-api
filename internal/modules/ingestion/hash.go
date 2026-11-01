package ingestion

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"math/big"
	"strings"
	"unicode/utf8"
)

// PayloadHash hashes semantic JSON: object order, formatting, escapes and equal
// decimal representations are ignored. Arrays retain order; integers never use
// float64. Duplicate object keys follow JSONB's last-key-wins semantics.
func PayloadHash(payload json.RawMessage) (string, error) {
	if !utf8.Valid(payload) {
		return "", errors.New("invalid raw JSON")
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return "", errors.New("invalid raw JSON")
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return "", errors.New("invalid raw JSON")
	}
	canonical, err := json.Marshal(normalizeJSON(value))
	if err != nil {
		return "", errors.New("normalize raw JSON")
	}
	hash := sha256.Sum256(canonical)
	return hex.EncodeToString(hash[:]), nil
}

func normalizeJSON(value any) any {
	switch v := value.(type) {
	case map[string]any:
		for key, field := range v {
			v[key] = normalizeJSON(field)
		}
	case []any:
		for i, field := range v {
			v[i] = normalizeJSON(field)
		}
	case json.Number:
		return normalizeNumber(string(v))
	}
	return value
}

func normalizeNumber(value string) json.Number {
	coefficient, exponent, _ := strings.Cut(strings.ToLower(value), "e")
	power := new(big.Int)
	if exponent != "" {
		power.SetString(exponent, 10)
	}
	whole, fraction, _ := strings.Cut(coefficient, ".")
	negative := strings.HasPrefix(whole, "-")
	digits := strings.TrimLeft(strings.TrimPrefix(whole, "-")+fraction, "0")
	if digits == "" {
		return json.Number("0")
	}
	trimmed := strings.TrimRight(digits, "0")
	power.Add(power, big.NewInt(int64(len(digits)-len(trimmed)-len(fraction))))
	if negative {
		trimmed = "-" + trimmed
	}
	return json.Number(trimmed + "e" + power.String())
}
