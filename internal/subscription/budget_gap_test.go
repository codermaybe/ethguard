package subscription

import (
	"context"
	"errors"
	"testing"
	"time"
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

func TestBudgetRejectsLifetimeEvidenceOverflow(t *testing.T) {
	a, err := New(Config{Snapshot: Snapshot{ID: "s", Members: []string{"a", "b"}, Quorum: 2}, Mode: Strict, Queue: 2, HardTimeout: time.Second, Budget: Budget{MaxEvidenceBytes: 4}})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	base := Observation{Kind: KindNewHeads, SourceID: "a", Generation: 0, Payload: []byte("1234")}
	base.ContextKey, base.Key = "h1", "k1"
	if err := a.Submit(base); err != nil {
		t.Fatal(err)
	}
	second := base
	second.ContextKey, second.Key = "h2", "k2"
	if err := a.Submit(second); !errors.Is(err, ErrEvidenceBudget) {
		t.Fatalf("err = %v, want evidence budget", err)
	}
	select {
	case err := <-a.Err():
		if !errors.Is(err, ErrEvidenceBudget) {
			t.Fatalf("terminal err = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("evidence budget error was not reported")
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

func TestHeadGapTrackerDoesNotInventGapOnRewindOrDuplicate(t *testing.T) {
	tracker := NewHeadGapTracker()
	tracker.Observe("a", 20)
	if gap := tracker.Observe("a", 20); gap != nil {
		t.Fatalf("duplicate produced gap: %+v", gap)
	}
	if gap := tracker.Observe("a", 18); gap != nil {
		t.Fatalf("rewind produced gap: %+v", gap)
	}
	if gap := tracker.Observe("a", 21); gap != nil {
		t.Fatalf("post-rewind contiguous head produced gap: %+v", gap)
	}
}

func TestPullProbeStopCancelsOwnerContext(t *testing.T) {
	a, err := New(Config{Snapshot: Snapshot{ID: "s", Members: []string{"a", "b"}, Quorum: 2}, Mode: Strict, Queue: 1, HardTimeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	started := make(chan struct{})
	finished := make(chan struct{})
	probe, err := StartPullProbe(context.Background(), a, []PullRequest{{Kind: KindNewHeads, ContextKey: "h", SourceID: "b", Generation: 1, FirstSeen: time.Now(), Pull: func(ctx context.Context) (Observation, error) {
		close(started)
		<-ctx.Done()
		close(finished)
		return Observation{}, ctx.Err()
	}}}, PullProbeConfig{SoftTimeout: time.Millisecond, HardTimeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer probe.Stop()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("pull did not start")
	}
	probe.Stop()
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("owner stop did not cancel pull")
	}
}
