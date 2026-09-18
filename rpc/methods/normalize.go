package methods

import (
	"encoding/json"

	"ethguard/rpc/internal/wire"
)

func normalizeWireQuantity(value json.RawMessage) (string, error) {
	return wire.NormalizeWireQuantity(value)
}
func normalizeWireData(value json.RawMessage) (string, error) {
	return wire.NormalizeWireData(value)
}
func validateWireFixedHex(value json.RawMessage, digits int, kind string) (string, error) {
	return wire.ValidateWireFixedHex(value, digits, kind)
}
func normalizeHexQuantity(value json.RawMessage) (string, error) {
	return wire.NormalizeHexQuantity(value)
}
func normalizeHexData(value json.RawMessage) (string, error) {
	return wire.NormalizeHexData(value)
}
func normalizeStorageWord(value json.RawMessage) (string, error) {
	return wire.NormalizeStorageWord(value)
}
func normalizeDecimalString(value json.RawMessage) (string, error) {
	return wire.NormalizeDecimalString(value)
}
func canonicalizeHex(value *any) {
	wire.CanonicalizeHex(value)
}
