package subscription

import (
	"errors"
	"testing"
)

func TestSessionGateRejectsLateFramesAfterReconnect(t *testing.T) {
	gate, err := NewSessionGate([]string{"a", "b"})
	if err != nil {
		t.Fatal(err)
	}
	first, err := gate.Open("a")
	if err != nil {
		t.Fatal(err)
	}
	if first.Generation != 1 || gate.Accept(first) != nil {
		t.Fatalf("first session = %+v", first)
	}
	second, err := gate.Open("a")
	if err != nil {
		t.Fatal(err)
	}
	if second.Generation != 2 {
		t.Fatalf("reconnect generation = %d, want 2", second.Generation)
	}
	if !errors.Is(gate.Accept(first), ErrSessionStale) {
		t.Fatal("late frame from old connection was accepted")
	}
	if err := gate.Accept(second); err != nil {
		t.Fatalf("current session rejected: %v", err)
	}
	if current, err := gate.Current("a"); err != nil || current.Generation != 2 {
		t.Fatalf("current session = %+v, err=%v", current, err)
	}
}

func TestSessionGateGenerationsArePerSource(t *testing.T) {
	gate, err := NewSessionGate([]string{"a", "b"})
	if err != nil {
		t.Fatal(err)
	}
	a, _ := gate.Open("a")
	b, _ := gate.Open("b")
	if a.Generation != 1 || b.Generation != 1 {
		t.Fatalf("initial generations = %+v/%+v", a, b)
	}
	_, _ = gate.Open("a")
	if err := gate.Accept(b); err != nil {
		t.Fatalf("reconnecting a invalidated b: %v", err)
	}
}

func TestSessionGateCloseDoesNotChangeMembershipSemantics(t *testing.T) {
	gate, err := NewSessionGate([]string{"a", "b"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := gate.Open("missing"); !errors.Is(err, ErrUnknownSource) {
		t.Fatalf("unknown open error = %v", err)
	}
	session, err := gate.Open("a")
	if err != nil {
		t.Fatal(err)
	}
	gate.Close()
	if _, err := gate.Open("a"); !errors.Is(err, ErrSessionClosed) {
		t.Fatalf("open after close = %v", err)
	}
	if err := gate.Accept(session); !errors.Is(err, ErrSessionClosed) {
		t.Fatalf("accept after close = %v", err)
	}
}

func TestSessionGateRejectsInvalidMembership(t *testing.T) {
	if _, err := NewSessionGate([]string{"a", "a"}); !errors.Is(err, ErrUnknownSource) {
		t.Fatalf("duplicate source accepted: %v", err)
	}
	if _, err := NewSessionGate([]string{""}); !errors.Is(err, ErrUnknownSource) {
		t.Fatalf("empty source accepted: %v", err)
	}
}

func TestSessionGateRetiresOnlyTheCurrentSession(t *testing.T) {
	gate, err := NewSessionGate([]string{"a", "b"})
	if err != nil {
		t.Fatal(err)
	}
	first, _ := gate.Open("a")
	other, _ := gate.Open("b")
	gate.Retire(first)
	if _, err := gate.Current("a"); !errors.Is(err, ErrSessionStale) {
		t.Fatalf("retired session is current: %v", err)
	}
	if err := gate.Accept(first); !errors.Is(err, ErrSessionStale) {
		t.Fatalf("retired session accepted: %v", err)
	}
	if err := gate.Accept(other); err != nil {
		t.Fatalf("retiring a changed b: %v", err)
	}
	second, _ := gate.Open("a")
	if second.Generation != first.Generation+1 {
		t.Fatalf("generation reset: %+v then %+v", first, second)
	}
	gate.Retire(first)
	if err := gate.Accept(second); err != nil {
		t.Fatalf("old retirement invalidated replacement: %v", err)
	}
}
