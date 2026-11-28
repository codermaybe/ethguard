package ethguard_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/codermaybe/ethguard"
	"github.com/codermaybe/ethguard/ethapi"
	"github.com/codermaybe/ethguard/internal/methods"
	"github.com/codermaybe/ethguard/rpc"
)

// Production-path health witnesses for the two transaction RPCs: the error
// exits — one faulting source beside two agreeing valid ones, an all-down
// fleet, and cancellation — with the sentinel classes and per-source
// attribution the processing refactor must reproduce. These run the full
// assembler path (facade → runtime → aggregator → pool → typed), not the
// local hook benchmark.
//
// Boundary fact recorded for the refactor (T02 baseline): rpc.ErrNoValid-
// Response is unreachable through the planned production path for these two
// methods. Admission already runs the exact preparation the comparison key
// needs, so any response that reaches clustering re-prepares successfully;
// the only per-source skips left are response-cause (transport, admission,
// malformed). Every complete raw-hook failure therefore happens in the
// admission phase and surfaces through the malformed-admission early exit
// (rpc.ErrMalformedResponse), which TestTransactionCompatibilityRejects-
// IdenticalInvalidSources pins. A processing refactor that moved admission
// work elsewhere without keeping this coverage would change which sentinel
// the caller sees.

// transactionHealthFixture returns one valid mined transaction fixture pair.
// Both sources answer with byte-identical payloads, so any vote difference
// below comes only from the injected fault.
func transactionHealthFixture(t *testing.T) (json.RawMessage, ethapi.Hash) {
	t.Helper()
	raw, signed := transactionCompatibilityFixture(t)
	return raw, ethapi.Hash(signed.Hash().Hex())
}

func transactionHealthRead(ctx context.Context, client *ethguard.Client, method string, hash ethapi.Hash) (ethguard.CallResult, error) {
	if method == methods.MethodGetTransactionByHash {
		_, _, result, err := client.TransactionByHash(ctx, hash)
		return result, err
	}
	_, result, err := client.TransactionInBlock(ctx, ethapi.Hash(transactionCompatibilityBlock), 0)
	return result, err
}

// TestTransactionOneFaultingSourceKeepsDynamicQuorum pins the one-error-plus-
// two-valid shape: the faulting source keeps its slot and its attribution,
// the two valid sources still corroborate against the fixed basis, the raw
// faulting evidence stays distinguishable, and the ancillary fact tables are
// not emptied by the fault.
func TestTransactionOneFaultingSourceKeepsDynamicQuorum(t *testing.T) {
	raw, hash := transactionHealthFixture(t)
	for _, method := range transactionCompatibilityMethods {
		t.Run(method, func(t *testing.T) {
			client, nodes := fleet(t, 3)
			nodes[0].Fail(method, -32000, "boom")
			nodes[1].ReturnRaw(method, raw)
			nodes[2].ReturnRaw(method, raw)
			base, err := ethguard.WithSourceEvidence(context.Background(), 1<<20)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(base, 5*time.Second)
			defer cancel()
			result, err := transactionHealthRead(ctx, client, method, hash)
			if err != nil || !result.IsCorroborated() {
				t.Fatalf("one faulting source broke the read: %+v, %v", result, err)
			}
			d := result.Decision
			if len(d.Participants) != 3 || len(d.Support) != 2 || len(d.Reject) != 0 {
				t.Fatalf("dynamic quorum facts: participants=%d support=%d reject=%d",
					len(d.Participants), len(d.Support), len(d.Reject))
			}
			if len(d.SourceFaults) != 1 || d.SourceFaults[0].Source.Name != "node-a" || d.SourceFaults[0].Err == nil {
				t.Fatalf("fault not attributed to the faulting source: %+v", d.SourceFaults)
			}
			if len(d.Faults) != 1 || !strings.Contains(d.Faults[0].Error(), "node-a") {
				t.Fatalf("human-readable fault lost the attribution: %v", d.Faults)
			}
			if d.CollectionFaults != nil {
				t.Fatalf("protocol error misrecorded as a collection fault: %v", d.CollectionFaults)
			}
			// The faulting source's vote is neither support nor dissent: its
			// verdict is unusable, and the two valid sources keep theirs.
			supported := map[string]bool{}
			for _, source := range d.Support {
				supported[source.Name] = true
			}
			if !supported["node-b"] || !supported["node-c"] {
				t.Fatalf("valid sources lost their support slots: %v", d.Support)
			}
			if result.Uncertain == nil || len(result.Uncertain.Raw) != 3 {
				t.Fatalf("raw evidence table lost a source: %+v", result.Uncertain)
			}
			for _, source := range result.Uncertain.Raw {
				if source.Source.Name == "node-a" && source.Value != nil {
					t.Fatalf("faulting source contributed raw material: %+v", source)
				}
				if source.Source.Name != "node-a" && source.Value == nil {
					t.Fatalf("valid source lost its raw evidence: %+v", source)
				}
			}
		})
	}
}

// TestTransactionDissentingValidSourceIsNotAFault pins the dissent boundary:
// a valid answer that loses the comparison is a Reject with its raw evidence
// kept, not a source fault, and the majority still corroborates.
func TestTransactionDissentingValidSourceIsNotAFault(t *testing.T) {
	raw, hash := transactionHealthFixture(t)
	other := transactionCompatibilityEdit(t, raw, map[string]json.RawMessage{
		"blockTimestamp": json.RawMessage(`"0x6"`),
	})
	if string(raw) == string(other) {
		t.Fatal("fixture helper returned identical payloads")
	}
	client, nodes := fleet(t, 3)
	nodes[0].ReturnRaw(methods.MethodGetTransactionByHash, raw)
	nodes[1].ReturnRaw(methods.MethodGetTransactionByHash, raw)
	nodes[2].ReturnRaw(methods.MethodGetTransactionByHash, other)
	base, err := ethguard.WithSourceEvidence(context.Background(), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(base, 5*time.Second)
	defer cancel()
	_, _, result, err := client.TransactionByHash(ctx, hash)
	if err != nil || !result.IsCorroborated() {
		t.Fatalf("majority read failed: %+v, %v", result, err)
	}
	d := result.Decision
	if len(d.Support) != 2 || len(d.Reject) != 1 || len(d.SourceFaults) != 0 || len(d.Faults) != 0 {
		t.Fatalf("valid dissent changed the fault tables: %+v", d)
	}
	if result.Uncertain == nil || len(result.Uncertain.Raw) != 3 {
		t.Fatalf("raw evidence table lost the dissenting source: %+v", result.Uncertain)
	}
}

// TestTransactionAllSourcesDownRecordsCollectionFault pins the transport
// exit: an unreachable fleet fails the call with the pool sentinel, the
// decision carries the collection fault once, and no source is blamed for a
// response it never delivered.
func TestTransactionAllSourcesDownRecordsCollectionFault(t *testing.T) {
	_, hash := transactionHealthFixture(t)
	client, nodes := fleet(t, 2)
	for _, node := range nodes {
		node.Close()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, _, result, err := client.TransactionByHash(ctx, hash)
	if !errors.Is(err, ethguard.ErrAllSourcesDown) || result.Decision == nil {
		t.Fatalf("all-down fleet = %+v, %v", result, err)
	}
	d := result.Decision
	if d.Status != rpc.Unknown || len(d.Value) != 0 {
		t.Fatalf("all-down fleet produced a value: %+v", d)
	}
	if len(d.CollectionFaults) != 1 {
		t.Fatalf("pool-level collection fault: %v", d.CollectionFaults)
	}
	if len(d.SourceFaults) != 2 {
		t.Fatalf("per-source transport attribution: %v", d.SourceFaults)
	}
	for _, fault := range d.SourceFaults {
		if fault.Err == nil || fault.Source.Name == "" {
			t.Fatalf("transport fault without source attribution: %+v", fault)
		}
	}
}

// TestTransactionCallerCancellationFailsTheRead pins the cancellation exit:
// a caller whose deadline expires mid-flight gets a canceled call, and the
// decision keeps no fabricated value.
func TestTransactionCallerCancellationFailsTheRead(t *testing.T) {
	raw, hash := transactionHealthFixture(t)
	client, nodes := fleet(t, 2)
	for _, node := range nodes {
		node.Hang(methods.MethodGetTransactionByHash, 2*time.Second)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	_, _, result, err := client.TransactionByHash(ctx, hash)
	_ = raw
	if err == nil || result.Decision == nil {
		t.Fatalf("canceled read succeeded: %+v, %v", result, err)
	}
	if result.Decision.Value != nil || result.Decision.Status == rpc.Corroborated {
		t.Fatalf("canceled read fabricated a value: %+v", result.Decision)
	}
}
