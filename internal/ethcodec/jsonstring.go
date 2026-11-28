package ethcodec

import "encoding/json"

// DecodeJSONString preserves json.Unmarshal's string semantics. A complete,
// unescaped ASCII string takes a copying fast path; all other input keeps the
// standard decoder's validation, Unicode replacement and error details.
func DecodeJSONString(raw json.RawMessage) (string, error) {
	plain := len(raw) >= 2 && raw[0] == '"' && raw[len(raw)-1] == '"'
	if plain {
		for _, c := range raw[1 : len(raw)-1] {
			if c < 0x20 || c >= 0x7f || c == '"' || c == '\\' {
				plain = false
				break
			}
		}
	}
	if plain {
		// This conversion owns its bytes; callers may reuse or modify raw.
		return string(raw[1 : len(raw)-1]), nil
	}
	var text string
	err := json.Unmarshal(raw, &text)
	return text, err
}
