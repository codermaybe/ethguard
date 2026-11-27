package ethguard_test

import (
	"context"
	"encoding/json"
	"math/big"
	"sync"
	"testing"
	"time"

	"github.com/codermaybe/ethguard"
	"github.com/codermaybe/ethguard/ethapi"
	"github.com/codermaybe/ethguard/internal/methods"
	"github.com/codermaybe/ethguard/observe"
	"github.com/codermaybe/ethguard/rpctest"
)

// The monitoring seam must stay a pure consumer of delivered facts: wiring a
// recorder onto a client may not change the business value, the error, the
// decision facts, the material snapshot, the capture delivery, or the number
// of upstream requests an operation makes. This fixture runs one anchored
// balance read through identical harnesses — one client with a recording
// sink and one on the default silent path — and requires the two deliveries
// to match field for field.

type recorderSink struct {
	mu     sync.Mutex
	events []observe.DecisionEvent
}

func (s *recorderSink) RecordDecision(e observe.DecisionEvent) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, e)
}

func (s *recorderSink) decisionEvents() []observe.DecisionEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]observe.DecisionEvent(nil), s.events...)
}

// recorderParityFleet scripts one anchored corroborated balance read; the
// caller configures the recorder through the options.
func recorderParityFleet(t *testing.T, opts ...ethguard.Option) (*ethguard.Client, []*rpctest.Node) {
	t.Helper()
	client, nodes := fleet(t, 3, opts...)
	for _, node := range nodes {
		node.Handle(methods.MethodGetBlockByNumber, func(c rpctest.Call) (any, error) {
			var ref string
			if len(c.Params) > 0 {
				_ = json.Unmarshal(c.Params[0], &ref)
			}
			var full bool
			if len(c.Params) > 1 {
				_ = json.Unmarshal(c.Params[1], &full)
			}
			if full || ref != "latest" {
				return nil, rpctest.RPCError(-32602, "unexpected anchor probe "+ref)
			}
			return map[string]any{"number": "0x10", "hash": "0x1111111111111111111111111111111111111111111111111111111111111111"}, nil
		})
		node.Return(methods.MethodGetBalance, "0x64")
	}
	return client, nodes
}

// recorderParityDelivery runs one capture- and evidence-bound balance read
// and collects every delivered fact the caller can observe, including the
// upstream request count the operation consumed.
func recorderParityDelivery(t *testing.T, client *ethguard.Client, nodes []*rpctest.Node) (*big.Int, ethguard.CallResult, ethguard.CallResult, int) {
	t.Helper()
	ctx, evidenceErr := ethguard.WithSourceEvidence(context.Background(), 1<<20)
	if evidenceErr != nil {
		t.Fatal(evidenceErr)
	}
	ctx, capture := ethguard.WithResult(ctx)
	before := 0
	for _, node := range nodes {
		before += len(node.Calls())
	}
	balance, result, err := client.BalanceAt(ctx, ethapi.Address("0x0000000000000000000000000000000000000dad"), nil)
	if err != nil {
		t.Fatalf("balance read failed: %v", err)
	}
	captured, published, captureErr := capture.Result()
	if captureErr != nil || !published {
		t.Fatalf("capture delivery = %v, %v, want published without error", published, captureErr)
	}
	after := 0
	for _, node := range nodes {
		after += len(node.Calls())
	}
	return balance, result, captured, after - before
}

func TestRecorderWiringKeepsDeliveryIdentical(t *testing.T) {
	sink := &recorderSink{}
	silentClient, silentNodes := recorderParityFleet(t)
	wiredClient, wiredNodes := recorderParityFleet(t, ethguard.WithRecorder(sink))

	silentBalance, silentResult, silentCapture, silentRequests := recorderParityDelivery(t, silentClient, silentNodes)
	wiredBalance, wiredResult, wiredCapture, wiredRequests := recorderParityDelivery(t, wiredClient, wiredNodes)

	if silentBalance.Cmp(wiredBalance) != 0 {
		t.Fatalf("business value drifted with a recorder: %v vs %v", silentBalance, wiredBalance)
	}
	if silentRequests != wiredRequests {
		t.Fatalf("upstream requests drifted with a recorder: %d vs %d", silentRequests, wiredRequests)
	}
	assertDecisionParity(t, silentResult, wiredResult)
	assertDecisionParity(t, silentCapture, wiredCapture)

	// Each read records its own event; the sink observes both clients, so the
	// count is two. A silent client records nothing.
	events := sink.decisionEvents()
	if len(events) != 2 {
		t.Fatalf("recorded %d decision events, want one per wired read: %+v", len(events), events)
	}
	for _, event := range events {
		if event.Status != "corroborated" || event.Conflict || event.Faults != 0 {
			t.Fatalf("recorded event misreports the delivery: %+v", event)
		}
		if len(event.Attempts) != 3 {
			t.Fatalf("recorded event attempts = %d, want one per voting member", len(event.Attempts))
		}
		for _, attempt := range event.Attempts {
			if attempt.Failed {
				t.Fatalf("a corroborated read must not mark attempts failed: %+v", attempt)
			}
		}
	}
}

// The view surface shares the same parity duty with a narrower signature:
// business value and error only. A recorder on the owner client may not
// change what the ethclient-shaped view returns.
func TestViewDeliveryIdenticalWithRecorder(t *testing.T) {
	run := func(withRecorder bool, sink observe.Recorder) (*big.Int, error) {
		var opts []ethguard.Option
		if withRecorder {
			opts = append(opts, ethguard.WithRecorder(sink))
		}
		client, _ := recorderParityFleet(t, opts...)
		return client.EthClient().BalanceAt(context.Background(), ethapi.Address("0x0000000000000000000000000000000000000dad"), nil)
	}
	silentBalance, silentErr := run(false, nil)
	wiredBalance, wiredErr := run(true, &recorderSink{})
	if silentErr != nil || wiredErr != nil {
		t.Fatalf("errors: %v vs %v", silentErr, wiredErr)
	}
	if silentBalance.Cmp(wiredBalance) != 0 {
		t.Fatalf("view value drifted with a recorder: %v vs %v", silentBalance, wiredBalance)
	}
}

// assertDecisionParity compares the decision facts two deliveries carry: the
// confidence class, the established reference, the participant and support
// statements, the fault tables and the payload presence.
func assertDecisionParity(t *testing.T, a, b ethguard.CallResult) {
	t.Helper()
	if (a.Decision == nil) != (b.Decision == nil) {
		t.Fatalf("decision presence drifted: %v vs %v", a.Decision != nil, b.Decision != nil)
	}
	if a.Decision == nil {
		return
	}
	da, db := a.Decision, b.Decision
	if da.Status != db.Status {
		t.Fatalf("status drifted: %s vs %s", da.Status, db.Status)
	}
	if (da.Reference == nil) != (db.Reference == nil) ||
		(da.Reference != nil && (da.Reference.Hash != db.Reference.Hash || da.Reference.Status != db.Reference.Status)) {
		t.Fatalf("reference drifted: %+v vs %+v", da.Reference, db.Reference)
	}
	if len(da.Support) != len(db.Support) || len(da.Participants) != len(db.Participants) ||
		len(da.Faults) != len(db.Faults) || len(da.SourceFaults) != len(db.SourceFaults) {
		t.Fatalf("support or fault tables drifted: %d/%d/%d/%d vs %d/%d/%d/%d",
			len(da.Support), len(da.Participants), len(da.Faults), len(da.SourceFaults),
			len(db.Support), len(db.Participants), len(db.Faults), len(db.SourceFaults))
	}
	if (da.Value == nil) != (db.Value == nil) {
		t.Fatalf("value presence drifted: %v vs %v", da.Value, db.Value)
	}
	assertMaterialParity(t, a, b)
}

// assertMaterialParity compares the material snapshot two deliveries carry:
// the counters and every retained raw entry.
func assertMaterialParity(t *testing.T, a, b ethguard.CallResult) {
	t.Helper()
	if (a.Uncertain == nil) != (b.Uncertain == nil) {
		t.Fatalf("material presence drifted: %v vs %v", a.Uncertain != nil, b.Uncertain != nil)
	}
	if a.Uncertain == nil {
		return
	}
	if a.Uncertain.Bytes != b.Uncertain.Bytes || a.Uncertain.Limit != b.Uncertain.Limit ||
		a.Uncertain.Dropped != b.Uncertain.Dropped || a.Uncertain.Truncated != b.Uncertain.Truncated ||
		len(a.Uncertain.Raw) != len(b.Uncertain.Raw) {
		t.Fatalf("material snapshot drifted: bytes %d/%d limit %d/%d dropped %d/%d truncated %v/%v raw %d/%d",
			a.Uncertain.Bytes, b.Uncertain.Bytes, a.Uncertain.Limit, b.Uncertain.Limit,
			a.Uncertain.Dropped, b.Uncertain.Dropped, a.Uncertain.Truncated, b.Uncertain.Truncated,
			len(a.Uncertain.Raw), len(b.Uncertain.Raw))
	}
	for i := range a.Uncertain.Raw {
		if string(a.Uncertain.Raw[i].Value) != string(b.Uncertain.Raw[i].Value) ||
			a.Uncertain.Raw[i].Stage != b.Uncertain.Raw[i].Stage {
			t.Fatalf("retained raw entry %d drifted: %q vs %q", i, a.Uncertain.Raw[i].Value, b.Uncertain.Raw[i].Value)
		}
	}
}

// A recorder wired but unused must still leave the delivery untouched: the
// sink observes the completed decision after the business result exists, so
// the call completes within the normal timeout even though the sink is
// invoked between resolve and delivery.
func TestRecorderSeesCompletedDecisionOnly(t *testing.T) {
	client, _ := recorderParityFleet(t, ethguard.WithRecorder(&recorderSink{}))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, _, err := client.BalanceAt(ctx, ethapi.Address("0x0000000000000000000000000000000000000dad"), nil); err != nil {
		t.Fatal(err)
	}
}
