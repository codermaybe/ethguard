package rpc

// WriteMethodEthSendRawTransaction is the JSON-RPC method the write pipeline
// broadcasts. It is deliberately the only write method in scope (decision-map
// #4): the same raw bytes re-sent to several sources is idempotent — every
// honest node derives the same transaction hash from them — so re-broadcast
// and catch-up re-sends stay safe. Account-managed eth_sendTransaction has
// no such idempotence and stays out of scope.
const WriteMethodEthSendRawTransaction = "eth_sendRawTransaction"

// WriteStatus classifies one broadcast outcome. The write path cannot reuse
// read Status: a minority of dissenting hash answers must not fail a
// propagation that reached quorum, and a receipt is a separate, later phase.
type WriteStatus int

const (
	// WriteUnknown is the zero value used when no write decision was made, for
	// example because request validation failed before contacting a source.
	WriteUnknown WriteStatus = iota
	// WriteAccepted means an agreeing hash cluster met the write quorum over
	// the full voting basis.
	WriteAccepted
	// WriteUnconfirmed means at least one attempt ran, but the propagation
	// condition was not confirmed. Adjudication and Sources carry the reasons.
	WriteUnconfirmed
)

// String names the write status for observability.
func (s WriteStatus) String() string {
	switch s {
	case WriteAccepted:
		return "accepted"
	case WriteUnconfirmed:
		return "unconfirmed"
	default:
		return "unknown"
	}
}

// SourceAnswer is one source's outcome in a broadcast, classified at the
// write boundary. Answers keep the source identity so callers can follow up
// per source (for example, re-broadcasting to a source whose answer arrived
// late).
type SourceAnswer struct {
	SourceSummary
	// Hash is the accepted transaction hash this source reported. Empty when
	// Rejected or unreachable. The wire value is normalized to lower-case by
	// the client before it reaches the resolver.
	Hash string
	// Rejection is a structured protocol error body from the source —
	// "nonce too low", replacement underpricing, an execution revert, and
	// similar. It is evidence on the decision: nothing here classifies a
	// rejection body as acceptance or as a permanent failure, because that
	// reading is a txpool-behaviour question this layer does not decide.
	Rejection *RPCError
}

// WriteDecision is the relative-trust result of one broadcast. It never
// claims Ethereum consensus finality: Accepted means a hash agreement reached the
// configured support bound across the voting basis, with every other
// outcome preserved as evidence.
type WriteDecision struct {
	Status WriteStatus
	// SubmittedHash is frozen from the local signed transaction before broadcast.
	SubmittedHash string
	// Hash is the leading reported hash, even when support or binding fails.
	Hash         string
	Sources      []SourceAnswer
	Adjudication Adjudication
	// Faults contains collection-level failures. Source causes live in Sources.
	Faults []error
}
