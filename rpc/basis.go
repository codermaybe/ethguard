package rpc

// Basis is the voting basis frozen before any request of one logical call is
// issued. Members are the sources that occupy the denominator — including
// members a scheduling gate skipped and members that later fail — so a
// partially delivered fan-out can never shrink the required support
// (decisions 0025 and 0027). Basis is immutable after construction.
//
// The basis does not know pool sources. Members carry identity only
// (SourceRef); connection lifecycle and health stay in pool. This keeps the
// dependency direction rpc ← pool rather than rpc → pool.
type Basis struct {
	// Members is the frozen member set in captured order. The order is the
	// capture order of the fan-out; it is not voting evidence. Diagnostics
	// that need a stable order use SortedMembers.
	Members []SourceRef
	// Corroboration is the adjudication snapshot captured at invocation
	// start: the mode and support floor frozen before any request.
	Corroboration Corroboration
}

// FreezeBasis captures the member set and adjudication snapshot for one
// invocation. It copies the member slice, so later mutation of the caller's
// slice cannot rewrite a frozen basis.
func FreezeBasis(members []SourceRef, corroboration Corroboration) Basis {
	return Basis{
		Members:       append([]SourceRef(nil), members...),
		Corroboration: corroboration,
	}
}

// Size reports the voting-basis denominator. Every member counts, whether or
// not its request was attempted.
func (b Basis) Size() int { return len(b.Members) }

// Required reports the support a winning cluster must hold against this
// basis. In Forwarded mode no corroboration claim is made and callers do not
// consult this value.
func (b Basis) Required() int {
	return b.Corroboration.Required(b.Size())
}

// SortedMembers returns the member set ordered by Source.ID (name breaking
// ties), for deterministic diagnostics. The order of the basis itself is not
// evidence and is never used for adjudication.
func (b Basis) SortedMembers() []SourceRef {
	out := append([]SourceRef(nil), b.Members...)
	sortSourceRefs(out)
	return out
}

// Clone deep-copies the member slice. The Corroboration snapshot is a value.
func (b Basis) Clone() Basis {
	b.Members = append([]SourceRef(nil), b.Members...)
	return b
}

func sortSourceRefs(refs []SourceRef) {
	// Insertion sort keeps this file free of a sort import while the member
	// sets involved are small; identity order is stable because IDs are
	// unique within one invocation's basis.
	for i := 1; i < len(refs); i++ {
		for j := i; j > 0 && sourceRefLess(refs[j], refs[j-1]); j-- {
			refs[j], refs[j-1] = refs[j-1], refs[j]
		}
	}
}

func sourceRefLess(a, b SourceRef) bool {
	if a.ID != b.ID {
		return a.ID < b.ID
	}
	return a.Name < b.Name
}
