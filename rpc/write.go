package rpc

import (
	"fmt"
	"sort"
)

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
	// WriteAccepted means an agreeing hash cluster met the write quorum over
	// the full voting basis.
	WriteAccepted WriteStatus = iota
	// WriteDisputed means sources answered with disagreeing hashes and no
	// cluster met the write quorum. Nothing can be claimed about acceptance.
	WriteDisputed
	// WriteRejected means every answering source returned an execution-level
	// rejection (txpool or protocol error). The transaction was not accepted
	// by any answering source.
	WriteRejected
	// WriteUnreachable means no source produced a usable answer at all
	// (transport silence). Acceptance is unknown.
	WriteUnreachable
)

// String names the write status for observability.
func (s WriteStatus) String() string {
	switch s {
	case WriteAccepted:
		return "accepted"
	case WriteDisputed:
		return "disputed"
	case WriteRejected:
		return "rejected"
	case WriteUnreachable:
		return "unreachable"
	default:
		return "unknown"
	}
}

// SourceAnswer is one source's outcome in a broadcast, classified at the
// write boundary. Answers keep the source identity so callers can follow up
// per source (for example, re-broadcasting to a source whose answer arrived
// late).
type SourceAnswer struct {
	Source SourceRef
	// Hash is the accepted transaction hash this source reported. Empty when
	// Rejected or unreachable. RawMessage is the untouched wire hash value.
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
	Status   WriteStatus
	Hash     string
	Accepted []SourceAnswer
	Rejected []SourceAnswer
	Silent   []SourceRef
	// Agreement is the size of the winning hash cluster.
	Agreement int
	// Basis is the number of voting members the outcome was adjudicated
	// against (fixed before any request, decision 0025).
	Basis int
	// Required is the support bound the winning cluster met.
	Required int
	// Faults preserves per-source collection causes that did not change the
	// classification.
	Faults []error
}

// WriteQuorum computes the support a hash cluster must reach for
// WriteAccepted over a voting basis of n members with caller floor h:
// Q = max(h, floor(n/2)+1) — the same shape as the read-side
// Corroboration.Required, applied to propagation rather than value truth.
func WriteQuorum(n, h int) int {
	q := n/2 + 1
	if h > q {
		q = h
	}
	return q
}

// ResolveWriteBroadcast adjudicates one completed broadcast's answers under
// a fixed voting basis. Per-source outcomes are already classified by the
// caller into SourceAnswer; this function only clusters and adjudicates. It
// returns the decision; the winning hash rides on it for downstream receipt
// binding.
//
// The relative-trust model (decision-map #4): a quorum-reaching hash cluster
// is WriteAccepted even when a minority dissented — dissent is evidence on
// the decision, not a failure. Only when no cluster reaches the bound does
// the status fall back to Disputed (disagreeing hashes) or Rejected
// (every answering source executed a rejection).
func ResolveWriteBroadcast(basis int, floor int, answers []SourceAnswer, faults []error) (WriteDecision, error) {
	decision := WriteDecision{Basis: basis, Required: WriteQuorum(basis, floor)}
	decision.Faults = append(decision.Faults, faults...)

	clusters := make(map[string][]SourceRef)
	var rejected []SourceAnswer
	var silent []SourceRef
	for _, answer := range answers {
		switch {
		case answer.Hash != "":
			clusters[answer.Hash] = append(clusters[answer.Hash], answer.Source)
		case answer.Rejection != nil:
			rejected = append(rejected, answer)
		default:
			silent = append(silent, answer.Source)
		}
	}
	decision.Accepted = nil
	decision.Rejected = rejected
	decision.Silent = silent

	// Rank clusters by size, tie-broken by hash, so identical inputs produce
	// identical verdicts regardless of arrival order.
	hashes := make([]string, 0, len(clusters))
	for hash := range clusters {
		hashes = append(hashes, hash)
	}
	sort.Slice(hashes, func(i, j int) bool {
		if len(clusters[hashes[i]]) != len(clusters[hashes[j]]) {
			return len(clusters[hashes[i]]) > len(clusters[hashes[j]])
		}
		return hashes[i] < hashes[j]
	})

	anyHash := len(hashes) > 0
	if anyHash {
		winner := hashes[0]
		decision.Hash = winner
		decision.Agreement = len(clusters[winner])
		for _, answer := range answers {
			if answer.Hash == winner {
				decision.Accepted = append(decision.Accepted, answer)
			}
		}
	}
	if anyHash && decision.Agreement >= decision.Required {
		decision.Status = WriteAccepted
		return decision, nil
	}
	if anyHash && decision.Agreement > 0 {
		decision.Status = WriteDisputed
		return decision, fmt.Errorf("write: hash agreement %d below required %d: %w",
			decision.Agreement, decision.Required, ErrConflict)
	}
	// No hash at all: every answer was a rejection or silence.
	if len(rejected) > 0 {
		decision.Status = WriteRejected
		return decision, fmt.Errorf("write: all answering sources rejected: %w", ErrNoValidResponse)
	}
	decision.Status = WriteUnreachable
	return decision, fmt.Errorf("write: no source answered: %w", ErrNoValidResponse)
}
