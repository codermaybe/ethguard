package rpc

import (
	"encoding/json"
	"errors"
	"sync"
	"testing"
)

type countingCapture struct {
	mu      sync.Mutex
	records int
}

func (c *countingCapture) Record(stage, method string, source SourceRef, value []byte, errText string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.records++
}

func TestInvocationSharesOneIDAndFrozenBasis(t *testing.T) {
	basis := FreezeBasis([]SourceRef{{ID: "a"}}, Corroboration{Mode: CorroborationMajority, Floor: 2})
	in := NewInvocation(NewInvocationID(), basis, WithRequiredCorroboration())
	if in.ID() == "" {
		t.Fatal("invocation id is empty")
	}
	if !in.RequireCorroboration() {
		t.Fatal("required-corroboration option was dropped")
	}
	if got, want := in.Basis().Size(), 1; got != want {
		t.Fatalf("invocation basis size = %d, want %d", got, want)
	}
}

func TestInvocationReferenceSetExactlyOnce(t *testing.T) {
	in := NewInvocation(NewInvocationID(), FreezeBasis(nil, Corroboration{}))
	if _, ok := in.Reference(); ok {
		t.Fatal("fresh invocation already carries a reference")
	}
	completed, err := in.WithReference(ReferenceEvidence{Tag: "latest", Status: Corroborated, Hash: "0xabc"})
	if err != nil {
		t.Fatalf("WithReference: %v", err)
	}
	if _, err := completed.WithReference(ReferenceEvidence{Hash: "0xdef"}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("second WithReference error = %v, want ErrInvalidRequest", err)
	}
	// The rejected second establishment must not reach the completed value.
	ref, ok := completed.Reference()
	if !ok || ref.Hash != "0xabc" {
		t.Fatalf("reference after rejected re-establishment = %+v, ok=%v", ref, ok)
	}
}

func TestInvocationReferenceReturnsClonedEvidence(t *testing.T) {
	in := NewInvocation(NewInvocationID(), FreezeBasis(nil, Corroboration{}))
	completed, err := in.WithReference(ReferenceEvidence{
		Participants: []SourceRef{{ID: "a"}},
		Support:      []SourceRef{{ID: "a"}},
		Faults:       []error{errors.New("first")},
	})
	if err != nil {
		t.Fatalf("WithReference: %v", err)
	}
	ref, _ := completed.Reference()
	ref.Participants[0].ID = "mutated"
	ref.Faults[0] = errors.New("mutated")
	again, _ := completed.Reference()
	if again.Participants[0].ID != "a" || again.Faults[0].Error() != "first" {
		t.Fatalf("invocation reference shares internal slices: %+v", again)
	}
}

func TestInvocationStageVisibility(t *testing.T) {
	// Prepare and the reference stage observe an invocation without a
	// reference; resolution observes the completed one. Value semantics are
	// what enforces the split: WithReference returns a new value, the
	// pre-establishment value stays without a reference.
	in := NewInvocation(NewInvocationID(), FreezeBasis(nil, Corroboration{}))
	if in.HasReference() {
		t.Fatal("pre-establishment invocation claims a reference")
	}
	completed, err := in.WithReference(ReferenceEvidence{Status: Corroborated})
	if err != nil {
		t.Fatalf("WithReference: %v", err)
	}
	if in.HasReference() {
		t.Fatal("WithReference mutated its receiver instead of returning a new value")
	}
	if !completed.HasReference() {
		t.Fatal("completed invocation lost its reference")
	}
}

func TestInvocationConcurrentReadsAfterEstablishment(t *testing.T) {
	capture := &countingCapture{}
	in := NewInvocation(NewInvocationID(), FreezeBasis([]SourceRef{{ID: "a"}, {ID: "b"}},
		Corroboration{Mode: CorroborationMajority, Floor: 2}), WithInvocationCapture(capture))
	completed, err := in.WithReference(ReferenceEvidence{Status: Corroborated, Hash: "0xabc"})
	if err != nil {
		t.Fatalf("WithReference: %v", err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// Fan-out branches copy the invocation by value and read it
			// concurrently; the capture is shared and records from every
			// branch.
			capture.Record("response", "eth_test", SourceRef{ID: "a"}, json.RawMessage("null"), "")
			if _, ok := completed.Reference(); !ok {
				t.Error("concurrent reader lost the reference")
			}
			if completed.Basis().Size() != 2 {
				t.Error("concurrent reader saw a mutated basis")
			}
		}()
	}
	wg.Wait()
}

func TestInvocationIDGenerationIsUnique(t *testing.T) {
	seen := make(map[InvocationID]struct{})
	for i := 0; i < 100; i++ {
		id := NewInvocationID()
		if id == "" {
			t.Fatal("generated invocation id is empty")
		}
		if _, dup := seen[id]; dup {
			t.Fatalf("invocation id %s generated twice", id)
		}
		seen[id] = struct{}{}
	}
}
