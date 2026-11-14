package methods

import (
	"encoding/json"
	"testing"
)

// TestSyncingDefinition pins the method's self-contained definition: the
// file owns the RPC spelling, the spec and the descriptor agree on it, the
// admission hook is bound where the method owns one, and the spec compiles
// into the plan the registry installs.
func TestSyncingDefinition(t *testing.T) {
	spec := syncingSpec()
	descriptor := syncingDescriptor()
	if spec.Method != MethodSyncing || descriptor.Name != spec.Method {
		t.Fatalf("method identity is not self-contained: spec=%+v descriptor=%+v", spec, descriptor)
	}
	if spec.Normalize == nil {
		t.Fatalf("method definition is incomplete: %+v", spec)
	}
	if descriptor.Adapter != "SyncProgress" {
		t.Fatalf("adapter mapping drifted: %+v", descriptor)
	}
	if _, err := CompilePlan(spec); err != nil {
		t.Fatal(err)
	}
}

func TestSyncingRejectsMalformedOfficialFieldsBeforeSourceSelection(t *testing.T) {
	for _, raw := range []string{`{"currentBlock":null}`, `{"highestBlock":3}`, `{"startingBlock":"negative"}`} {
		if _, err := normalizeSyncing(json.RawMessage(raw)); err == nil {
			t.Fatalf("invalid progress entered local selection: %s", raw)
		}
	}
	const raw = `{"currentBlock":"0x1","txIndexRemainingBlocks":"0x10"}`
	if _, err := normalizeSyncing(json.RawMessage(raw)); err != nil {
		t.Fatalf("valid official progress plus source evidence rejected: %v", err)
	}
	got, err := encodeSyncing(json.RawMessage(raw))
	if err != nil || string(got) != `{"currentBlock":"0x1"}` {
		t.Fatalf("official progress = %s, %v", got, err)
	}
}
