package ethguard

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/codermaybe/ethguard/rpc"
)

// The delivery-isolation baseline: the copying the delivery layer performs to
// keep one operation's facts isolated across owners. The clone row measures
// CallResult.Clone over a fully populated result — decision with reference,
// support and fault tables plus a material snapshot with several retained
// entries; the capture row measures the full publish-and-read chain of one
// ResultCapture, which clones on publish and clones again on every read.
// These measurements run on values built outside the timer, so the cost
// attributed here is the isolation copying itself, never the pipeline that
// formed the result.

// deliveryResultFixture builds one fully populated completion result: a
// corroborated decision carrying its reference evidence, support, faults and
// prepared request, plus a material snapshot with several retained entries.
func deliveryResultFixture() CallResult {
	return CallResult{
		Decision: &rpc.Decision{
			Status: rpc.Corroborated,
			Value:  json.RawMessage(`"0x1"`),
			Reference: &rpc.ReferenceEvidence{
				Hash:         "0x1111111111111111111111111111111111111111111111111111111111111111",
				Height:       16,
				Status:       rpc.Corroborated,
				Participants: []rpc.SourceRef{sourceRefFixture("a"), sourceRefFixture("b")},
				Support:      []rpc.SourceRef{sourceRefFixture("a"), sourceRefFixture("b")},
			},
			Support:  []rpc.SourceRef{sourceRefFixture("a"), sourceRefFixture("b")},
			Prepared: &rpc.PreparedRequest{Method: "eth_getBalance", Params: json.RawMessage(`["0x0000000000000000000000000000000000000dad","latest"]`)},
			Faults:   []error{faultFixture("source timed out")},
		},
		Uncertain: &UncertainResult{
			Raw: []UncertainResponse{
				{Stage: "response", Method: "eth_getBlockByNumber", Source: sourceRefFixture("a"), Value: json.RawMessage(`{"number":"0x10","hash":"0x1111"}`)},
				{Stage: "response", Method: "eth_getBlockByNumber", Source: sourceRefFixture("b"), Value: json.RawMessage(`{"number":"0x10","hash":"0x1111"}`)},
				{Stage: "dependency", Method: "eth_getUncleByBlockHashAndIndex", Source: sourceRefFixture("a"), Value: json.RawMessage(`{"number":"0xf"}`)},
			},
			Fields: []UncertainField{
				{Stage: "response", Method: "eth_getBlockByNumber", Source: sourceRefFixture("a"), Path: "number", Value: json.RawMessage(`"0x10"`)},
				{Stage: "response", Method: "eth_getBlockByNumber", Source: sourceRefFixture("b"), Path: "hash", Value: json.RawMessage(`"0x1111"`)},
			},
			Bytes: 64, Limit: 1 << 20,
		},
	}
}

func sourceRefFixture(name string) rpc.SourceRef {
	return rpc.SourceRef{Name: name, URL: "http://127.0.0.1:1", ID: name}
}

func faultFixture(text string) error {
	return &deliveryFaultFixture{text: text}
}

type deliveryFaultFixture struct{ text string }

func (e *deliveryFaultFixture) Error() string { return e.text }

func BenchmarkDeliveryCallResultClone(b *testing.B) {
	result := deliveryResultFixture()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if clone := result.Clone(); clone.Decision == nil || clone.Uncertain == nil {
			b.Fatal("clone lost a fact")
		}
	}
}

// BenchmarkDeliveryResultCapturePublishReads measures one capture's full
// delivery chain: bind, publish (which clones), and two Result reads (each
// cloning again) — the per-call cost a caller pays for capture isolation.
func BenchmarkDeliveryResultCapturePublishReads(b *testing.B) {
	result := deliveryResultFixture()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, capture := WithResult(context.Background())
		if !capture.bind() {
			b.Fatal("bind refused")
		}
		capture.publish(result)
		first, published, err := capture.Result()
		if err != nil || !published || first.Decision == nil {
			b.Fatalf("first read = %v, %v", published, err)
		}
		second, _, _ := capture.Result()
		if second.Decision == nil {
			b.Fatal("second read lost the decision")
		}
	}
}
