package wire

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/big"
	"strings"
)

// CanonicalQuantity checks a wire QUANTITY and returns its canonical string
// spelling: 0x plus lowercase hex, no leading zeros, zero as 0x0. Object
// encoders embed these strings straight into their canonical structs. It is
// the string form of EncodeQuantity's JSON bytes.
func CanonicalQuantity(value json.RawMessage) (string, error) {
	decimal, err := NormalizeWireQuantity(value)
	if err != nil {
		return "", err
	}
	number, ok := new(big.Int).SetString(decimal, 10)
	if !ok {
		return "", fmt.Errorf("invalid quantity %q", decimal)
	}
	return "0x" + number.Text(16), nil
}

// MarshalNoEscape encodes one canonical object: compact JSON, HTML escaping
// off, and the trailing newline dropped. Every object encoder's marshal step
// is this function.
func MarshalNoEscape(value any) (json.RawMessage, error) {
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

// IsJSONNull reports whether value is the protocol's null absence. Whitespace
// around the token is not a different answer: " null " is null.
func IsJSONNull(value json.RawMessage) bool {
	return strings.TrimSpace(string(value)) == "null"
}

// PresentNonNull reports whether value carries a non-null JSON value. Absent
// (empty or blank) and null both count as absent, so it is the negation of
// IsJSONNull over present values.
func PresentNonNull(value json.RawMessage) bool {
	trimmed := strings.TrimSpace(string(value))
	return len(trimmed) > 0 && trimmed != "null"
}
