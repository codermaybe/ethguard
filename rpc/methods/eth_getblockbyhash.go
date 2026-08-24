package methods

import (
	"context"
	"encoding/json"
	"strings"

	"ethguard/rpc"
)

// BlockByHashPolicy compares block objects after canonicalizing hexadecimal
// casing. JSON object key order does not cause false conflicts.
type BlockByHashPolicy struct{}

func (BlockByHashPolicy) Prepare(_ context.Context, req rpc.Request) (rpc.Request, error) {
	return req, nil
}

func (BlockByHashPolicy) Normalize(value json.RawMessage) (string, error) {
	var decoded any
	if err := json.Unmarshal(value, &decoded); err != nil {
		return "", err
	}
	canonicalizeHex(&decoded)
	encoded, err := json.Marshal(decoded)
	return string(encoded), err
}

func (p BlockByHashPolicy) Resolve(obs []rpc.Observation) (rpc.Decision, error) {
	return rpc.ResolveStrict(p, obs)
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
