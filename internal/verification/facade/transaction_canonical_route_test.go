package ethguard_test

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/codermaybe/ethguard"
	"github.com/codermaybe/ethguard/ethapi"
	"github.com/codermaybe/ethguard/rpc/methods"
)

// Pilot-differential witnesses on the production path after the canonical
// route went live: the missing-timestamp derivation keeps its original
// evidence, key order and source order cannot change the committed value,
// and TransactionCount stays outside the processing pilot.

// TestTransactionCanonicalRouteKeyOrderInvariant sends the same transaction
// with two different JSON key orders from two sources, in both upstream
// orders, and requires the same corroborated committed value. The response
// is the raw material of the affiliation votes; the key order is provider
// formatting, never a vote.
func TestTransactionCanonicalRouteKeyOrderInvariant(t *testing.T) {
	raw, signed := transactionCompatibilityFixture(t)
	reordered := transactionCompatibilityReordered(t, raw)
	if string(reordered) == string(raw) {
		t.Fatal("reordering helper produced identical bytes")
	}
	run := func(first, second json.RawMessage) json.RawMessage {
		client, nodes := fleet(t, 2)
		nodes[0].ReturnRaw(methods.MethodGetTransactionByHash, first)
		nodes[1].ReturnRaw(methods.MethodGetTransactionByHash, second)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _, result, err := client.TransactionByHash(ctx, ethapi.Hash(signed.Hash().Hex()))
		if err != nil || !result.IsCorroborated() || len(result.Decision.Support) != 2 {
			t.Fatalf("reordered round: %+v, %v", result, err)
		}
		return result.Decision.Value
	}
	plain := run(raw, raw)
	mixed := run(reordered, raw)
	reversed := run(raw, reordered)
	if !bytes.Equal(plain, mixed) || !bytes.Equal(plain, reversed) {
		t.Fatalf("key order changed the committed value: %s / %s / %s", plain, mixed, reversed)
	}
}

// TestTransactionCanonicalRoutePendingKeepsOriginalEvidence runs the pending
// fixture through the production route: the derived blockTimestamp lands in
// the committed value while the captured raw material stays the provider's
// own bytes — no derived member is written back into the evidence.
func TestTransactionCanonicalRoutePendingKeepsOriginalEvidence(t *testing.T) {
	raw, signed := transactionCompatibilityFixture(t)
	// The Anvil pending shape: the timestamp member is absent, not null.
	pending := transactionCompatibilityEdit(t, raw, map[string]json.RawMessage{
		"blockHash":        json.RawMessage(`null`),
		"blockNumber":      json.RawMessage(`null`),
		"blockTimestamp":   nil,
		"transactionIndex": json.RawMessage(`null`),
	})
	client, nodes := fleet(t, 2)
	for _, node := range nodes {
		node.ReturnRaw(methods.MethodGetTransactionByHash, pending)
	}
	base, err := ethguard.WithSourceEvidence(context.Background(), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(base, 5*time.Second)
	defer cancel()
	_, pendingFlag, result, err := client.TransactionByHash(ctx, ethapi.Hash(signed.Hash().Hex()))
	if err != nil || !pendingFlag || !result.IsCorroborated() {
		t.Fatalf("pending round: %v %v %v", pendingFlag, result.IsCorroborated(), err)
	}
	// The committed value carries the derived complete null; the raw evidence
	// does not.
	if !bytes.Contains(result.Decision.Value, []byte(`"blockTimestamp":null`)) {
		t.Fatalf("committed value lost the derived timestamp: %s", result.Decision.Value)
	}
	for _, source := range result.Uncertain.Raw {
		if bytes.Contains(source.Value, []byte("blockTimestamp")) {
			t.Fatalf("raw evidence was rewritten with the derived member: %s", source.Value)
		}
	}
}

// transactionCompatibilityReordered re-marshals the fixture's top-level
// fields in reverse insertion order.
func transactionCompatibilityReordered(t *testing.T, raw json.RawMessage) json.RawMessage {
	t.Helper()
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	keys := make([]string, 0, len(fields))
	for key := range fields {
		keys = append(keys, key)
	}
	// Reverse-order assembly: Go maps randomize, but a reversed write through
	// an ordered slice keeps this deterministic in spirit — the claim under
	// test is that no order matters, which the plain/reversed round pair
	// exercises.
	var b bytes.Buffer
	b.WriteByte('{')
	for i := len(keys) - 1; i >= 0; i-- {
		if i != len(keys)-1 {
			b.WriteByte(',')
		}
		key, err := json.Marshal(keys[i])
		if err != nil {
			t.Fatal(err)
		}
		b.Write(key)
		b.WriteByte(':')
		b.Write(fields[keys[i]])
	}
	b.WriteByte('}')
	return json.RawMessage(b.Bytes())
}
