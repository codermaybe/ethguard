package ethcodec

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// CanonicalQuantity checks a wire QUANTITY and returns its canonical string
// spelling: 0x plus lowercase hex, no leading zeros, zero as 0x0. Object
// encoders embed these strings straight into their canonical structs. It is
// the string form of EncodeQuantity's JSON bytes.
func CanonicalQuantity(value json.RawMessage) (string, error) {
	text, err := decodedScalarString(value, "hex quantity")
	if err != nil {
		return "", err
	}
	text = foldWireHexText(text)
	if !IsCanonicalQuantity(text) {
		return "", fmt.Errorf("hex quantity: invalid canonical quantity %q", text)
	}
	return text, nil
}

// MarshalNoEscape encodes one canonical object: compact JSON, HTML escaping
// off, and the trailing newline dropped. Every object encoder's marshal step
// is this function.
func MarshalNoEscape(value any) (json.RawMessage, error) {
	if text, ok := value.(string); ok && len(text) <= 128 {
		// Generated scalar marshalers arrive here as strings. Short plain
		// ASCII needs only quotes. Bound the probe so a long string needing
		// escaping near its end is not scanned twice. The standard encoder
		// remains authoritative for long strings, escaping and invalid UTF-8.
		plain := true
		for i := 0; i < len(text); i++ {
			c := text[i]
			if c < 0x20 || c >= 0x80 || c == '"' || c == '\\' {
				plain = false
				break
			}
		}
		if plain {
			out := make([]byte, len(text)+2)
			out[0], out[len(out)-1] = '"', '"'
			copy(out[1:], text)
			return out, nil
		}
	}
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

// IsJSONNull reports whether value is the protocol's null absence. Only JSON
// whitespace may surround the token; other whitespace is malformed JSON.
func IsJSONNull(value json.RawMessage) bool {
	return bytes.Equal(bytes.Trim(value, " \t\r\n"), []byte("null"))
}

// PresentNonNull reports whether value carries a non-null JSON value. Absent
// (empty or blank) and null both count as absent, so it is the negation of
// IsJSONNull over present values.
func PresentNonNull(value json.RawMessage) bool {
	trimmed := strings.TrimSpace(string(value))
	return len(trimmed) > 0 && trimmed != "null"
}
