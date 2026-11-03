package methods

import (
	"encoding/json"

	"github.com/codermaybe/ethguard/rpc/internal/wire"
)

// normalizeAccessList reduces one access-list answer to its pruned comparison
// key: hexadecimal casing or JSON key ordering differences do not cause false
// conflicts, and vendor extension fields do not either.
func normalizeAccessList(value json.RawMessage) (string, error) {
	key, err := wire.PruneMethod(MethodCreateAccessList, value)
	if err != nil {
		return "", err
	}
	// The mask folds hex values, but error is text: preserve its decoded
	// spelling even when it starts with 0x. JSON escape spelling is immaterial.
	var raw struct {
		Error json.RawMessage `json:"error"`
	}
	if err := json.Unmarshal(value, &raw); err != nil {
		return "", err
	}
	if len(raw.Error) == 0 {
		return key, nil
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(key), &fields); err != nil {
		return "", err
	}
	if _, retained := fields["error"]; !retained {
		return key, nil
	}
	var text *string
	if err := json.Unmarshal(raw.Error, &text); err != nil {
		return "", err
	}
	if text == nil {
		return key, nil
	}
	fields["error"], err = json.Marshal(text)
	if err != nil {
		return "", err
	}
	encoded, err := json.Marshal(fields)
	return string(encoded), err
}
