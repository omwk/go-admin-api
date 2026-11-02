package ingestion

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/expr-lang/expr"
	"github.com/expr-lang/expr/vm"
	"github.com/shopspring/decimal"
)

const FunctionSetVersion = "mapping-functions-v2"

func compileExpression(raw string, tables map[string]map[string]string) (*vm.Program, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, errors.New("empty transform")
	}
	program, err := expr.Compile(raw, expressionOptions(tables)...)
	if err != nil {
		return nil, fmt.Errorf("compile transform: %w", err)
	}
	return program, nil
}

func expressionOptions(tables map[string]map[string]string) []expr.Option {
	options := []expr.Option{
		expr.Env(map[string]any{"value": any(nil)}),
		expr.DisableAllBuiltins(),
	}
	options = append(options,
		expr.Function("trim", func(params ...any) (any, error) { return evalStringUnary("trim", params[0]) }, new(func(any) string)),
		expr.Function("upper", func(params ...any) (any, error) { return evalStringUnary("upper", params[0]) }, new(func(any) string)),
		expr.Function("lower", func(params ...any) (any, error) { return evalStringUnary("lower", params[0]) }, new(func(any) string)),
		expr.Function("int", func(params ...any) (any, error) { return asInt(params[0]) }, new(func(any) int)),
		expr.Function("decimal", func(params ...any) (any, error) { return evalDecimal(params[0]) }, new(func(any) decimal.Decimal)),
		expr.Function("money_cents", func(params ...any) (any, error) { return moneyCents(params[0]) }, new(func(any) int64)),
		expr.Function("ts_unix", func(params ...any) (any, error) { return evalTimestamp(params[0], false) }, new(func(any) time.Time)),
		expr.Function("ts_ms", func(params ...any) (any, error) { return evalTimestamp(params[0], true) }, new(func(any) time.Time)),
		expr.Function("ts_str", func(params ...any) (any, error) { return evalTimestampString(params[0], params[1]) }, new(func(any, string) time.Time)),
		expr.Function("map_status", func(params ...any) (any, error) { return evalStatus(params[0], params[1], tables) }, new(func(any, string) any)),
		expr.Function("default", func(params ...any) (any, error) { return evalDefault(params[0], params[1]), nil }, new(func(any, any) any)),
		expr.Function("split", func(params ...any) (any, error) { return evalSplit(params[0], params[1]) }, new(func(any, string) []string)),
		expr.Function("join", func(params ...any) (any, error) { return evalJoin(params[0], params[1]) }, new(func(any, string) string)),
		expr.Function("substr", func(params ...any) (any, error) { return evalSubstring(params[0], params[1], params[2]) }, new(func(any, int, int) string)),
		expr.Function("replace", func(params ...any) (any, error) { return evalReplace(params[0], params[1], params[2]) }, new(func(any, string, string) string)),
		expr.Function("regex_extract", func(params ...any) (any, error) { return evalRegexExtract(params[0], params[1], params[2]) }, new(func(any, string, int) string)),
		expr.Function("to_bool", func(params ...any) (any, error) { return asBool(params[0]) }, new(func(any) bool)),
		expr.Function("json_raw", func(params ...any) (any, error) { return evalJSONRaw(params[0]) }, new(func(any) []byte)),
	)
	options = append(options,
		expr.Function("coalesce", func(params ...any) (any, error) { return evalCoalesce(params...), nil }, variadicSignatures(1, 64, reflect.TypeOf((*any)(nil)).Elem())...),
		expr.Function("concat", func(params ...any) (any, error) { return evalConcat(params...) }, variadicSignatures(1, 64, reflect.TypeOf((*string)(nil)).Elem())...),
	)
	return options
}

func variadicSignatures(minArgs, maxArgs int, output reflect.Type) []any {
	anyType := reflect.TypeOf((*any)(nil)).Elem()
	signatures := make([]any, 0, maxArgs-minArgs+1)
	for count := minArgs; count <= maxArgs; count++ {
		inputs := make([]reflect.Type, count)
		for i := range inputs {
			inputs[i] = anyType
		}
		functionType := reflect.FuncOf(inputs, []reflect.Type{output}, false)
		signatures = append(signatures, reflect.New(functionType).Interface())
	}
	return signatures
}
