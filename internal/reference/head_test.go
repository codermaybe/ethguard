package reference

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/codermaybe/ethguard/observe"
	"github.com/codermaybe/ethguard/rpc"
)

type fakeHeadReader struct {
	observations  []HeadObservation
	confirmations []HeadObservation
}

func (f fakeHeadReader) Read(context.Context, string, time.Duration) ([]HeadObservation, error) {
	return f.observations, nil
}

func (f fakeHeadReader) Confirm(context.Context, uint64, []string, time.Duration) ([]HeadObservation, error) {
	return f.confirmations, nil
}

func TestFinalizedConsumesHeadReaderSeam(t *testing.T) {
	raw := json.RawMessage(`{"number":"0x10","hash":"0xabc"}`)
	reader := fakeHeadReader{observations: []HeadObservation{
		{Source: rpc.SourceRef{Name: "a", ID: "a"}, Value: raw, Attempted: true},
		{Source: rpc.SourceRef{Name: "b", ID: "b"}, Value: raw, Attempted: true},
	}}
	ref, err := Finalized(context.Background(), reader, 32, 2, rpc.CorroborationMajority, time.Second, observe.Nop)
	if err != nil {
		t.Fatal(err)
	}
	if ref.Status != rpc.Corroborated || ref.Hash != "0xabc" || ref.Height != 16 {
		t.Fatalf("reference = %+v", ref)
	}
}
