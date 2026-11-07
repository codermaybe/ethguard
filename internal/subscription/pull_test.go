package subscription

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestPullProbeAdmitsBoundSameSourceVoteAfterSoftTimeout(t *testing.T) {
	first := time.Now()
	a, err := New(Config{Snapshot: Snapshot{ID: "snap", Generation: 1, Members: []string{"a", "b"}, Quorum: 2}, Mode: Strict, Queue: 1, HardTimeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	if err := a.Submit(Observation{Kind: KindNewHeads, ContextKey: "head:1", Key: "candidate", SourceID: "a", Generation: 1, ObservedAt: first}); err != nil {
		t.Fatal(err)
	}
	probe, err := StartPullProbe(context.Background(), a, []PullRequest{{Kind: KindNewHeads, ContextKey: "head:1", SourceID: "b", Generation: 1, FirstSeen: first, Pull: func(context.Context) (Observation, error) {
		return Observation{Kind: KindNewHeads, ContextKey: "head:1", Key: "candidate", SourceID: "b", Generation: 1}, nil
	}}}, PullProbeConfig{SoftTimeout: time.Millisecond, HardTimeout: 100 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	defer probe.Stop()
	select {
	case delivery := <-a.Events():
		if delivery.Status != Decided || delivery.Votes != 2 {
			t.Fatalf("delivery = %+v", delivery)
		}
	case <-time.After(time.Second):
		t.Fatal("pull vote did not reach quorum")
	}
}

func TestPullProbeRejectsIdentityMismatchWithoutVoting(t *testing.T) {
	a := strictConfig(t)
	defer a.Close()
	first := time.Now()
	if err := a.Submit(Observation{Kind: KindNewHeads, ContextKey: "head:1", Key: "candidate", SourceID: "a", Generation: 4, ObservedAt: first}); err != nil {
		t.Fatal(err)
	}
	probe, err := StartPullProbe(context.Background(), a, []PullRequest{{Kind: KindNewHeads, ContextKey: "head:1", SourceID: "b", Generation: 4, FirstSeen: first, Pull: func(context.Context) (Observation, error) {
		return Observation{Kind: KindNewHeads, ContextKey: "head:2", Key: "candidate", SourceID: "b", Generation: 4}, nil
	}}}, PullProbeConfig{SoftTimeout: time.Millisecond, HardTimeout: 30 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	defer probe.Stop()
	var probeErr *ProbeError
	select {
	case err := <-probe.Err():
		if !errors.As(err, &probeErr) || !errors.Is(err, ErrUnknownSource) {
			t.Fatalf("probe err = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("identity mismatch was not reported")
	}
	select {
	case <-a.Events():
		t.Fatal("mismatched pull reached quorum")
	default:
	}
}

func TestPullProbeHardTimeoutQuarantinesUnmetEvent(t *testing.T) {
	first := time.Now()
	a := strictConfig(t)
	defer a.Close()
	if err := a.Submit(Observation{Kind: KindNewHeads, ContextKey: "head:1", Key: "candidate", SourceID: "a", Generation: 4, ObservedAt: first}); err != nil {
		t.Fatal(err)
	}
	probe, err := StartPullProbe(context.Background(), a, []PullRequest{{Kind: KindNewHeads, ContextKey: "head:1", SourceID: "b", Generation: 4, FirstSeen: first, Pull: func(ctx context.Context) (Observation, error) {
		<-ctx.Done()
		return Observation{}, ctx.Err()
	}}}, PullProbeConfig{SoftTimeout: time.Millisecond, HardTimeout: 20 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	defer probe.Stop()
	deadline := time.After(time.Second)
	for {
		if a.Status("head:1") == Quarantined {
			break
		}
		select {
		case <-deadline:
			t.Fatal("event was not quarantined at hard timeout")
		case <-time.After(time.Millisecond):
		}
	}
}
