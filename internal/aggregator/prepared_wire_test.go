package aggregator

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/codermaybe/ethguard/rpc"
)

// The prepared-request record and the business fan-out must carry the same
// parameter bytes: the snapshot on the decision equals what the node
// actually received on the wire, rewrite included.
func TestPreparedRequestMatchesTheSentWireFact(t *testing.T) {
	nodes, p := probeNodes(t, "0x64", 2)
	stage := &scriptedStage{ref: rpc.ReferenceEvidence{Hash: "0xabc", Height: 16, Status: rpc.Corroborated}}
	decision, err := probeAggregator(t, p, stage).Aggregate(context.Background(),
		rpc.Request{Method: probeMethod, Params: []any{"0x1", "latest"}})
	if err != nil {
		t.Fatalf("planned read failed: %v", err)
	}
	if decision.Prepared == nil || decision.Prepared.Params == nil {
		t.Fatalf("prepared request was not recorded: %+v", decision.Prepared)
	}
	var wire []string
	for _, call := range nodes[0].Calls() {
		if call.Method != probeMethod {
			continue
		}
		for _, raw := range call.Params {
			wire = append(wire, string(raw))
		}
		break
	}
	if len(wire) == 0 {
		t.Fatal("the probe read never reached a node")
	}
	sent := "[" + strings.Join(wire, ",") + "]"
	if sent != string(decision.Prepared.Params) {
		t.Fatalf("wire params = %s, prepared record = %s: the two facts diverged", sent, decision.Prepared.Params)
	}
	// The rewrite is in both: the moving tag became the established hash.
	if !strings.Contains(sent, "0xabc") || strings.Contains(sent, "latest") {
		t.Fatalf("wire params %s did not carry the rewrite", sent)
	}
}

// A parameter whose marshaler would produce different bytes per call must
// appear identically in the record and on the wire.
func TestPreparedRequestFreezesCustomMarshalers(t *testing.T) {
	nodes, p := probeNodes(t, "0x64", 2)
	stage := &scriptedStage{ref: rpc.ReferenceEvidence{Hash: "0xabc", Height: 16, Status: rpc.Corroborated}}
	wobbly := &wobblyMarshaler{}
	decision, err := probeAggregator(t, p, stage).Aggregate(context.Background(),
		rpc.Request{Method: probeMethod, Params: []any{"0x1", wobbly}})
	if err != nil {
		t.Fatalf("planned read failed: %v", err)
	}
	if wobbly.calls != 1 {
		t.Fatalf("marshaler ran %d times, want exactly one", wobbly.calls)
	}
	for _, call := range nodes[0].Calls() {
		if call.Method != probeMethod {
			continue
		}
		if len(call.Params) != 2 || string(call.Params[1]) != wobbly.frozen {
			t.Fatalf("wire param = %s, frozen = %s", call.Params[1], wobbly.frozen)
		}
	}
	var recorded []any
	if err := json.Unmarshal(decision.Prepared.Params, &recorded); err != nil {
		t.Fatalf("prepared params are not JSON: %v", err)
	}
	// The record is decoded JSON; the wire carries the frozen bytes. Both
	// spell the one frozen serialization "a".
	if len(recorded) != 2 || recorded[1] != "a" {
		t.Fatalf("recorded param = %#v, want the frozen value", recorded[1])
	}
}

// wobblyMarshaler is a parameter that would change its bytes if it were
// serialized twice.
type wobblyMarshaler struct {
	calls  int
	frozen string
}

func (w *wobblyMarshaler) MarshalJSON() ([]byte, error) {
	w.calls++
	w.frozen = `"` + string(rune('a'+w.calls-1)) + `"`
	return []byte(w.frozen), nil
}
