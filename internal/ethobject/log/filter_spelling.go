package log

import (
	"encoding/json"

	"github.com/codermaybe/ethguard/internal/ethcodec"
)

// foldFilterSpelling preserves the facade's case-insensitive hex inputs in
// the frozen JSON snapshot before generated validation selects its branch.
// Extra request members keep their original bytes and are never traversed.
func foldFilterSpelling(raw json.RawMessage) (json.RawMessage, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return nil, err
	}
	for _, name := range []string{"blockHash", "fromBlock", "toBlock"} {
		if value, present := fields[name]; present {
			fields[name] = ethcodec.FoldHexSpelling(value)
		}
	}
	for name, depth := range map[string]int{"address": 1, "topics": 2} {
		if value, present := fields[name]; present {
			folded, err := foldFilterHexArray(value, depth)
			if err != nil {
				return nil, err
			}
			fields[name] = folded
		}
	}
	return ethcodec.MarshalNoEscape(fields)
}

// Arrays are visited only to their declared depth. Objects, null and wrong
// shapes remain intact for the generated filter constraints to reject.
func foldFilterHexArray(raw json.RawMessage, depth int) (json.RawMessage, error) {
	if depth == 0 || ethcodec.IsJSONNull(raw) {
		return ethcodec.FoldHexSpelling(raw), nil
	}
	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		return ethcodec.FoldHexSpelling(raw), nil
	}
	for i := range items {
		folded, err := foldFilterHexArray(items[i], depth-1)
		if err != nil {
			return nil, err
		}
		items[i] = folded
	}
	return ethcodec.MarshalNoEscape(items)
}
