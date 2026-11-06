package ethguard

import "github.com/codermaybe/ethguard/rpc"

// CallResult is the execution result carried by every public operation: the
// aggregation facts the call formed plus the call's final business
// conclusion. Decision and WriteDecision are mutually exclusive — a read
// fills Decision, a write fills WriteDecision, and a call that failed before
// any adjudication can leave both nil. Err equals the operation's trailing
// error; it is repeated here so one value carries the whole outcome.
//
// A failed call keeps the facts its aggregation actually formed: a failed
// establishment keeps its reference evidence, a corroborated receipt-null
// keeps its null payload, a corroborated revert keeps its classification and
// data. Only a main aggregation failure clears Decision.Value on the public
// result; diagnostics (Support, Reject, Faults) are never erased by a
// non-nil Err.
type CallResult struct {
	// Decision is the read-side result: reference evidence, the resolved
	// value, status, and diagnostics. nil for writes and for calls that
	// failed before any read adjudication.
	Decision *rpc.Decision
	// WriteDecision is the broadcast result of a write operation. nil for
	// reads.
	WriteDecision *rpc.WriteDecision
	// Uncertain is the collected source-evidence snapshot when the call ran
	// with WithSourceEvidence and a ResultCapture published it; nil
	// otherwise. Its presence never changes the business outcome.
	Uncertain *UncertainResult
	// Err is the call's final business conclusion, identical to the
	// operation's trailing error. nil means the operation succeeded.
	Err error
}

// IsCorroborated reports whether the read reached the Corroborated class. It
// inspects only Decision.Status — the two-layer fact split keeps the
// business conclusion in Err, so a corroborated revert or a corroborated
// receipt-null reports true here while Err carries the classified business
// error. A write result never reports corroboration.
func (r CallResult) IsCorroborated() bool {
	return r.Decision != nil && r.Decision.Status == rpc.Corroborated
}

// IsWriteAccepted reports whether the write reached its support bound with a
// concrete transaction hash. It does not claim the transaction has been
// mined: acceptance is propagation, the receipt is a separate later read.
func (r CallResult) IsWriteAccepted() bool {
	return r.WriteDecision != nil &&
		r.WriteDecision.Status == rpc.WriteAccepted &&
		r.WriteDecision.Hash != ""
}
