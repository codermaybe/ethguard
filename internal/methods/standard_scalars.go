package methods

import (
	"encoding/json"
	"fmt"

	"github.com/codermaybe/ethguard/ethapi"
	"github.com/codermaybe/ethguard/internal/ethcodec"
)

type scalarCompatibility func(json.RawMessage) (json.RawMessage, error)

// Each binding supplies its actual generated method root. Compatibility only
// prepares spelling; the constructor remains responsible for schema validity.
// Method wrappers own the stable scalar diagnostic class. Generated schema
// paths remain in the wrapped cause, rather than replacing that attribution.
func standardQuantityResult(parse func(json.RawMessage) (ethapi.Quantity, error), compatibility scalarCompatibility) func(json.RawMessage) (string, error) {
	return func(raw json.RawMessage) (string, error) {
		candidate, err := compatibility(raw)
		if err != nil {
			return "", fmt.Errorf("hex quantity: %w", err)
		}
		value, err := parse(candidate)
		if err != nil {
			return "", fmt.Errorf("hex quantity: %w", err)
		}
		return ethcodec.QuantityDecimal(string(value))
	}
}
func standardNullableQuantityResult(parse func(json.RawMessage) (*ethapi.Quantity, error), allowNull bool) func(json.RawMessage) (string, error) {
	return func(raw json.RawMessage) (string, error) {
		candidate := raw
		if !ethcodec.IsJSONNull(raw) {
			var err error
			candidate, err = ethcodec.FoldWireHexCase(raw)
			if err != nil {
				return "", fmt.Errorf("hex quantity: %w", err)
			}
		}
		value, err := parse(candidate)
		if err != nil {
			return "", fmt.Errorf("hex quantity: %w", err)
		}
		if value == nil {
			if allowNull {
				return "null", nil
			}
			return "", fmt.Errorf("hex quantity: null is not admitted by this method")
		}
		return ethcodec.QuantityDecimal(string(*value))
	}
}
func standardDataResult(parse func(json.RawMessage) (ethapi.Data, error), bytes int) func(json.RawMessage) (string, error) {
	kind := "hex data"
	if bytes > 0 {
		kind = "storage word"
	}
	return func(raw json.RawMessage) (string, error) {
		candidate, err := ethcodec.FoldLowerPrefixHexCase(raw)
		if err != nil {
			return "", fmt.Errorf("%s: %w", kind, err)
		}
		value, err := parse(candidate)
		if err != nil {
			return "", fmt.Errorf("%s: %w", kind, err)
		}
		if bytes > 0 && len(value) != 2+2*bytes {
			return "", fmt.Errorf("storage word: expected %d-byte data", bytes)
		}
		return string(value), nil
	}
}
func normalizeNetworkVersion(raw json.RawMessage) (string, error) {
	candidate, err := ethcodec.FoldLegacyDecimal(raw)
	if err != nil {
		return "", err
	}
	value, err := ethapi.NewVersionResult(candidate)
	if err != nil {
		return "", fmt.Errorf("decimal string: %w", err)
	}
	return string(value), nil
}

func standardQuantityEncoding(parse func(json.RawMessage) (ethapi.Quantity, error), compatibility scalarCompatibility) func(json.RawMessage) (json.RawMessage, error) {
	return func(raw json.RawMessage) (json.RawMessage, error) {
		candidate, err := compatibility(raw)
		if err != nil {
			return nil, fmt.Errorf("hex quantity: %w", err)
		}
		value, err := parse(candidate)
		if err != nil {
			return nil, fmt.Errorf("hex quantity: %w", err)
		}
		return json.Marshal(value)
	}
}
func standardNullableQuantityEncoding(parse func(json.RawMessage) (*ethapi.Quantity, error), allowNull bool) func(json.RawMessage) (json.RawMessage, error) {
	normalize := standardNullableQuantityResult(parse, allowNull)
	return func(raw json.RawMessage) (json.RawMessage, error) {
		if _, err := normalize(raw); err != nil {
			return nil, err
		}
		if ethcodec.IsJSONNull(raw) {
			return json.RawMessage("null"), nil
		}
		return ethcodec.FoldWireHexCase(raw)
	}
}
func standardDataEncoding(parse func(json.RawMessage) (ethapi.Data, error), size int) func(json.RawMessage) (json.RawMessage, error) {
	normalize := standardDataResult(parse, size)
	return func(raw json.RawMessage) (json.RawMessage, error) {
		value, err := normalize(raw)
		if err != nil {
			return nil, err
		}
		return json.Marshal(value)
	}
}
func encodeNetworkVersion(raw json.RawMessage) (json.RawMessage, error) {
	text, err := normalizeNetworkVersion(raw)
	if err != nil {
		return nil, err
	}
	return json.Marshal(text)
}
func encodeListening(raw json.RawMessage) (json.RawMessage, error) {
	value, err := ethapi.NewListeningResult(raw)
	if err != nil {
		return nil, err
	}
	return json.Marshal(value)
}
