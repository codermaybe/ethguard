package methods

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/codermaybe/ethguard/internal/ethcodec"
	"github.com/codermaybe/ethguard/internal/rpcengine"

	syncingobject "github.com/codermaybe/ethguard/internal/ethobject/syncing"
	"github.com/codermaybe/ethguard/rpc"
)

// MethodSyncing is the JSON-RPC method spelling this file owns: the node
// sync-state read behind SyncProgress.
const MethodSyncing = "eth_syncing"

// syncingSpec is the method's compiled contract: canonical JSON comparison
// over the local resolver; the value is node-local, so sources are sampled
// without clustering. No whole-key compatibility correction applies to this
// method.
func syncingSpec() MethodSpec {
	return MethodSpec{
		Method:                 MethodSyncing,
		Resolver:               ResolverLocal,
		Normalize:              normalizeSyncing,
		Encode:                 encodeSyncing,
		ObservationProjectorID: ProjectorSyncing,
		CompareKind:            rpcengine.RuleCompareObject,
	}
}

func syncingDescriptor() MethodDescriptor {
	return MethodDescriptor{
		Name:      MethodSyncing,
		Kind:      RegisteredMethod,
		Family:    FamilyRead,
		Coverage:  CoverageRegistered,
		Policy:    "CompilePlan",
		Adapter:   "SyncProgress",
		Alignment: "v1.17.10",
	}
}

// normalizeSyncing admits the two wire shapes the method defines: false for a
// fully synced node, or a progress object. JSON null, booleans other than
// false, and scalars are malformed answers, not local truth.
func normalizeSyncing(value json.RawMessage) (string, error) {
	trimmed := bytes.TrimSpace(value)
	if bytes.Equal(trimmed, []byte("false")) {
		return "false", nil
	}
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return "", fmt.Errorf("syncing: expected false or an object")
	}
	if _, err := syncingobject.Decode(trimmed); err != nil {
		return "", fmt.Errorf("syncing: invalid official progress fields: %w", err)
	}
	return rpc.CanonicalJSON(value)
}

// encodeSyncing keeps only fields declared by the official result schema.
// Original source observations remain available for extension/diagnostic and
// explicitly budgeted raw-evidence delivery.
func encodeSyncing(raw json.RawMessage) (json.RawMessage, error) {
	value, err := syncingobject.Decode(raw)
	if err != nil {
		return nil, err
	}
	return ethcodec.MarshalNoEscape(value)
}
