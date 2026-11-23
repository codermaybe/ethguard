package subscription

// ObjectRequest is independent of a particular evaluation. Its immutable
// target remains useful when witnesses or the preparation target change.
type ObjectRequest struct {
	ID     uint64
	Hash   [32]byte
	Number uint64
}
type ObjectResult struct {
	Request ObjectRequest
	Session SourceSession
	Node    AnchorNode
	Header  PayloadLease
	Err     error
	Reason  AuditReason
}
