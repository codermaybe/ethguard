package rpc

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
)

// InvocationID identifies one logical call across all of its stages. Reference
// establishment rounds and the business round of one moving-tag read share one
// ID, so trace and observability records can be correlated without guessing.
// Every invocation gets a fresh ID; an ID is never reused across calls.
type InvocationID string

// NewInvocationID returns a random invocation identifier. It never returns an
// empty ID; a caller that needs a deterministic ID (tests, replay) assigns its
// own value instead.
func NewInvocationID() InvocationID {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		// crypto/rand failing is a process-level breakage; a panic here is
		// honest, while a constant ID would silently merge distinct calls'
		// traces.
		panic(fmt.Sprintf("rpc: invocation id: %v", err))
	}
	return InvocationID(hex.EncodeToString(buf[:]))
}

// InvocationCapture records raw, unnormalized upstream data for one
// invocation. It is the rpc-owned seam the invocation carries; the collector
// implementation lives outside rpc and is responsible for its own concurrency
// safety, because fan-out stages copy the invocation to concurrent branches.
type InvocationCapture interface {
	Record(stage, method string, source SourceRef, value []byte, errText string)
}

// Invocation is the stage value of one logical call. One invocation owns one
// frozen basis and at most one reference; every stage of the call — reference
// establishment, preparation, collection, admission, resolution, projection —
// works on the same invocation value.
//
// Stage visibility contract:
//
//   - Prepare and the reference/anchor stage see an invocation without a
//     reference; the reference is added once, by the reference stage, through
//     WithReference.
//   - Resolve and every later stage see the completed invocation.
//   - A stage that needs the reference before Resolve declares that need
//     explicitly; there is no implicit second channel.
//
// Value semantics: Invocation is passed and copied by value. WithReference
// returns a new value carrying its own evidence copy; the receiver it was
// called on stays without a reference. Slices held by an invocation (basis
// members, reference evidence) are copied on the way in and on the way out, so
// no stage shares a mutable slice with another. The capture, when present, is
// shared by design and must be safe for concurrent Record calls.
type Invocation struct {
	id                   InvocationID
	basis                Basis
	reference            *ReferenceEvidence
	requireCorroboration bool
	capture              InvocationCapture
}

// InvocationOption tunes a new invocation at construction.
type InvocationOption func(*Invocation)

// WithInvocationCapture attaches the invocation's raw-data collector.
func WithInvocationCapture(c InvocationCapture) InvocationOption {
	return func(in *Invocation) { in.capture = c }
}

// WithRequiredCorroboration marks invocations whose typed boundary cannot
// expose a Selected decision: after resolution, anything short of Corroborated
// fails the call. The gate itself is applied by the coordinator, not here.
func WithRequiredCorroboration() InvocationOption {
	return func(in *Invocation) { in.requireCorroboration = true }
}

// NewInvocation begins one logical call with a frozen basis.
func NewInvocation(id InvocationID, basis Basis, opts ...InvocationOption) Invocation {
	in := Invocation{id: id, basis: basis.Clone()}
	for _, opt := range opts {
		opt(&in)
	}
	return in
}

// ID reports the invocation identifier shared by every stage of this call.
func (in Invocation) ID() InvocationID { return in.id }

// Basis reports the frozen voting basis as a private copy: the slices are
// cloned on the way out, so a caller mutating the returned value can never
// rewrite the invocation's internal members. The basis itself is immutable
// after FreezeBasis; the exported Members field is read-only by contract,
// and this accessor is what guarantees the invocation's own copy stays that
// way.
func (in Invocation) Basis() Basis { return in.basis.Clone() }

// RequireCorroboration reports whether the call boundary requires a
// corroborated result (named to avoid confusion with a generic agreement
// gate: it is the typed-read corroboration requirement, nothing else).
func (in Invocation) RequireCorroboration() bool { return in.requireCorroboration }

// Capture reports the invocation's raw-data collector, or nil.
func (in Invocation) Capture() InvocationCapture { return in.capture }

// HasReference reports whether the reference stage has already established
// this invocation's reference. Prepare and the anchor stage observe false;
// resolution observes true for moving-tag reads. Stateless reads keep false
// for their whole life.
func (in Invocation) HasReference() bool { return in.reference != nil }

// WithReference attaches the established reference evidence and returns the
// completed invocation. It succeeds exactly once per invocation: a second
// call returns an error, because a call that re-established its reference
// mid-flight would mix evidence from two chains. The evidence is cloned on
// the way in, so later mutation of the caller's value cannot rewrite recorded
// evidence. A failed establishment never reaches this method — the call fails
// before the business round starts.
func (in Invocation) WithReference(ref ReferenceEvidence) (Invocation, error) {
	if in.reference != nil {
		return in, fmt.Errorf("rpc: invocation %s already carries a reference: %w", in.id, ErrInvalidRequest)
	}
	clone := ref.Clone()
	in.reference = &clone
	return in, nil
}

// Reference returns a copy of the established reference evidence. The bool is
// false before the reference stage ran and for stateless reads. Callers never
// share the invocation's internal slice.
func (in Invocation) Reference() (ReferenceEvidence, bool) {
	if in.reference == nil {
		return ReferenceEvidence{}, false
	}
	return in.reference.Clone(), true
}
