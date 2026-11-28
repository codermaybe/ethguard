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
		value, _, err := prepareNullableQuantity(raw, parse, allowNull)
		if err != nil {
			return "", err
		}
		if value == nil {
			return "null", nil
		}
		return ethcodec.QuantityDecimal(string(*value))
	}
}

func prepareNullableQuantity(raw json.RawMessage, parse func(json.RawMessage) (*ethapi.Quantity, error), allowNull bool) (*ethapi.Quantity, json.RawMessage, error) {
	candidate := raw
	if !ethcodec.IsJSONNull(raw) {
		var err error
		candidate, err = ethcodec.FoldWireHexCase(raw)
		if err != nil {
			return nil, nil, fmt.Errorf("hex quantity: %w", err)
		}
	}
	value, err := parse(candidate)
	if err != nil {
		return nil, nil, fmt.Errorf("hex quantity: %w", err)
	}
	if value == nil && !allowNull {
		return nil, nil, fmt.Errorf("hex quantity: null is not admitted by this method")
	}
	return value, candidate, nil
}

func standardDataResult(parse func(json.RawMessage) (ethapi.Data, error), bytes int) func(json.RawMessage) (string, error) {
	return func(raw json.RawMessage) (string, error) {
		value, _, err := prepareStandardData(raw, parse, bytes)
		return string(value), err
	}
}

func prepareStandardData(raw json.RawMessage, parse func(json.RawMessage) (ethapi.Data, error), bytes int) (ethapi.Data, json.RawMessage, error) {
	kind := "hex data"
	if bytes > 0 {
		kind = "storage word"
	}
	candidate, err := ethcodec.FoldLowerPrefixHexCase(raw)
	if err != nil {
		return "", nil, fmt.Errorf("%s: %w", kind, err)
	}
	value, err := parse(candidate)
	if err != nil {
		return "", nil, fmt.Errorf("%s: %w", kind, err)
	}
	if bytes > 0 && len(value) != 2+2*bytes {
		return "", nil, fmt.Errorf("storage word: expected %d-byte data", bytes)
	}
	return value, candidate, nil
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
		if _, err := parse(candidate); err != nil {
			return nil, fmt.Errorf("hex quantity: %w", err)
		}
		// Quantity constructors preserve the validated string; compatibility
		// already encoded that spelling as compact JSON.
		return candidate, nil
	}
}
func standardNullableQuantityEncoding(parse func(json.RawMessage) (*ethapi.Quantity, error), allowNull bool) func(json.RawMessage) (json.RawMessage, error) {
	return func(raw json.RawMessage) (json.RawMessage, error) {
		value, candidate, err := prepareNullableQuantity(raw, parse, allowNull)
		if err != nil {
			return nil, err
		}
		if value == nil {
			return json.RawMessage("null"), nil
		}
		return candidate, nil
	}
}
func standardDataEncoding(parse func(json.RawMessage) (ethapi.Data, error), size int) func(json.RawMessage) (json.RawMessage, error) {
	return func(raw json.RawMessage) (json.RawMessage, error) {
		// Data likewise preserves spelling after compatibility. Share the
		// method-root and storage-width checks with the string result path.
		_, candidate, err := prepareStandardData(raw, parse, size)
		return candidate, err
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
