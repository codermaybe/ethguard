package subscription

import (
	"errors"
	"testing"
)

func TestBudgetRejectsOversizedPayload(t *testing.T) {
	a, err := New(Config{Snapshot: Snapshot{ID: "s", Members: []string{"a", "b"}, Quorum: 2}, Mode: Strict, Queue: 1, HardTimeout: 1, Budget: Budget{MaxPayloadBytes: 2}})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	err = a.Submit(Observation{Kind: KindNewHeads, ContextKey: "h", Key: "k", SourceID: "a", Payload: []byte("too long")})
	if !errors.Is(err, ErrPayloadTooLarge) {
		t.Fatalf("err = %v, want payload limit", err)
	}
}

func TestHeadGapTrackerReportsSkippedHeightsPerSource(t *testing.T) {
	tracker := NewHeadGapTracker()
	if tracker.Observe("a", 10) != nil || tracker.Observe("a", 11) != nil {
		t.Fatal("reported gap for contiguous heads")
	}
	gap := tracker.Observe("a", 14)
	if gap == nil || gap.From != 12 || gap.To != 13 {
		t.Fatalf("gap = %+v", gap)
	}
	if tracker.Observe("b", 14) != nil {
		t.Fatal("source b inherited source a height")
	}
}
