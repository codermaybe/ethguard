package methods

import (
	"encoding/json"
	"strings"

	"ethguard/rpc"
)

// MethodGetBlockByHash is the JSON-RPC method name resolved by
// BlockByHashPolicy.
const MethodGetBlockByHash = "eth_getBlockByHash"

// BlockByHashPolicy verifies eth_getBlockByHash by full agreement.
func BlockByHashPolicy() rpc.Policy { return rpc.Strict(normalizeBlockByHash) }

// normalizeBlockByHash compares block objects after canonicalizing hexadecimal
// casing. JSON object key order does not cause false conflicts.
func normalizeBlockByHash(value json.RawMessage) (string, error) {
	var decoded any
	if err := json.Unmarshal(value, &decoded); err != nil {
		return "", err
	}
	canonicalizeHex(&decoded)
	encoded, err := json.Marshal(decoded)
	return string(encoded), err
}

func canonicalizeHex(value *any) {
	switch item := (*value).(type) {
	case map[string]any:
		for key, child := range item {
			canonicalizeHex(&child)
			item[key] = child
		}
	case []any:
		for i := range item {
			canonicalizeHex(&item[i])
		}
	case string:
		if strings.HasPrefix(item, "0x") {
			*value = strings.ToLower(item)
		}
	}
}
