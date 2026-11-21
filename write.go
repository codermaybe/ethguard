package ethguard

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	ethereum "github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"

	"github.com/codermaybe/ethguard/ethapi"
	"github.com/codermaybe/ethguard/internal/uncertain"
	"github.com/codermaybe/ethguard/pool"
	"github.com/codermaybe/ethguard/rpc"
	"github.com/codermaybe/ethguard/rpc/methods"
)

// geth v1.17.10 uses a 20-second default for eth_sendRawTransactionSync when
// the caller omits a timeout. Keep the local polling path bounded to the same
// compatibility default while still honoring an explicit positive timeout.
const defaultTransactionReceiptTimeout = 20 * time.Second

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
	var protocolErr rpc.ProtocolError
	if !errors.As(err, &protocolErr) {
		return rpc.SourceAnswer{Source: source}
	}
	rejection := &rpc.RPCError{Code: protocolErr.ErrorCode(), Message: protocolErr.Error()}
	var preserved rpc.ErrorDataPresence
	if errors.As(err, &preserved) {
		raw, present := preserved.ErrorDataJSON()
		if !present {
			rejection.DataPresence = rpc.ErrorDataPresenceMissing
			return rpc.SourceAnswer{Source: source, Rejection: rejection}
		}
		rejection.Data = append(json.RawMessage(nil), raw...)
		if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			rejection.DataPresence = rpc.ErrorDataPresenceNull
		} else {
			rejection.DataPresence = rpc.ErrorDataPresenceValue
		}
		return rpc.SourceAnswer{Source: source, Rejection: rejection}
	}
	var dataErr rpc.DataError
	if errors.As(err, &dataErr) {
		if data := dataErr.ErrorData(); data != nil {
			if raw, marshalErr := json.Marshal(data); marshalErr == nil {
				rejection.Data = raw
				rejection.DataPresence = rpc.ErrorDataPresenceValue
			}
		}
	}
	return rpc.SourceAnswer{Source: source, Rejection: rejection}
}

// SendRawTransaction broadcasts signed transaction bytes to the full voting
// basis and adjudicates the outcome as relative trust (decision-map #4, the
// write-path prototype). The completion result carries WriteDecision — the
// CallResult.Err and the trailing error are the same conclusion.
//
// Semantics:
//
//   - The request rides pool.Broadcast: every requestable source is contacted
//     in one wave, with no tip-freshness gate and no first-success
//     short-circuit. The same raw bytes re-sent are idempotent, so this is
//     safe to retry at the caller level too.
//   - The voting basis is captured before any request (decision 0025): the
//     membership snapshot, the corroboration mode, and the support floor.
//     Forwarded mode tries one member at a time and reports a matching
//     WriteAccepted with Basis=1 without claiming corroboration.
//   - A quorum-reaching hash cluster is WriteAccepted; minority dissent and
//     rejections are evidence, not failure. Rejection bodies never join the
//     hash cluster, even when their text suggests the transaction is already
//     in that source's pool; callers that need idempotent-retry semantics
//     should re-read via TransactionReceipt on the winning hash.
//     WriteRejected describes answering sources only; silent sources may
//     already have received the transaction. Neither a rejection nor a
//     transport fault proves non-submission or transaction invalidity.
//   - The returned error wraps exactly one classified sentinel
//     (ErrConflict for disputed, ErrNoValidResponse for rejected/unreachable,
//     pool sentinels for fan-out impossibility), so errors.Is keeps working.
//
// Receipt waiting is deliberately separate: follow up with TransactionReceipt
// on the winning hash once the caller's confirmation policy decides the
// window.
func (c *Client) SendRawTransaction(ctx context.Context, rawTx []byte) (CallResult, error) {
	return writeOperation(c, ctx, false, func(opCtx context.Context) (CallResult, error) {
		result, err := c.sendRawTransaction(opCtx, rawTx)
		return result, err
	})
}

// sendRawTransaction is the shared write implementation both public entries
// run. It owns the local validation, the broadcast, and the adjudication.
func (c *Client) sendRawTransaction(ctx context.Context, rawTx []byte) (CallResult, error) {
	// All local checks, hashing and wire encoding consume owned bytes, including
	// when the caller supplied raw bytes rather than a BinaryTransaction.
	rawTx = bytes.Clone(rawTx)
	if len(rawTx) == 0 {
		return CallResult{}, fmt.Errorf("%w: empty raw transaction", rpc.ErrInvalidRequest)
	}
	// Verify the signed payload locally before any wire contact: the hash the
	// fleet must agree on is derived here, so a fabricated hash answer can
	// never win the cluster.
	tx := new(types.Transaction)
	if err := tx.UnmarshalBinary(rawTx); err != nil {
		return CallResult{}, fmt.Errorf("%w: decode signed transaction: %v", rpc.ErrInvalidRequest, err)
	}
	// Binary decoding checks representation, not signature validity. Recover
	// with the transaction's own chain ID before contacting any pool member;
	// this does not enforce provider chain ID or transaction executability.
	if err := validateWriteSignature(tx); err != nil {
		return CallResult{}, fmt.Errorf("%w: invalid transaction signature: %v", rpc.ErrInvalidRequest, err)
	}
	hash := strings.ToLower(tx.Hash().Hex())

	snap := c.corrob.Snapshot()
	members := c.pool.VotingMembers()
	basis := len(members)
	// Forwarded mode is an intentional single-source path even when the pool
	// contains more than one configured fallback member. The write decision is
	// therefore adjudicated with a one-source requirement and makes no
	// corroboration claim.
	if snap.Mode == rpc.CorroborationForwarded && basis > 0 {
		basis = 1
	}
	params := []any{hexutil.Encode(rawTx)}

	var emissions []pool.Emission
	var collectErr error
	if snap.Mode == rpc.CorroborationForwarded {
		emissions, collectErr = c.pool.Forward(ctx, methods.MethodSendRawTransaction, params, pool.CollectOptions{Sources: members, Timeout: c.collectTimeoutValue()})
	} else {
		emissions, collectErr = c.pool.Broadcast(ctx, methods.MethodSendRawTransaction, params,
			pool.WithBroadcastTimeout(c.collectTimeoutValue()), pool.WithBroadcastSources(members))
	}

	answers := make([]rpc.SourceAnswer, 0, len(emissions))
	var faults []error
	collector := uncertain.CollectorFromContext(ctx)
	for _, emission := range emissions {
		source := rpc.SourceRef{}
		if emission.Src != nil {
			source = rpc.SourceRef{Name: emission.Src.Name(), URL: emission.Src.URL(), ID: emission.Src.ID()}
		}
		if collector != nil {
			errText := ""
			if emission.Err != nil {
				errText = emission.Err.Error()
			}
			collector.Record("broadcast", methods.MethodSendRawTransaction, source, emission.Val, errText)
		}
		answer := writeAnswerClassified(source, emission.Val, emission.Err)
		if emission.Attempted && answer.Hash == "" && answer.Rejection == nil && emission.Err != nil {
			faults = append(faults, fmt.Errorf("source %q: %w", source.Name, emission.Err))
		}
		answers = append(answers, answer)
	}
	if collector != nil && collectErr != nil {
		collector.Record("broadcast", methods.MethodSendRawTransaction, rpc.SourceRef{}, nil, collectErr.Error())
	}
	if collectErr != nil && !hasBroadcastAnswer(emissions) {
		decision, _ := rpc.ResolveWriteBroadcast(basis, snap.Floor, answers, faults)
		decision.Status = rpc.WriteUnreachable
		return CallResult{WriteDecision: &decision}, fmt.Errorf("write broadcast: %w", collectErr)
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
	return CallResult{WriteDecision: &decision}, err
}

// hasBroadcastAnswer reports whether any emission carried a usable wire
// outcome (a value or a structured protocol answer).
func hasBroadcastAnswer(emissions []pool.Emission) bool {
	for _, emission := range emissions {
		if emission.Val != nil {
			return true
		}
		var protocolErr rpc.ProtocolError
		if errors.As(emission.Err, &protocolErr) {
			return true
		}
	}
	return false
}

// collectTimeoutValue returns the client's per-call collection bound for the
// write fan-out. It reads the same configuration the read aggregator uses.
func (c *Client) collectTimeoutValue() time.Duration { return c.collectTimeout }

// validateWriteSignature checks recoverability without introducing a configured
// chain or execution policy. The pinned geth modern-signer constructor requires
// a positive chain ID, so typed chain ID zero uses the same signature hash and
// crypto primitives directly rather than adding a new chain-ID restriction.
func validateWriteSignature(tx *types.Transaction) error {
	chainID := tx.ChainId()
	if tx.Type() == types.LegacyTxType {
		_, err := types.Sender(types.NewEIP155Signer(chainID), tx)
		return err
	}
	if chainID.Sign() > 0 {
		_, err := types.Sender(types.LatestSignerForChainID(chainID), tx)
		return err
	}
	v, r, s := tx.RawSignatureValues()
	if v.BitLen() > 1 || !crypto.ValidateSignatureValues(byte(v.Uint64()), r, s, true) {
		return types.ErrInvalidSig
	}
	signature := make([]byte, crypto.SignatureLength)
	r.FillBytes(signature[:32])
	s.FillBytes(signature[32:64])
	signature[64] = byte(v.Uint64())
	// EIP155Signer's Hash delegates to the transaction's native signing hash;
	// it does not restrict the payload to legacy transactions.
	signingHash := types.NewEIP155Signer(chainID).Hash(tx)
	_, err := crypto.SigToPub(signingHash[:], signature)
	return err
}

// SendTransaction mirrors go-ethereum's ethclient.SendTransaction: encode the
// signed transaction once through the BinaryTransaction contract (any value
// with MarshalBinary — *types.Transaction still qualifies), snapshot the
// bytes, and broadcast them. Encoding or local payload failures are local
// ErrInvalidRequest rejections; acceptance stays a propagation outcome, not a
// mining or executability claim.
func (c *Client) SendTransaction(ctx context.Context, tx BinaryTransaction) (CallResult, error) {
	return writeOperation(c, ctx, false, func(opCtx context.Context) (CallResult, error) {
		return c.sendTransactionExec(opCtx, tx)
	})
}

// sendTransactionExec freezes the typed transaction once for both facades;
// every later validation consumes the same snapshot bytes.
func (c *Client) sendTransactionExec(ctx context.Context, tx BinaryTransaction) (CallResult, error) {
	raw, err := snapshotBinaryTransaction(tx)
	if err != nil {
		return CallResult{}, err
	}
	return c.sendRawTransaction(ctx, raw)
}

// sendRawTransactionSyncExec broadcasts a transaction through the normal
// write quorum and then polls the shared receipt execution until it is mined
// or the caller's optional timeout/context ends. The write decision remains
// attached to the returned CallResult; receipt absence is an expected polling
// state and is never treated as a successful receipt.
func (c *Client) sendRawTransactionSyncExec(ctx context.Context, rawTx []byte, timeout *time.Duration) (*ethapi.Receipt, CallResult, error) {
	if timeout != nil && *timeout < 0 {
		return nil, CallResult{}, fmt.Errorf("%w: negative receipt timeout", rpc.ErrInvalidRequest)
	}
	writeResult, err := c.sendRawTransaction(ctx, rawTx)
	if err != nil {
		return nil, writeResult, err
	}
	if writeResult.WriteDecision == nil || writeResult.WriteDecision.Status != rpc.WriteAccepted || writeResult.WriteDecision.Hash == "" {
		return nil, writeResult, fmt.Errorf("%w: transaction was not accepted for synchronous receipt", rpc.ErrNoValidResponse)
	}
	effectiveTimeout := defaultTransactionReceiptTimeout
	if timeout != nil && *timeout > 0 {
		effectiveTimeout = *timeout
	}
	waitCtx, cancel := context.WithTimeout(ctx, effectiveTimeout)
	defer cancel()
	hash := common.HexToHash(writeResult.WriteDecision.Hash)
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		receipt, _, receiptErr := c.transactionReceiptExec(waitCtx, ethapi.Hash(hash.Hex()))
		if receiptErr == nil {
			return receipt, writeResult, nil
		}
		if !errors.Is(receiptErr, ethereum.NotFound) && !errors.Is(receiptErr, rpc.ErrConflict) {
			return nil, writeResult, receiptErr
		}
		select {
		case <-waitCtx.Done():
			return nil, writeResult, waitCtx.Err()
		case <-ticker.C:
		}
	}
}

// SendTransactionSync broadcasts a signed transaction and waits for its
// receipt, preserving the normal write decision and lifecycle semantics. The
// input is the BinaryTransaction contract: the transaction is encoded once,
// snapshotted, and every later validation consumes the same bytes.
func (c *Client) SendTransactionSync(ctx context.Context, tx BinaryTransaction, timeout *time.Duration) (*ethapi.Receipt, CallResult, error) {
	return operation(c, ctx, false, func(opCtx context.Context) (*ethapi.Receipt, CallResult, error) {
		return c.sendTransactionSyncExec(opCtx, tx, timeout)
	})
}

// sendTransactionSyncExec freezes the typed transaction once for both
// facades before the broadcast-and-poll execution.
func (c *Client) sendTransactionSyncExec(ctx context.Context, tx BinaryTransaction, timeout *time.Duration) (*ethapi.Receipt, CallResult, error) {
	raw, err := snapshotBinaryTransaction(tx)
	if err != nil {
		return nil, CallResult{}, err
	}
	return c.sendRawTransactionSyncExec(ctx, raw, timeout)
}

// SendRawTransactionSync broadcasts raw signed bytes and waits for the
// corresponding receipt.
func (c *Client) SendRawTransactionSync(ctx context.Context, rawTx []byte, timeout *time.Duration) (*ethapi.Receipt, CallResult, error) {
	return operation(c, ctx, false, func(opCtx context.Context) (*ethapi.Receipt, CallResult, error) {
		return c.sendRawTransactionSyncExec(opCtx, rawTx, timeout)
	})
}
