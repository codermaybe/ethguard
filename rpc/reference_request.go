package rpc

import "strings"

// ReferenceKind names the moving tag a read's reference is established at.
// Each kind keeps its level: safe is anchored at the safe reference,
// finalized at the finalized reference, never silently latest.
type ReferenceKind string

const (
	ReferenceLatest    ReferenceKind = "latest"
	ReferenceSafe      ReferenceKind = "safe"
	ReferenceFinalized ReferenceKind = "finalized"
)

// ReferenceRequest is a method's declared need for an established reference:
// which moving tag the request rides on. Declaring the need is all a method
// does — the reference stage performs the network rounds and attaches the
// evidence to the invocation.
type ReferenceRequest struct {
	Kind ReferenceKind
}

// ReferenceKindForTag maps a wire moving tag to its reference kind; the
// empty tag and unknown spellings map to the default reference.
func ReferenceKindForTag(tag string) ReferenceKind {
	switch strings.ToLower(tag) {
	case string(ReferenceSafe):
		return ReferenceSafe
	case string(ReferenceFinalized):
		return ReferenceFinalized
	default:
		return ReferenceLatest
	}
}

// IsValid reports whether the kind names a known establishment level.
func (r ReferenceRequest) IsValid() bool {
	switch r.Kind {
	case ReferenceLatest, ReferenceSafe, ReferenceFinalized:
		return true
	}
	return false
}
