package subscription

import (
	"context"
	"errors"
	"testing"
	"time"
)

func strictConfig(t *testing.T) *Aggregator {
	t.Helper()
	a, err := New(Config{Snapshot: Snapshot{ID: "snap-1", Generation: 4, Members: []string{"a", "b", "c"}, Quorum: 2}, Mode: Strict, Queue: 4, SoftTimeout: 10 * time.Millisecond, HardTimeout: 20 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func obs(kind Kind, contextKey, key, source string) Observation {
	return Observation{Kind: kind, ContextKey: contextKey, Key: key, SourceID: source, Generation: 4, Payload: []byte(key), ObservedAt: time.Unix(100, 0)}
}

func TestStrictQuorumDeduplicatesSources(t *testing.T) {
	a := strictConfig(t)
	defer a.Close()
	if err := a.Submit(obs(KindNewHeads, "head:1", "candidate-a", "a")); err != nil {
		t.Fatal(err)
	}
	if err := a.Submit(obs(KindNewHeads, "head:1", "candidate-a", "a")); err != nil {
		t.Fatal(err)
	}
	select {
	case <-a.Events():
		t.Fatal("one source delivered before quorum")
	default:
	}
	if err := a.Submit(obs(KindNewHeads, "head:1", "candidate-a", "b")); err != nil {
		t.Fatal(err)
	}
	select {
	case delivery := <-a.Events():
		if delivery.Status != Decided || delivery.Votes != 2 || delivery.Snapshot.Quorum != 2 {
			t.Fatalf("delivery = %+v", delivery)
		}
	case <-time.After(time.Second):
		t.Fatal("quorum delivery missing")
	}
}

func TestStrictConflictAndReorgRemainUndelivered(t *testing.T) {
	a := strictConfig(t)
	defer a.Close()
	_ = a.Submit(obs(KindNewHeads, "head:1", "hash-a", "a"))
	_ = a.Submit(obs(KindNewHeads, "head:1", "hash-b", "b"))
	select {
	case <-a.Events():
		t.Fatal("conflicting candidates delivered")
	default:
	}
	if got := a.Status("head:1"); got != Collecting {
		t.Fatalf("status = %v", got)
	}
}

func TestSingleSourceSelectedAndRemoved(t *testing.T) {
	a, err := New(Config{Snapshot: Snapshot{ID: "snap", Generation: 1, Members: []string{"a"}, Quorum: 1}, Mode: SingleSourceSelected, Queue: 2, HardTimeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	contextKey, firstKey, err := CanonicalLogKey("0xblock", "0x1", "0xtx", 0, "0xaddr", "0xdata", nil, false)
	if err != nil {
		t.Fatal(err)
	}
	_, removedKey, err := CanonicalLogKey("0xblock", "0x1", "0xtx", 0, "0xaddr", "0xdata", nil, true)
	if err != nil {
		t.Fatal(err)
	}
	first := obs(KindLogs, contextKey, firstKey, "a")
	first.Generation = 1
	if err := a.Submit(first); err != nil {
		t.Fatal(err)
	}
	got := <-a.Events()
	if got.Status != Selected || got.Votes != 1 {
		t.Fatalf("selected = %+v", got)
	}
	first.Removed = true
	first.Key = removedKey
	if err := a.Submit(first); err != nil {
		t.Fatal(err)
	}
	revoked := <-a.Events()
	if revoked.Status != Revoked || !revoked.Removed {
		t.Fatalf("revoked = %+v", revoked)
	}
}

func TestStrictSingleSourceQuarantinesAtDeadline(t *testing.T) {
	a, err := New(Config{Snapshot: Snapshot{ID: "single", Generation: 1, Members: []string{"a"}, Quorum: 1}, Mode: Strict, Queue: 1, HardTimeout: 10 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	first := obs(KindNewHeads, "head:1", "candidate", "a")
	first.Generation = 1
	if err := a.Submit(first); err != nil {
		t.Fatal(err)
	}
	select {
	case <-a.Events():
		t.Fatal("strict single source was delivered")
	default:
	}
	a.Advance(first.ObservedAt.Add(20 * time.Millisecond))
	if got := a.Status("head:1"); got != Quarantined {
		t.Fatalf("status = %v, want quarantined", got)
	}
	if err := <-a.Err(); !errors.Is(err, ErrQuorumTimeout) {
		t.Fatalf("timeout error = %v", err)
	}
}

func TestTimeoutAndBackpressure(t *testing.T) {
	a := strictConfig(t)
	_ = a.Submit(obs(KindLogs, "log:tx:1", "log-a", "a"))
	a.Advance(time.Unix(100, 0).Add(25 * time.Millisecond))
	if got := a.Status("log:tx:1"); got != Quarantined {
		t.Fatalf("timeout status = %v", got)
	}
	if err := <-a.Err(); !errors.Is(err, ErrQuorumTimeout) {
		t.Fatalf("timeout err = %v", err)
	}
	a.Close()

	b, err := New(Config{Snapshot: Snapshot{ID: "snap", Generation: 1, Members: []string{"a", "b"}, Quorum: 2}, Mode: Strict, Queue: 1, HardTimeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	_ = b.Submit(Observation{Kind: KindLogs, ContextKey: "x", Key: "a", SourceID: "a", Generation: 1})
	_ = b.Submit(Observation{Kind: KindLogs, ContextKey: "x", Key: "a", SourceID: "b", Generation: 1})
	_ = b.Submit(Observation{Kind: KindLogs, ContextKey: "y", Key: "b", SourceID: "a", Generation: 1})
	if !errors.Is(b.Submit(Observation{Kind: KindLogs, ContextKey: "y", Key: "b", SourceID: "b", Generation: 1}), ErrBackpressure) {
		t.Fatal("expected backpressure")
	}
}

func TestGenerationSourceAndPullValidation(t *testing.T) {
	a := strictConfig(t)
	defer a.Close()
	if !errors.Is(a.Submit(obs(KindNewHeads, "x", "k", "z")), ErrUnknownSource) {
		t.Fatal("unknown source accepted")
	}
	stale := obs(KindNewHeads, "x", "k", "a")
	stale.Generation = 3
	if !errors.Is(a.Submit(stale), ErrStaleGeneration) {
		t.Fatal("stale generation accepted")
	}
	_, err := PullObservation(context.Background(), "a", 4, func(context.Context) (Observation, error) { return obs(KindLogs, "x", "k", "b"), nil })
	if !errors.Is(err, ErrUnknownSource) {
		t.Fatalf("pull source error = %v", err)
	}
}

func TestCanonicalKeysNormalizeAndKeepReorgIdentity(t *testing.T) {
	contextA, keyA, err := CanonicalNewHeadKey("0x1", "0xAA", "0xBB")
	if err != nil {
		t.Fatal(err)
	}
	contextB, keyB, err := CanonicalNewHeadKey("0x1", "0xaa", "0xbb")
	if err != nil {
		t.Fatal(err)
	}
	if contextA != contextB || keyA != keyB {
		t.Fatalf("hex normalization mismatch: %s/%s %s/%s", contextA, keyA, contextB, keyB)
	}
	_, reorgKey, err := CanonicalNewHeadKey("0x1", "0xcc", "0xdd")
	if err != nil {
		t.Fatal(err)
	}
	if keyA == reorgKey {
		t.Fatal("reorg candidate collapsed")
	}
	_, logA, err := CanonicalLogKey("0xAA", "0x1", "0xTX", 0, "0xADDR", "0xDATA", []string{"0xAB"}, false)
	if err != nil {
		t.Fatal(err)
	}
	_, logB, err := CanonicalLogKey("0xaa", "0x1", "0xtx", 0, "0xaddr", "0xdata", []string{"0xab"}, true)
	if err != nil {
		t.Fatal(err)
	}
	if logA == logB {
		t.Fatal("removed flag must change log identity")
	}
}

func TestCanonicalTransactionReceiptsKeyIsStableAcrossBatchOrder(t *testing.T) {
	receipts := []ReceiptIdentity{
		{TransactionHash: "0xB", BlockHash: "0xAA", BlockNumber: "0x10", TransactionIndex: 2, Canonical: []byte(`{"status":"0x1","logs":[]}`)},
		{TransactionHash: "0xA", BlockHash: "0xAA", BlockNumber: "0x10", TransactionIndex: 1, Canonical: []byte(`{"logs":[],"status":"0x1"}`)},
	}
	contextA, keyA, err := CanonicalTransactionReceiptsKey(receipts)
	if err != nil {
		t.Fatal(err)
	}
	contextB, keyB, err := CanonicalTransactionReceiptsKey([]ReceiptIdentity{receipts[1], receipts[0]})
	if err != nil {
		t.Fatal(err)
	}
	if contextA != contextB || keyA != keyB {
		t.Fatalf("batch order changed canonical identity: %s/%s vs %s/%s", contextA, keyA, contextB, keyB)
	}
	if _, _, err := CanonicalTransactionReceiptsKey([]ReceiptIdentity{{TransactionHash: "0xA", BlockHash: "0xAA", BlockNumber: "0x10", Canonical: []byte(`{`)}}); err == nil {
		t.Fatal("malformed canonical receipt accepted")
	}
}

func TestTransactionReceiptsReachStrictQuorumAndRejectConflictingBatch(t *testing.T) {
	a, err := New(Config{Snapshot: Snapshot{ID: "snap", Generation: 1, Members: []string{"a", "b"}, Quorum: 2}, Mode: Strict, Queue: 2, HardTimeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	rows := []ReceiptIdentity{{TransactionHash: "0xA", BlockHash: "0xAA", BlockNumber: "0x10", Canonical: []byte(`{"status":"0x1"}`)}}
	contextKey, key, err := CanonicalTransactionReceiptsKey(rows)
	if err != nil {
		t.Fatal(err)
	}
	for _, source := range []string{"a", "b"} {
		if err := a.Submit(Observation{Kind: KindTransactionReceipts, ContextKey: contextKey, Key: key, SourceID: source, Generation: 1, Payload: []byte("batch")}); err != nil {
			t.Fatal(err)
		}
	}
	got := <-a.Events()
	if got.Kind != KindTransactionReceipts || got.Status != Decided || got.Votes != 2 {
		t.Fatalf("receipt delivery = %+v", got)
	}
	_, conflictKey, err := CanonicalTransactionReceiptsKey([]ReceiptIdentity{{TransactionHash: "0xA", BlockHash: "0xAA", BlockNumber: "0x10", Canonical: []byte(`{"status":"0x0"}`)}})
	if err != nil {
		t.Fatal(err)
	}
	if conflictKey == key {
		t.Fatal("different receipt batch collapsed to same candidate")
	}
}

func TestCloseAndUnsubscribeAreIdempotent(t *testing.T) {
	a := strictConfig(t)
	a.Unsubscribe()
	a.Unsubscribe()
	a.Close()
	if _, ok := <-a.Err(); ok {
		t.Fatal("intentional unsubscribe reported a terminal error")
	}
	if err := a.Submit(obs(KindNewHeads, "x", "k", "a")); !errors.Is(err, ErrClosed) {
		t.Fatalf("submit after unsubscribe = %v", err)
	}
}

func TestKindIsPartOfEventIdentity(t *testing.T) {
	a, err := New(Config{Snapshot: Snapshot{ID: "snap", Generation: 1, Members: []string{"a", "b"}, Quorum: 2}, Mode: Strict, Queue: 2, HardTimeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	head := obs(KindNewHeads, "same", "head", "a")
	head.Generation = 1
	if err := a.Submit(head); err != nil {
		t.Fatal(err)
	}
	log := obs(KindLogs, "same", "log", "b")
	log.Generation = 1
	if err := a.Submit(log); err != nil {
		t.Fatal(err)
	}
	select {
	case <-a.Events():
		t.Fatal("cross-kind observations shared quorum context")
	default:
	}
}

func TestSnapshotMembersAreCopied(t *testing.T) {
	a := strictConfig(t)
	defer a.Close()
	s := a.Snapshot()
	s.Members[0] = "mutated"
	if got := a.Snapshot().Members[0]; got == "mutated" {
		t.Fatal("snapshot exposed mutable member storage")
	}
}

func TestRemovedEventRevokesAQuorumDelivery(t *testing.T) {
	a := strictConfig(t)
	defer a.Close()
	contextKey, key, err := CanonicalLogKey("0xabc", "0x10", "0xtx", 3, "0xaddr", "0xdata", nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Submit(obs(KindLogs, contextKey, key, "a")); err != nil {
		t.Fatal(err)
	}
	if err := a.Submit(obs(KindLogs, contextKey, key, "b")); err != nil {
		t.Fatal(err)
	}
	if got := <-a.Events(); got.Status != Decided || got.Removed {
		t.Fatalf("initial delivery = %+v", got)
	}
	_, removedKey, err := CanonicalLogKey("0xabc", "0x10", "0xtx", 3, "0xaddr", "0xdata", nil, true)
	if err != nil {
		t.Fatal(err)
	}
	removed := obs(KindLogs, contextKey, removedKey, "a")
	removed.Removed = true
	if err := a.Submit(removed); err != nil {
		t.Fatal(err)
	}
	removed.SourceID = "b"
	if err := a.Submit(removed); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-a.Events():
		if got.Status != Revoked || !got.Removed || got.Votes != 2 {
			t.Fatalf("revocation = %+v", got)
		}
	case <-time.After(time.Second):
		t.Fatal("revocation was not delivered")
	}
}

func TestStrictRemovedEventRequiresQuorumAndDeliveredPredecessor(t *testing.T) {
	a := strictConfig(t)
	defer a.Close()
	contextKey, key, err := CanonicalLogKey("0xabc", "0x10", "0xtx", 3, "0xaddr", "0xdata", nil, false)
	if err != nil {
		t.Fatal(err)
	}
	_, removedKey, err := CanonicalLogKey("0xabc", "0x10", "0xtx", 3, "0xaddr", "0xdata", nil, true)
	if err != nil {
		t.Fatal(err)
	}
	removed := obs(KindLogs, contextKey, removedKey, "a")
	removed.Removed = true
	if err := a.Submit(removed); !errors.Is(err, ErrUnboundRemoval) {
		t.Fatalf("unbound removal error = %v", err)
	}
	if got := a.Status(contextKey); got != Quarantined {
		t.Fatalf("unbound removal status = %v", got)
	}

	b := strictConfig(t)
	defer b.Close()
	if err := b.Submit(obs(KindLogs, contextKey, key, "a")); err != nil {
		t.Fatal(err)
	}
	if err := b.Submit(obs(KindLogs, contextKey, key, "b")); err != nil {
		t.Fatal(err)
	}
	<-b.Events()
	removed.SourceID = "a"
	if err := b.Submit(removed); err != nil {
		t.Fatal(err)
	}
	select {
	case <-b.Events():
		t.Fatal("single removal source revoked strict delivery")
	default:
	}
	removed.SourceID = "b"
	if err := b.Submit(removed); err != nil {
		t.Fatal(err)
	}
	select {
	case delivery := <-b.Events():
		if delivery.Status != Revoked || delivery.Votes != 2 || !delivery.Removed {
			t.Fatalf("revocation = %+v", delivery)
		}
	case <-time.After(time.Second):
		t.Fatal("quorum removal was not delivered")
	}
}

func TestSnapshotIsCopiedAndGenerationFiltersLateFrames(t *testing.T) {
	cfg := Config{Snapshot: Snapshot{ID: "snap", Generation: 7, Members: []string{"a", "b"}, Quorum: 2}, Mode: Strict, Queue: 2, HardTimeout: time.Second}
	a, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Snapshot.Members[0] = "mutated"
	defer a.Close()
	if got := a.Snapshot(); got.Members[0] != "a" {
		t.Fatalf("snapshot members were aliased: %+v", got.Members)
	}
	stale := obs(KindNewHeads, "head:1", "candidate", "a")
	stale.Generation = 6
	if err := a.Submit(stale); !errors.Is(err, ErrStaleGeneration) {
		t.Fatalf("stale generation error = %v", err)
	}
}

func TestPerSourceGenerationsFilterOnlyTheStaleSession(t *testing.T) {
	a, err := New(Config{Snapshot: Snapshot{ID: "snap", Generation: 1, Generations: map[string]uint64{"a": 2, "b": 7}, Members: []string{"a", "b"}, Quorum: 2}, Mode: Strict, Queue: 1, HardTimeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	if err := a.Submit(Observation{Kind: KindNewHeads, ContextKey: "head:1", Key: "candidate", SourceID: "a", Generation: 1}); !errors.Is(err, ErrStaleGeneration) {
		t.Fatalf("stale source generation accepted: %v", err)
	}
	if err := a.Submit(Observation{Kind: KindNewHeads, ContextKey: "head:1", Key: "candidate", SourceID: "a", Generation: 2}); err != nil {
		t.Fatal(err)
	}
	if err := a.Submit(Observation{Kind: KindNewHeads, ContextKey: "head:1", Key: "candidate", SourceID: "b", Generation: 7}); err != nil {
		t.Fatal(err)
	}
	if got := <-a.Events(); got.Votes != 2 {
		t.Fatalf("delivery votes = %d, want 2", got.Votes)
	}
}

func TestQueueOverflowReportsTerminalBackpressureAndClosesEvents(t *testing.T) {
	a, err := New(Config{Snapshot: Snapshot{ID: "snap", Generation: 1, Members: []string{"a", "b"}, Quorum: 2}, Mode: Strict, Queue: 1, HardTimeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	first := obs(KindNewHeads, "head:1", "one", "a")
	first.Generation = 1
	if err := a.Submit(first); err != nil {
		t.Fatal(err)
	}
	secondVote := obs(KindNewHeads, "head:1", "one", "b")
	secondVote.Generation = 1
	if err := a.Submit(secondVote); err != nil {
		t.Fatal(err)
	}
	second := obs(KindNewHeads, "head:2", "two", "a")
	second.Generation = 1
	_ = a.Submit(second)
	secondVote = obs(KindNewHeads, "head:2", "two", "b")
	secondVote.Generation = 1
	if err := a.Submit(secondVote); !errors.Is(err, ErrBackpressure) {
		t.Fatalf("overflow error = %v", err)
	}
	if err := <-a.Err(); !errors.Is(err, ErrBackpressure) {
		t.Fatalf("terminal error = %v", err)
	}
	if _, ok := <-a.Events(); ok {
		// The first decided delivery may remain buffered; drain it and require
		// the next receive to observe the closed channel.
		if _, ok = <-a.Events(); ok {
			t.Fatal("events channel remained open after backpressure")
		}
	}
}
