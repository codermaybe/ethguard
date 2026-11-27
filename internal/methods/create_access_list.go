package methods

import (
	"encoding/json"

	"github.com/codermaybe/ethguard/internal/rpcengine"

	accesslist "github.com/codermaybe/ethguard/internal/ethobject/accesslist"
	"github.com/codermaybe/ethguard/internal/ethobject/wiremask"
)

// MethodCreateAccessList is the JSON-RPC method spelling this file owns: the
// access-list construction read. It has no ethclient-shaped facade method;
// callers reach it through the raw Call surface.
const MethodCreateAccessList = "eth_createAccessList"

// createAccessListSpec is the method's compiled contract: canonical
// access-list comparison over the strict resolver with a moving reference
// riding the block parameter. The error field keeps its decoded text
// spelling; no whole-key compatibility correction applies to this method.
func createAccessListSpec() MethodSpec {
	return MethodSpec{
		Method:      MethodCreateAccessList,
		Resolver:    ResolverStrict,
		Normalize:   normalizeAccessList,
		Encode:      accesslist.Encode,
		KeySource:   rpcengine.RuleKeyCanonicalBytes,
		CompareKind: rpcengine.RuleCompareMethodSpecific,
		Reference:   ReferenceSpec{Kind: ReferenceShapeHashAt, Positions: []int{1}, EIP1898: true},
		Pending:     PendingSpec{Mode: PendingObserve, Pos: 1},
	}
}

func createAccessListDescriptor() MethodDescriptor {
	return MethodDescriptor{
		Name:      MethodCreateAccessList,
		Kind:      RegisteredMethod,
		Family:    FamilyRead,
		Coverage:  CoverageRegistered,
		Policy:    "CompilePlan",
		Alignment: "v1.17.10",
	}
}

// normalizeAccessList reduces one access-list answer to its pruned comparison
// key: hexadecimal casing or JSON key ordering differences do not cause false
// conflicts, and vendor extension fields do not either.
func normalizeAccessList(value json.RawMessage) (string, error) {
	key, err := wiremask.PruneMethod(MethodCreateAccessList, value)
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
