package rpc

import (
	"encoding/json"
	"errors"
	"testing"
)

// The frozen parameters must be the one serialization the wire sends: a
// custom MarshalJSON runs at freeze time, never again at send time, and the
// joined snapshot equals what EncodeRequest puts on the wire.
type nondeterministicParam struct{ calls int }

func (p *nondeterministicParam) MarshalJSON() ([]byte, error) {
	p.calls++
	if p.calls > 1 {
		return []byte(`"second-call"`), nil
	}
	return []byte(`"first-call"`), nil
}

func TestFreezeParamsExecutesCustomMarshalerOnce(t *testing.T) {
	param := &nondeterministicParam{}
	frozen, snapshot, err := FreezeParams([]any{param, "0x1"})
	if err != nil {
		t.Fatalf("freeze: %v", err)
	}
	if param.calls != 1 {
		t.Fatalf("marshaler ran %d times, want exactly one", param.calls)
	}
	if string(snapshot) != `["first-call","0x1"]` {
		t.Fatalf("snapshot = %s, want the frozen serialization", snapshot)
	}
	// The wire path marshals the frozen elements as raw JSON, so it
	// reproduces the snapshot bytes without running the marshaler again.
	if param.calls != 1 {
		t.Fatalf("marshaler ran again before send")
	}
	wire, err := json.Marshal(frozen)
	if err != nil {
		t.Fatalf("marshal frozen params: %v", err)
	}
	if param.calls != 1 {
		t.Fatalf("marshaler ran again on the wire path: %d executions", param.calls)
	}
	if string(wire) != string(snapshot) {
		t.Fatalf("wire params = %s, snapshot = %s: the recorded fact and the sent fact diverged", wire, snapshot)
	}
}

func TestFreezeParamsShapeContract(t *testing.T) {
	frozen, snapshot, err := FreezeParams(nil)
	if err != nil || frozen != nil || snapshot != nil {
		t.Fatalf("nil params must stay nil in both results: %v %v %v", frozen, snapshot, err)
	}
	frozen, snapshot, err = FreezeParams([]any{})
	if err != nil || frozen == nil || len(frozen) != 0 {
		t.Fatalf("empty params stay an empty list: %v %v", frozen, err)
	}
	if string(snapshot) != "[]" {
		t.Fatalf("empty snapshot = %s, want []", snapshot)
	}
	frozen, snapshot, err = FreezeParams([]any{"0x1", map[string]any{"blockNumber": "latest"}, []any{float64(1), float64(2)}})
	if err != nil {
		t.Fatalf("freeze: %v", err)
	}
	if string(snapshot) != `["0x1",{"blockNumber":"latest"},[1,2]]` {
		t.Fatalf("snapshot = %s", snapshot)
	}
	if len(frozen) != 3 {
		t.Fatalf("frozen list length = %d, want 3", len(frozen))
	}
	for i, element := range frozen {
		raw, ok := element.(json.RawMessage)
		if !ok {
			t.Fatalf("frozen element %d is %T, want json.RawMessage", i, element)
		}
		if len(raw) == 0 {
			t.Fatalf("frozen element %d is empty", i)
		}
	}
}

func TestFreezeParamsReportsEncodeFailureAsArgumentError(t *testing.T) {
	_, _, err := FreezeParams([]any{"ok", make(chan int)})
	if err == nil {
		t.Fatal("unmarshalable parameter must fail the freeze")
	}
	if !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("freeze error = %v, want an ErrInvalidRequest argument error", err)
	}
}

func TestPreparedRequestCloneOwnsItsBytes(t *testing.T) {
	original := &PreparedRequest{Method: "eth_getBalance", Params: json.RawMessage(`["0x1"]`)}
	clone := original.Clone()
	clone.Params[2] = '2'
	clone.Method = "mutated"
	if string(original.Params) != `["0x1"]` || original.Method != "eth_getBalance" {
		t.Fatalf("prepared request leaked through the clone: %+v", original)
	}
	if (*PreparedRequest)(nil).Clone() != nil {
		t.Fatal("nil clone must stay nil")
	}
}
