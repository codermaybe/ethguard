package ethcodec

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
)

// JSONValuesEqual compares JSON values structurally. Object ordering is
// irrelevant and numbers compare exactly, including equivalent exponents.
// It is a value primitive for generated constraints, not a schema evaluator.
func JSONValuesEqual(left, right json.RawMessage) (bool, error) {
	decode := func(raw json.RawMessage) (any, error) {
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.UseNumber()
		var value any
		if err := decoder.Decode(&value); err != nil {
			return nil, err
		}
		if err := decoder.Decode(new(any)); err != io.EOF {
			return nil, fmt.Errorf("trailing JSON value")
		}
		return value, nil
	}
	a, err := decode(left)
	if err != nil {
		return false, err
	}
	b, err := decode(right)
	if err != nil {
		return false, err
	}
	return equalJSONValue(a, b), nil
}

func equalJSONValue(left, right any) bool {
	switch left := left.(type) {
	case nil:
		return right == nil
	case string:
		value, ok := right.(string)
		return ok && left == value
	case bool:
		value, ok := right.(bool)
		return ok && left == value
	case json.Number:
		value, ok := right.(json.Number)
		return ok && CompareNumbers(left, value) == 0
	case []any:
		values, ok := right.([]any)
		if !ok || len(left) != len(values) {
			return false
		}
		for i, value := range left {
			if !equalJSONValue(value, values[i]) {
				return false
			}
		}
		return true
	case map[string]any:
		values, ok := right.(map[string]any)
		if !ok || len(left) != len(values) {
			return false
		}
		for key, value := range left {
			other, exists := values[key]
			if !exists || !equalJSONValue(value, other) {
				return false
			}
		}
		return true
	}
	return false
}
