package ethguard

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"
	gethrpc "github.com/ethereum/go-ethereum/rpc"

	"ethguard/pool"
	"ethguard/rpc"
	"ethguard/rpc/methods"
)

// Client's collectTimeout is unexported in client.go; the write pipeline
// reads it through the constructor-supplied field directly.

// WriteResult is the broadcast phase outcome of SendRawTransaction. The
// receipt phase (TransactionReceipt on the winning hash) is a separate read
// and stays out of this struct: nothing here claims the transaction has been
// mined, only that the fleet's pools accepted propagation.
type WriteResult struct {
	// Decision is the relative-trust classification of the broadcast.
	Decision rpc.WriteDecision
}

// Accepted reports whether the broadcast reached its support bound.
func (w WriteResult) Accepted() bool { return w.Decision.Status == rpc.WriteAccepted }

// Hash is the winning transaction hash; empty when no hash cluster formed.
func (w WriteResult) Hash() common.Hash { return common.HexToHash(w.Decision.Hash) }

// writeAnswerClassified converts one broadcast emission into a classified
// SourceAnswer. A hash-shaped value is an acceptance candidate; a structured
// protocol error body is always a rejection answer kept as evidence. No
// message-text classification ("already known" etc.) happens here.
func writeAnswerClassified(source rpc.SourceRef, value json.RawMessage, err error) rpc.SourceAnswer {
	if err == nil && len(value) > 0 {
		var hashHex string
		if json.Unmarshal(value, &hashHex) == nil && common.IsHexHash(hashHex) {
			return rpc.SourceAnswer{Source: source, Hash: strings.ToLower(hashHex)}
		}
		return rpc.SourceAnswer{Source: source}
	}
	var protocolErr gethrpc.Error
	if !errors.As(err, &protocolErr) {
		return rpc.SourceAnswer{Source: source}
	}
	rejection := &rpc.RPCError{Code: protocolErr.ErrorCode(), Message: protocolErr.Error()}
	var dataErr gethrpc.DataError
	if errors.As(err, &dataErr) {
		if data := dataErr.ErrorData(); data != nil {
			if raw, marshalErr := json.Marshal(data); marshalErr == nil {
				rejection.Data = raw
			}
		}
	}
	return rpc.SourceAnswer{Source: source, Rejection: rejection}
}

// SendRawTransaction broadcasts signed transaction bytes to the full voting
// basis and adjudicates the outcome as relative trust (decision-map #4, the
// write-path prototype).
//
// Semantics:
//
//   - The request rides pool.Broadcast: every requestable source is contacted
//     in one wave, with no tip-freshness gate and no first-success
//     short-circuit. The same raw bytes re-sent are idempotent, so this is
//     safe to retry at the caller level too.
//   - The voting basis is captured before any request (decision 0025): the
//     membership snapshot, the corroboration mode, and the support floor.
//     Forwarded mode broadcasts to the single member and reports WriteAccepted
//     with Basis=1 without claiming corroboration.
//   - A quorum-reaching hash cluster is WriteAccepted; minority dissent and
//     rejections are evidence, not failure. Rejection bodies never join the
//     hash cluster, even when their text suggests the transaction is already
//     in that source's pool; callers that need idempotent-retry semantics
//     should re-read via TransactionReceipt on the winning hash.
//   - The returned error wraps exactly one classified sentinel
//     (ErrConflict for disputed, ErrNoValidResponse for rejected/unreachable,
//     pool sentinels for fan-out impossibility), so errors.Is keeps working.
//
// Receipt waiting is deliberately separate: follow up with TransactionReceipt
// on the winning hash once the caller's confirmation policy decides the
// window.
func (c *Client) SendRawTransaction(ctx context.Context, rawTx []byte) (WriteResult, error) {
	if len(rawTx) == 0 {
		return WriteResult{}, fmt.Errorf("%w: empty raw transaction", rpc.ErrInvalidRequest)
	}
	// Verify the signed payload locally before any wire contact: the hash the
	// fleet must agree on is derived here, so a fabricated hash answer can
	// never win the cluster.
	tx := new(types.Transaction)
	if err := tx.UnmarshalBinary(rawTx); err != nil {
		return WriteResult{}, fmt.Errorf("%w: decode signed transaction: %v", rpc.ErrInvalidRequest, err)
	}
	hash := strings.ToLower(tx.Hash().Hex())

	snap := c.corrob.Snapshot()
	basis := c.pool.Len()
	params := []any{hexutil.Encode(rawTx)}

	var emissions []pool.Emission
	var collectErr error
	if snap.Mode == rpc.CorroborationForwarded {
		emissions, collectErr = c.pool.Forward(ctx, methods.MethodSendRawTransaction, params, pool.CollectOptions{Timeout: c.collectTimeoutValue()})
	} else {
		emissions, collectErr = c.pool.Broadcast(ctx, methods.MethodSendRawTransaction, params, pool.WithBroadcastTimeout(c.collectTimeoutValue()))
	}
	if collectErr != nil && !hasBroadcastAnswer(emissions) {
		return WriteResult{Decision: rpc.WriteDecision{Status: rpc.WriteUnreachable, Basis: basis}},
			fmt.Errorf("write broadcast: %w", collectErr)
	}

	answers := make([]rpc.SourceAnswer, 0, len(emissions))
	var faults []error
	for _, emission := range emissions {
		source := rpc.SourceRef{}
		if emission.Src != nil {
			source = rpc.SourceRef{Name: emission.Src.Name(), URL: emission.Src.URL(), ID: emission.Src.ID()}
		}
		answer := writeAnswerClassified(source, emission.Val, emission.Err)
		if emission.Attempted && answer.Hash == "" && answer.Rejection == nil && emission.Err != nil {
			faults = append(faults, fmt.Errorf("source %q: %w", source.Name, emission.Err))
		}
		answers = append(answers, answer)
	}

	// In forwarded mode the single answer is locally validated only: the
	// hash must match the sent bytes, and no corroboration claim is made.
	if snap.Mode == rpc.CorroborationForwarded {
		for _, answer := range answers {
			if answer.Hash != "" {
				if answer.Hash != hash {
					return WriteResult{Decision: rpc.WriteDecision{Status: rpc.WriteDisputed, Basis: basis, Hash: answer.Hash}},
						fmt.Errorf("write: source hash %s does not match signed payload %s: %w", answer.Hash, hash, rpc.ErrConflict)
				}
				return WriteResult{Decision: rpc.WriteDecision{Status: rpc.WriteAccepted, Hash: answer.Hash, Agreement: 1, Basis: basis, Required: 1}}, nil
			}
		}
		return WriteResult{Decision: rpc.WriteDecision{Status: rpc.WriteUnreachable, Basis: basis}},
			fmt.Errorf("write: forwarded source did not accept: %w", rpc.ErrNoValidResponse)
	}

	decision, err := rpc.ResolveWriteBroadcast(basis, snap.Floor, answers, faults)
	// Winning-hash integrity: the cluster's hash must equal the sent bytes'
	// locally derived hash. A fleet that coherently reports a different hash
	// is not accepting this transaction — fail closed rather than binding a
	// foreign hash for receipt waiting.
	if decision.Hash != "" && decision.Hash != hash {
		if decision.Status == rpc.WriteAccepted {
			decision.Status = rpc.WriteDisputed
			err = fmt.Errorf("write: fleet hash %s does not match signed payload %s: %w",
				decision.Hash, hash, rpc.ErrConflict)
		} else {
			err = fmt.Errorf("write: reported hash %s does not match signed payload %s: %w",
				decision.Hash, hash, err)
		}
	}
	return WriteResult{Decision: decision}, err
}

// hasBroadcastAnswer reports whether any emission carried a usable wire
// outcome (a value or a structured protocol answer).
func hasBroadcastAnswer(emissions []pool.Emission) bool {
	for _, emission := range emissions {
		if emission.Val != nil {
			return true
		}
		var protocolErr gethrpc.Error
		if errors.As(emission.Err, &protocolErr) {
			return true
		}
	}
	return false
}

// collectTimeoutValue returns the client's per-call collection bound for the
// write fan-out. It reads the same configuration the read aggregator uses.
func (c *Client) collectTimeoutValue() time.Duration { return c.collectTimeout }

// SendTransaction mirrors go-ethereum's ethclient.SendTransaction: encode the
// signed transaction and broadcast its raw bytes.
func (c *Client) SendTransaction(ctx context.Context, tx *types.Transaction) (WriteResult, error) {
	raw, err := tx.MarshalBinary()
	if err != nil {
		return WriteResult{}, fmt.Errorf("%w: encode transaction: %v", rpc.ErrInvalidRequest, err)
	}
	return c.SendRawTransaction(ctx, raw)
}
