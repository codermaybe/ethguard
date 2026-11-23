package subscription

import (
	"errors"
	"iter"
)

// AnchorNode is the immutable, pointer-free hash commitment retained in the DAG.
type AnchorNode struct {
	Number           uint64
	Hash, ParentHash [32]byte
}
type EvidenceTier uint8

const (
	Corroborated EvidenceTier = iota
	SingleSourceSelected
	Forwarded
)

type Acquisition uint8

const (
	Push Acquisition = iota
	ByHash
)

// PathWitness attests to a hash through verified edges, not to optional JSON fields.
type PathWitness struct {
	Session                                               SourceSession
	ReadSequence, ObservationRevision, EvaluationRevision uint64
	Tip                                                   AnchorNode
	Path                                                  []AnchorNode
}
type ObjectSupplier struct {
	Session     SourceSession
	Acquisition Acquisition
	RequestID   uint64
	TargetHash  [32]byte
}
type EvaluationSnapshot struct {
	Revision, ObservationRevision uint64
	Target                        *AnchorNode
	Witnesses                     []PathWitness
}
type PayloadLease struct {
	ID, Bytes uint64
	state     *payloadState
}
type PathChangeReason uint8

const (
	InitialAnchor PathChangeReason = iota
	ContinuousAdvance
	Reorg
	PreBirthReorg
)

type DeliveredHeader struct {
	Node                       AnchorNode
	Header, Evidence           PayloadLease
	HeaderValue, EvidenceValue any
	ObjectSupplier             ObjectSupplier
}
type PathDelta struct {
	BaseVersion, NewVersion uint64
	CommonAncestor          AnchorNode
	Retracted               []AnchorNode
	Appended                []DeliveredHeader
	PreBirthProof           []AnchorNode
	Reason                  PathChangeReason
}
type AuditKind uint8

type AuditReason uint8

const (
	ReasonObservation AuditReason = iota
	ReasonReconnect
	ReasonDisconnected
	ReasonTipExpired
	ReasonInvalidPush
	ReasonNullObject
	ReasonInvalidObject
	ReasonWrongTarget
	ReasonRequestTimeout
	ReasonRequestFailure
	ReasonTargetChanged
	ReasonGateExpired
	ReasonNotificationLimit
	ReasonMessageTooLarge
	ReasonSourceSetupFailure
)

type EvidenceBasis struct {
	DomainID string
	Members  []string
	Quorum   int
	Tier     EvidenceTier
}

const (
	TipChanged AuditKind = iota
	TipInvalidated
	SessionDisconnected
	InvalidObject
	RequestFailed
	EvaluationReplaced
	WaitExpired
)

// AuditRecord contains bounded identities and reasons; no raw wire payload is retained.
type AuditRecord struct {
	Sequence, ObservationRevision, EvaluationRevision, RequestID uint64
	Session                                                      SourceSession
	Previous, Current                                            AnchorNode
	Kind                                                         AuditKind
	Reason                                                       AuditReason
	At                                                           int64
}

// AuditRange owns an immutable sequence without copying its backing records.
// All returns records in order, bounded by this range's frozen last sequence.
type AuditRange struct {
	First, Last uint64
	first       *auditEntry
}

func (a AuditRange) All() iter.Seq[AuditRecord] {
	return func(yield func(AuditRecord) bool) {
		for n := a.first; n != nil && n.record.Sequence <= a.Last; n = n.next {
			if !yield(n.record) {
				return
			}
		}
	}
}
func (a AuditRange) releaseOwned() {
	for n := a.first; n != nil && n.record.Sequence <= a.Last; n = n.next {
		n.release()
	}
}

var (
	ErrStaleAttempt      = errors.New("subscription: stale request attempt")
	ErrStalePathVersion  = errors.New("subscription: stale path version")
	ErrEvidenceDomain    = errors.New("subscription: evidence domain mismatch")
	ErrInvalidCandidate  = errors.New("subscription: invalid header identity")
	ErrRecoveryExhausted = errors.New("subscription: recovery exhausted")
	ErrAuditTimeout      = errors.New("subscription: audit delivery deadline exceeded")
	ErrReadSequence      = errors.New("subscription: stale read sequence")
)
