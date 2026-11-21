package ethguard

import "github.com/codermaybe/ethguard/rpc"

// CallResult is the execution result carried by every public operation: the
// aggregation facts the call formed plus the call's final business
// conclusion. Decision belongs to an ordinary read; WriteDecision belongs to
// the broadcast. A synchronous write also retains its bounded ReceiptWait,
// whose read decisions each belong to their own polling attempt. Err equals
// the operation's trailing error and never accumulates past polling errors.
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
	// ReceiptWait records the last query and latest formed receipt observation
	// of a synchronous write. Nil means receipt waiting never began. Its facts
	// are retained even when optional source-evidence capture is disabled/full.
	ReceiptWait *ReceiptWaitResult
	// Uncertain is the optional source-evidence snapshot. WithSourceEvidence
	// enables it for root results directly and for view calls with a bound
	// ResultCapture. Its presence never changes the business outcome.
	Uncertain *UncertainResult
	// Err is the call's final business conclusion, identical to the
	// operation's trailing error. nil means the operation succeeded.
	Err error
}

// IsCorroborated reports whether the read reached the Corroborated class.
// Status records the guarantee level, while Err records the final conclusion.
// A corroborated revert or receipt-null reports true here while Err carries
// the classified business error. A write result never reports corroboration.
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

// ReceiptWaitResult retains at most two receipt-query outcomes, independently
// of the number of polls. A later interrupted query does not replace an older
// formed observation, and neither position borrows votes from another query.
type ReceiptWaitResult struct {
	LastAttempt     *ReceiptAttempt
	LastObservation *ReceiptAttempt
}

// ReceiptAttempt records one actual transaction-receipt query, not one source
// request. Sequence starts at one. Decision and Err belong to this query;
// CallResult.Err alone describes how the whole synchronous operation ended.
// A nil Decision means the query ended before it could form read evidence.
type ReceiptAttempt struct {
	Sequence uint64
	Decision *rpc.Decision
	Err      error
}
