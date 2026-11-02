package ingestion

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/shopspring/decimal"
)

type statusResult struct{ Value, Raw string }

func emptyValue(value any) bool {
	if value == nil {
		return true
	}
	text, ok := value.(string)
	return ok && strings.TrimSpace(text) == ""
}

func asString(value any) (string, error) {
	if value == nil {
		return "", errors.New("value is null")
	}
	switch typed := value.(type) {
	case string:
		return typed, nil
	case json.Number:
		return typed.String(), nil
	case decimal.Decimal:
		return typed.String(), nil
	default:
		return fmt.Sprint(value), nil
	}
}

func asInt(value any) (int, error) {
	number, err := asInt64(value)
	if err != nil {
		return 0, err
	}
	converted := int(number)
	if int64(converted) != number {
		return 0, errors.New("integer overflows int")
	}
	return converted, nil
}

func asInt64(value any) (int64, error) {
	number, err := asDecimal(value)
	if err != nil {
		return 0, err
	}
	if !number.IsInteger() {
		return 0, errors.New("value is not an integer")
	}
	return checkedInt64(number)
}

func asDecimal(value any) (decimal.Decimal, error) {
	if value == nil {
		return decimal.Decimal{}, errors.New("value is null")
	}
	if number, ok := value.(decimal.Decimal); ok {
		return number, nil
	}
	text, err := asString(value)
	if err != nil {
		return decimal.Decimal{}, err
	}
	number, err := decimal.NewFromString(strings.TrimSpace(text))
	if err != nil {
		return decimal.Decimal{}, errors.New("value is not decimal")
	}
	return number, nil
}

func moneyCents(value any) (int64, error) {
	number, err := asDecimal(value)
	if err != nil {
		return 0, err
	}
	// Round before scaling so the decimal operation is explicit and never uses binary floats.
	cents := number.Round(2).Mul(decimal.NewFromInt(100)).Round(0)
	return checkedInt64(cents)
}

func checkedInt64(number decimal.Decimal) (int64, error) {
	const maxInt64 = int64(1<<63 - 1)
	const minInt64 = -1 << 63
	maxDecimal := decimal.NewFromInt(maxInt64)
	minDecimal := decimal.NewFromInt(minInt64)
	if number.Cmp(maxDecimal) > 0 || number.Cmp(minDecimal) < 0 {
		return 0, errors.New("integer overflows int64")
	}
	return number.IntPart(), nil
}

func asBool(value any) (bool, error) {
	switch typed := value.(type) {
	case bool:
		return typed, nil
	case json.Number:
		return boolText(typed.String())
	case string:
		return boolText(typed)
	default:
		return false, errors.New("value is not boolean")
	}
}

func boolText(value string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "y", "yes":
		return true, nil
	case "0", "false", "n", "no":
		return false, nil
	default:
		return false, errors.New("value is not boolean")
	}
}
