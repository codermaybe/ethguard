package ethguard

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strings"

	ethereum "github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"
	gethrpc "github.com/ethereum/go-ethereum/rpc"

	"github.com/codermaybe/ethguard/internal/jsonsafe"
	"github.com/codermaybe/ethguard/internal/typed"
	"github.com/codermaybe/ethguard/rpc"
	"github.com/codermaybe/ethguard/rpc/methods"
)

// callTyped is the typed facade's aggregate entry. The corroboration
// requirement rides the invocation (aggregator.AggregateStrict), never the
// context and never the request; Call stays the generic decision entry, and
// the two never share a requirement carrier. Forwarded mode and the
// pending-local route keep their documented exemptions.
func (c *Client) callTyped(ctx context.Context, method string, params ...any) (rpc.Decision, error) {
	return c.aggregator.AggregateStrict(ctx, rpc.Request{Method: method, Params: params})
}

// typedInto aggregates one gated request and decodes the resolved value into
// T. The returned pointer is nil whenever the error is non-nil.
func typedInto[T any](ctx context.Context, c *Client, method string, params ...any) (*T, error) {
	decision, err := c.callTyped(ctx, method, params...)
	if err != nil {
		return nil, err
	}
	var out T
	if err := jsonsafe.Decode(decision.Value, &out); err != nil {
		return nil, fmt.Errorf("%w: decode %s: %w", rpc.ErrMalformedResponse, method, err)
	}
	return &out, nil
}

// decodedQuantity reduces one decision's canonical quantity spelling into a
// big.Int through the owning method's normalizer.
func (c *Client) decodedQuantity(method string, decision rpc.Decision) (*big.Int, error) {
	canonical, err := c.Normalize(method, decision.Value)
	if err != nil {
		return nil, fmt.Errorf("ethguard: decode %s: %w", method, err)
	}
	value, ok := new(big.Int).SetString(canonical, 10)
	if !ok {
		return nil, fmt.Errorf("ethguard: invalid %s %q", method, canonical)
	}
	return value, nil
}

// ChainID returns the fleet-corroborated chain ID.
func (c *Client) ChainID(ctx context.Context) (*big.Int, error) {
	decision, err := c.callTyped(ctx, methods.MethodChainID)
	if err != nil {
		return nil, err
	}
	return c.decodedQuantity(methods.MethodChainID, decision)
}

// NetworkID returns the corroborated legacy chain identity (net_version
// spells in decimal what eth_chainId spells in hex).
func (c *Client) NetworkID(ctx context.Context) (*big.Int, error) {
	decision, err := c.callTyped(ctx, methods.MethodNetVersion)
	if err != nil {
		return nil, err
	}
	return c.decodedQuantity(methods.MethodNetVersion, decision)
}

// BalanceAt returns the corroborated balance of account at blockNumber; nil
// means the currently corroborated reference behind "latest".
func (c *Client) BalanceAt(ctx context.Context, account common.Address, blockNumber *big.Int) (*big.Int, error) {
	blockArg, err := checkedBlockNumArg(blockNumber)
	if err != nil {
		return nil, err
	}
	value, err := typedInto[hexutil.Big](ctx, c, methods.MethodGetBalance, account.Hex(), blockArg)
	if err != nil {
		return nil, err
	}
	return (*big.Int)(value), nil
}

// NonceAt returns the corroborated account nonce of account at blockNumber;
// nil pins the moving tag to the currently corroborated reference.
func (c *Client) NonceAt(ctx context.Context, account common.Address, blockNumber *big.Int) (uint64, error) {
	blockArg, err := checkedBlockNumArg(blockNumber)
	if err != nil {
		return 0, err
	}
	nonce, err := typedInto[hexutil.Uint64](ctx, c, methods.MethodGetTransactionCount, account.Hex(), blockArg)
	if err != nil {
		return 0, err
	}
	return uint64(*nonce), nil
}

// StorageAt returns the corroborated value of key in account's storage at
// blockNumber; nil pins the moving tag to the currently corroborated reference.
func (c *Client) StorageAt(ctx context.Context, account common.Address, key common.Hash, blockNumber *big.Int) ([]byte, error) {
	blockArg, err := checkedBlockNumArg(blockNumber)
	if err != nil {
		return nil, err
	}
	value, err := typedInto[hexutil.Bytes](ctx, c, methods.MethodGetStorageAt, account.Hex(), key.Hex(), blockArg)
	if err != nil {
		return nil, err
	}
	return *value, nil
}

// CodeAt returns the corroborated contract code of account at blockNumber; nil
// pins the moving tag to the currently corroborated reference. An externally
// owned account answers empty code, not an error.
func (c *Client) CodeAt(ctx context.Context, account common.Address, blockNumber *big.Int) ([]byte, error) {
	blockArg, err := checkedBlockNumArg(blockNumber)
	if err != nil {
		return nil, err
	}
	code, err := typedInto[hexutil.Bytes](ctx, c, methods.MethodGetCode, account.Hex(), blockArg)
	if err != nil {
		return nil, err
	}
	return *code, nil
}

// BlockByHash returns the corroborated block with the given hash.
func (c *Client) BlockByHash(ctx context.Context, hash common.Hash) (*types.Block, error) {
	return c.blockAt(ctx, hash.Hex())
}

// BlockByNumber returns the corroborated block at number. nil and explicit
// moving tags (-2 latest, -3 finalized, -4 safe) pin that tag's own reference
// first and fetch by hash. Explicit heights, including earliest (-5 as 0x0),
// fetch directly. Pending (-1) is rejected locally: there is no pending-block
// typed projection, and -1 is not rewritten into an anchored or ordinary block.
func (c *Client) BlockByNumber(ctx context.Context, number *big.Int) (*types.Block, error) {
	if number != nil && number.Sign() >= 0 && !number.IsUint64() {
		return nil, fmt.Errorf("block number exceeds supported height range: %w", ErrInvalidRequest)
	}
	if number != nil && number.Sign() < 0 {
		return c.blockByNegativeNumber(ctx, number)
	}
	if number == nil {
		ref, err := c.AnchorBlockHash(ctx)
		if err != nil {
			return nil, err
		}
		return c.blockAt(ctx, ref)
	}
	blockArg, err := checkedBlockNumArg(number)
	if err != nil {
		return nil, err
	}
	return c.blockAt(ctx, blockArg)
}

// blockByNegativeNumber resolves Geth's negative block numbers without
// collapsing them into one tag. -2/-3/-4 establish their own level's
// reference (never downgraded, never fetched as a moving tag) and read by
// hash. -1 is a local rejection: pending has no typed block projection, so
// it is not anchored and not sent as a business request or as "-0x1".
// -5 is earliest, an explicit height 0x0. Anything else — including values
// outside int64 range — fails locally before any upstream contact.
func (c *Client) blockByNegativeNumber(ctx context.Context, number *big.Int) (*types.Block, error) {
	if !number.IsInt64() {
		return nil, fmt.Errorf("ethguard: unsupported block number %s: %w", number.String(), ErrInvalidRequest)
	}
	switch number.Int64() {
	case -1:
		return nil, fmt.Errorf("ethguard: pending block typed projection is not supported: %w", ErrInvalidRequest)
	case -2, -3, -4:
		tags := map[int64]string{-2: "latest", -3: "finalized", -4: "safe"}
		tag := tags[number.Int64()]
		ref, err := c.AnchorBlockHashForTag(ctx, tag)
		if err != nil {
			return nil, err
		}
		return c.blockAt(ctx, ref)
	case -5:
		return c.blockAt(ctx, "0x0")
	default:
		return nil, fmt.Errorf("ethguard: unsupported block number %s: %w", number.String(), ErrInvalidRequest)
	}
}

// BlockNumber returns the height of the currently corroborated block reference.
// It is a derivation over corroborated primitives, not an independent read, and
// the selection reasoning is stated here rather than left implicit:
//
//   - Sources sit at different heads as a matter of course. A fleet fan-out
//     of eth_blockNumber would turn routine replication lag into conflict;
//     divergence here is the common case, not an anomaly, so fail-closed
//     comparison is the wrong tool for this read.
//   - The corroborated reference behind moving-tag reads already carries the
//     fleet's agreement. Its height is the only height with a corroboration
//     claim behind it — any other answer would be one provider's tip.
//   - eth_blockNumber therefore stays unregistered on purpose: a raw
//     fan-out of it falls through to transparent Forwarded, never a
//     pretended Corroborated on a moving value.
//
// This is the source-agreement-override shape proxyd popularized, expressed as
// derivation. Callers wanting bounded staleness should configure the
// anchoring presets (WithFinalizedAnchoring) and, optionally, its cache.
func (c *Client) BlockNumber(ctx context.Context) (uint64, error) {
	ref, err := c.AnchorBlockHash(ctx)
	if err != nil {
		return 0, err
	}
	// The height lookup rides eth_getBlockByHash even though a "height of
	// this hash" read feels like eth_getBlockByNumber: the first live soak
	// run caught public endpoints rejecting a 32-byte hash in the number
	// position ("hex number > 64 bits"), so hash lookups use the
	// hash-addressed method, full stop. blockAt applies the same dispatch and
	// binds the answer to the requested reference.
	block, err := c.blockAt(ctx, ref)
	if err != nil {
		return 0, err
	}
	return block.NumberU64(), nil
}

// blockAt fetches and strictly compares one block identified explicitly by
// hash or height — never by a moving tag. Which method the reference rides
// is a wire-portability decision: the first live soak run caught public
// endpoints rejecting a 32-byte hash in eth_getBlockByNumber's number
// position ("hex number > 64 bits"), so hash-looking references take the
// hash-addressed method and everything else the number-addressed one.
func (c *Client) blockAt(ctx context.Context, ref string) (*types.Block, error) {
	method := methods.MethodGetBlockByNumber
	if len(ref) == 2+2*common.HashLength && strings.HasPrefix(ref, "0x") {
		method = methods.MethodGetBlockByHash
	}
	decision, err := c.callTyped(ctx, method, ref, true)
	if err != nil {
		return nil, err
	}
	block, err := typed.DecodeBlockWithEvidence(decision.Value, decision.Provenance)
	if err != nil {
		return nil, err
	}
	if len(ref) == 2+2*common.HashLength && strings.HasPrefix(ref, "0x") {
		if block.Hash() != common.HexToHash(ref) {
			return nil, fmt.Errorf("%w: block hash %s does not match requested hash %s",
				rpc.ErrMalformedResponse, block.Hash(), ref)
		}
		return block, nil
	}
	number, ok := new(big.Int).SetString(strings.TrimPrefix(ref, "0x"), 16)
	if !ok || block.Number().Cmp(number) != 0 {
		return nil, fmt.Errorf("%w: block height %d does not match requested reference %s",
			rpc.ErrMalformedResponse, block.NumberU64(), ref)
	}
	return block, nil
}

// BlockReceipts returns the fleet-corroborated receipts of one block. Numbers
// and moving tags are pinned to the corroborated reference before fan-out;
// hashes address immutable content directly. Corroborated absence of the block
// maps to ethereum.NotFound.
//
// Request-answer binding closes the typed boundary for every reference
// form: a hash request binds every receipt's BlockHash to that hash, an
// explicit number binds every receipt's BlockNumber to that height, and a
// moving tag binds every receipt's BlockHash to the reference the policy
// actually pinned (decision.BlockRef — no second anchor call). A coherent
// fleet returning another block's legitimate receipts therefore fails as
// malformed instead of passing. This binding is per-response integrity,
// not a completeness proof: an empty array stays a legal answer for an
// empty block, and nothing here proves the fleet reported every receipt
// of the block.
func (c *Client) BlockReceipts(ctx context.Context, blockNrOrHash gethrpc.BlockNumberOrHash) ([]*types.Receipt, error) {
	refText, err := blockRefArg(blockNrOrHash)
	if err != nil {
		return nil, err
	}
	var ref any = refText
	var canonicalHash *common.Hash
	if blockNrOrHash.RequireCanonical {
		// Preserve go-ethereum's requireCanonical bit on the wire. A moving
		// tag cannot be made canonical without first establishing a tag-aware
		// reference; reject it explicitly rather than silently dropping the
		// caller's constraint.
		if _, ok := blockNrOrHash.Number(); ok {
			number, _ := blockNrOrHash.Number()
			if number == gethrpc.PendingBlockNumber || number == gethrpc.LatestBlockNumber || number == gethrpc.SafeBlockNumber || number == gethrpc.FinalizedBlockNumber {
				return nil, fmt.Errorf("ethguard: requireCanonical with moving block tag %s is unsupported: %w", number, ErrInvalidRequest)
			}
			ref = map[string]any{"blockNumber": refText, "requireCanonical": true}
		} else if hash, ok := blockNrOrHash.Hash(); ok {
			ref = map[string]any{"blockHash": hash, "requireCanonical": true}
			canonicalHash = &hash
		}
	}
	decision, err := c.callTyped(ctx, methods.MethodGetBlockReceipts, ref)
	if err != nil {
		return nil, err
	}
	receipts, err := typed.DecodeReceipts(decision.Value)
	if err != nil {
		return nil, err
	}
	if hash, ok := blockNrOrHash.Hash(); ok {
		return bindReceiptsToHash(receipts, hash)
	}
	if canonicalHash != nil {
		return bindReceiptsToHash(receipts, *canonicalHash)
	}
	if number, ok := blockNrOrHash.Number(); ok && number >= 0 {
		requested := big.NewInt(number.Int64())
		for i, receipt := range receipts {
			if receipt.BlockNumber == nil || receipt.BlockNumber.Cmp(requested) != 0 {
				return nil, errors.Join(
					ErrMalformedResponse,
					ErrAnchorMismatch,
					fmt.Errorf("receipt %d height %v does not match requested number %d", i, receipt.BlockNumber, number.Int64()),
				)
			}
		}
		return receipts, nil
	}
	// Moving tag: the policy rewrote it to the corroborated reference, and
	// decision.BlockRef carries that pin. Bind against the pin, not against
	// a fresh anchor call.
	if decision.BlockRef == "" {
		return nil, errors.Join(ErrMalformedResponse, ErrAnchorMismatch,
			errors.New("pinned receipts carry no block reference"))
	}
	return bindReceiptsToHash(receipts, common.HexToHash(decision.BlockRef))
}

// bindReceiptsToHash applies the per-receipt block-hash binding shared by
// hash-addressed and tag-pinned requests.
func bindReceiptsToHash(receipts []*types.Receipt, hash common.Hash) ([]*types.Receipt, error) {
	for i, receipt := range receipts {
		if receipt.BlockHash != hash {
			return nil, errors.Join(
				ErrMalformedResponse,
				ErrAnchorMismatch,
				fmt.Errorf("receipt %d block hash %s does not match requested hash %s", i, receipt.BlockHash, hash),
			)
		}
	}
	return receipts, nil
}

// blockRefArg converts go-ethereum's BlockNumberOrHash into the plain
// string reference the anchored policies rewrite. The object wire form is
// deliberately avoided: an object-shaped moving tag would slip past the
// rewrite unrewritten and turn the read into inevitable dissent.
func blockRefArg(blockNrOrHash gethrpc.BlockNumberOrHash) (string, error) {
	if hash, ok := blockNrOrHash.Hash(); ok {
		return hash.Hex(), nil
	}
	if number, ok := blockNrOrHash.Number(); ok {
		switch number {
		case gethrpc.PendingBlockNumber:
			return "pending", nil
		case gethrpc.LatestBlockNumber:
			return "latest", nil
		case gethrpc.SafeBlockNumber:
			return "safe", nil
		case gethrpc.FinalizedBlockNumber:
			return "finalized", nil
		case gethrpc.EarliestBlockNumber:
			return "0x0", nil
		default:
			if number < 0 {
				return "", fmt.Errorf("ethguard: unsupported block number %d: %w", number, ErrInvalidRequest)
			}
			return checkedBlockNumArg(big.NewInt(number.Int64()))
		}
	}
	return "", fmt.Errorf("ethguard: empty block number or hash")
}

// TransactionReceipt returns the fleet-corroborated receipt for the transaction
// hash; ethereum.NotFound when the fleet corroborates the transaction has no
// receipt yet, matching go-ethereum's own client. The hash addresses immutable
// content, so no anchoring or freshness machinery applies.
func (c *Client) TransactionReceipt(ctx context.Context, hash common.Hash) (*types.Receipt, error) {
	decision, err := c.callTyped(ctx, methods.MethodGetTransactionReceipt, hash.Hex())
	if err != nil {
		return nil, err
	}
	receipt, err := typed.DecodeReceipt(decision.Value)
	if err != nil {
		return nil, err
	}
	if receipt.TxHash != hash {
		return nil, fmt.Errorf("%w: receipt transaction hash %s does not match requested hash %s",
			rpc.ErrMalformedResponse, receipt.TxHash, hash)
	}
	return receipt, nil
}

// TransactionByHash returns the fleet-corroborated transaction for the hash and
// whether the fleet saw it as still pending; ethereum.NotFound when the
// fleet corroborates no such transaction exists. Pending transactions are
// ordinary corroborated objects here: while they stay pending, honest sources
// hold identical payloads.
func (c *Client) TransactionByHash(ctx context.Context, hash common.Hash) (*types.Transaction, bool, error) {
	decision, err := c.callTyped(ctx, methods.MethodGetTransactionByHash, hash.Hex())
	if err != nil {
		return nil, false, err
	}
	tx, pending, err := typed.DecodeTransaction(decision.Value)
	if err != nil {
		return nil, false, err
	}
	if tx.Hash() != hash {
		return nil, false, fmt.Errorf("%w: transaction hash %s does not match requested hash %s",
			rpc.ErrMalformedResponse, tx.Hash(), hash)
	}
	return tx, pending, nil
}

// SuggestGasTipCap returns the fleet's lower-median priority-fee estimate —
// a variable result resolved by robust selection, never claimed corroborated.
func (c *Client) SuggestGasTipCap(ctx context.Context) (*big.Int, error) {
	decision, err := c.callTyped(ctx, methods.MethodMaxPriorityFeePerGas)
	if err != nil {
		return nil, err
	}
	return c.decodedQuantity(methods.MethodMaxPriorityFeePerGas, decision)
}

// SuggestGasPrice returns the fleet's lower-median gas price — a variable
// result resolved by robust selection, never claimed corroborated.
func (c *Client) SuggestGasPrice(ctx context.Context) (*big.Int, error) {
	decision, err := c.callTyped(ctx, methods.MethodGasPrice)
	if err != nil {
		return nil, err
	}
	return c.decodedQuantity(methods.MethodGasPrice, decision)
}

// CallContract returns the fleet-corroborated result of executing msg at
// blockNumber; nil pins the moving tag to the currently corroborated reference.
// A fleet-corroborated revert is an observation, not a pipeline failure: it is
// returned as a *RevertError carrying the revert context, while the
// corroboration claim stays on the decision evidence.
func (c *Client) CallContract(ctx context.Context, msg ethereum.CallMsg, blockNumber *big.Int) ([]byte, error) {
	blockArg, err := checkedBlockNumArg(blockNumber)
	if err != nil {
		return nil, err
	}
	decision, err := c.callTyped(ctx, methods.MethodCall, toCallArg(msg), blockArg)
	if err != nil {
		return nil, err
	}
	return decodeCallDecision(decision)
}

// RevertError reports a fleet-corroborated execution revert of an eth_call.
// The corroborated observation is the data-aware comparison key (missing,
// null, empty, hex, or raw JSON). Message is the winning provider's own
// context and is carried separately from Decision.Value. None of the
// classified pipeline sentinels apply.
type RevertError struct {
	message string
	// Data is set only when the provider data is a hex string. Missing and
	// JSON null leave it nil; empty bytes ("0x") are a non-nil empty slice.
	Data []byte
	// DataRaw is the original JSON data value. Structured data stays here
	// and is not forced into hex. Missing data leaves DataRaw empty; JSON
	// null is retained as the bytes `null`.
	DataRaw json.RawMessage
}

func (e *RevertError) Error() string {
	if len(e.Data) > 0 {
		return fmt.Sprintf("ethguard: call reverted (corroborated observation): %s: 0x%x", e.message, e.Data)
	}
	return fmt.Sprintf("ethguard: call reverted (corroborated observation): %s", e.message)
}

// ErrorData makes RevertError compatible with go-ethereum's rpc.DataError.
// A hex string is returned as that string so RevertErrorData can decode it.
// A structured value is returned as decoded JSON, never coerced to hex.
// Missing and JSON null return nil rather than an empty "0x", so absence
// is not presented as empty bytes.
func (e *RevertError) ErrorData() interface{} {
	if e == nil {
		return nil
	}
	trimmed := bytes.TrimSpace(e.DataRaw)
	if len(trimmed) == 0 || string(trimmed) == "null" {
		if len(e.Data) > 0 {
			return hexutil.Encode(e.Data)
		}
		return nil
	}
	var value any
	if json.Unmarshal(trimmed, &value) == nil {
		return value
	}
	if len(e.Data) > 0 {
		return hexutil.Encode(e.Data)
	}
	return nil
}

// decodeCallDecision projects the corroborated call decision through the
// typed decoder and maps the neutral revert carrier onto the facade's own
// error type. internal/typed owns no facade error semantics.
func decodeCallDecision(decision rpc.Decision) ([]byte, error) {
	data, revert, err := typed.DecodeCallDecision(decision)
	if err != nil {
		return nil, err
	}
	if revert != nil {
		return nil, &RevertError{message: revert.Message, Data: revert.Data, DataRaw: revert.DataRaw}
	}
	return data, nil
}

// toCallArg mirrors go-ethereum's own client-side shape of an eth_call
// message, the "input" key spelling included, so requests stay
// wire-comparable with ethclient's.
func toCallArg(msg ethereum.CallMsg) map[string]any {
	arg := map[string]any{
		"from": msg.From,
		"to":   msg.To,
	}
	if len(msg.Data) > 0 {
		arg["input"] = hexutil.Bytes(msg.Data)
	}
	if msg.Value != nil {
		arg["value"] = (*hexutil.Big)(msg.Value)
	}
	if msg.Gas != 0 {
		arg["gas"] = hexutil.Uint64(msg.Gas)
	}
	if msg.GasPrice != nil {
		arg["gasPrice"] = (*hexutil.Big)(msg.GasPrice)
	}
	if msg.GasFeeCap != nil {
		arg["maxFeePerGas"] = (*hexutil.Big)(msg.GasFeeCap)
	}
	if msg.GasTipCap != nil {
		arg["maxPriorityFeePerGas"] = (*hexutil.Big)(msg.GasTipCap)
	}
	if msg.AccessList != nil {
		arg["accessList"] = msg.AccessList
	}
	if msg.BlobGasFeeCap != nil {
		arg["maxFeePerBlobGas"] = (*hexutil.Big)(msg.BlobGasFeeCap)
	}
	if msg.BlobHashes != nil {
		arg["blobVersionedHashes"] = msg.BlobHashes
	}
	if msg.AuthorizationList != nil {
		arg["authorizationList"] = msg.AuthorizationList
	}
	return arg
}

// EstimateGas returns the fleet's lower-median gas estimate for msg at the
// currently corroborated reference — a variable result, Selected at best, never
// claimed corroborated. A fleet that unanimously fails estimation fails as
// no-valid-response; the revert is never laundered into a number.
func (c *Client) EstimateGas(ctx context.Context, msg ethereum.CallMsg) (uint64, error) {
	decision, err := c.callTyped(ctx, methods.MethodEstimateGas, toCallArg(msg), "latest")
	if err != nil {
		return 0, err
	}
	return typed.DecodeGasEstimate(decision.Value)
}

// FilterLogs returns the fleet-corroborated logs matching q. Range filters are
// pinned to the corroborated reference exactly like any other anchored read —
// the policy rewrites moving tags and defaults missing bounds inside the
// filter object; a blockHash query addresses immutable content directly.
func (c *Client) FilterLogs(ctx context.Context, q ethereum.FilterQuery) ([]types.Log, error) {
	arg, err := toFilterArg(q)
	if err != nil {
		return nil, err
	}
	decision, err := c.callTyped(ctx, methods.MethodGetLogs, arg)
	if err != nil {
		return nil, err
	}
	return typed.DecodeLogs(decision.Value)
}

// toFilterArg mirrors go-ethereum's filter-object shape: address and topics
// are omitted when empty (some providers reject empty arrays), blockHash
// addresses the range explicitly, and otherwise geth's from/to spellings
// apply — a nil fromBlock becomes "0x0" and a nil toBlock "latest", which
// the anchored policy then pins to the corroborated reference.
func toFilterArg(q ethereum.FilterQuery) (map[string]any, error) {
	arg := map[string]any{}
	if len(q.Addresses) > 0 {
		arg["address"] = q.Addresses
	}
	if q.Topics != nil {
		arg["topics"] = q.Topics
	}
	if q.BlockHash != nil {
		if q.FromBlock != nil || q.ToBlock != nil {
			return nil, fmt.Errorf("ethguard: cannot specify both block hash and from/to blocks")
		}
		arg["blockHash"] = *q.BlockHash
		return arg, nil
	}
	fromBlock := "0x0"
	var err error
	if q.FromBlock != nil {
		fromBlock, err = checkedBlockNumArg(q.FromBlock)
		if err != nil {
			return nil, err
		}
	}
	toBlock, err := checkedBlockNumArg(q.ToBlock)
	if err != nil {
		return nil, err
	}
	arg["fromBlock"] = fromBlock
	arg["toBlock"] = toBlock
	return arg, nil
}

// PeerCount returns one healthy source's count of its own peers — an
// upstream-local result, selected not compared.
func (c *Client) PeerCount(ctx context.Context) (uint64, error) {
	decision, err := c.callTyped(ctx, methods.MethodPeerCount)
	if err != nil {
		return 0, err
	}
	value, err := c.decodedQuantity(methods.MethodPeerCount, decision)
	if err != nil {
		return 0, err
	}
	if !value.IsUint64() {
		return 0, fmt.Errorf("ethguard: invalid %s %q", methods.MethodPeerCount, decision.Value)
	}
	return value.Uint64(), nil
}

// SyncProgress reports the consulted source's sync state; nil means it
// considers itself fully synced.
func (c *Client) SyncProgress(ctx context.Context) (*ethereum.SyncProgress, error) {
	decision, err := c.callTyped(ctx, methods.MethodSyncing)
	if err != nil {
		return nil, err
	}
	return typed.DecodeSyncProgress(decision.Value)
}

// FeeHistory returns the fleet-corroborated fee history ending at the
// corroborated reference: lastBlock nil pins the moving "latest" tag, an
// explicit number passes through. The range is chain-determined once
// lastBlock is pinned, so the decision is Corroborated when sources agree;
// cross-client percentile rounding of Reward is the known liveness gate and
// fails closed as conflict until the soak measures it.
func (c *Client) FeeHistory(ctx context.Context, blockCount uint64, lastBlock *big.Int, rewardPercentiles []float64) (*ethereum.FeeHistory, error) {
	lastRef := "latest"
	if lastBlock != nil {
		lastRef = hexutil.EncodeBig(lastBlock)
	}
	decision, err := c.callTyped(ctx, methods.MethodFeeHistory, blockCount, lastRef, rewardPercentiles)
	if err != nil {
		return nil, err
	}
	return typed.DecodeFeeHistory(decision.Value)
}

// BlobBaseFee returns the fleet's lower-median blob base fee (EIP-4844) — a
// variable result, so the decision is Selected at best, with outliers kept as
// evidence.
func (c *Client) BlobBaseFee(ctx context.Context) (*big.Int, error) {
	decision, err := c.callTyped(ctx, methods.MethodBlobBaseFee)
	if err != nil {
		return nil, err
	}
	return c.decodedQuantity(methods.MethodBlobBaseFee, decision)
}

// AccessListResult represents the outcome of eth_createAccessList. The type
// and its decode are owned by internal/typed; the root facade re-exports the
// name so callers keep one import.
type AccessListResult = typed.AccessListResult

// CreateAccessList creates an EIP-2930 access list for a transaction at the
// corroborated reference block or an explicitly provided block argument.
// This is an EthGuard typed extension: go-ethereum's client does not expose
// eth_createAccessList, so the return value carries no decision evidence.
func (c *Client) CreateAccessList(ctx context.Context, msg any, blockNrOrHash ...any) (*AccessListResult, error) {
	params := []any{msg}
	if len(blockNrOrHash) > 0 {
		params = append(params, blockNrOrHash[0])
	}
	decision, err := c.callTyped(ctx, methods.MethodCreateAccessList, params...)
	if err != nil {
		return nil, err
	}
	return typed.DecodeAccessListResult(decision.Value)
}

// toBlockNumArg mirrors go-ethereum's conversion of a block number argument.
func toBlockNumArg(number *big.Int) string {
	if number == nil {
		return "latest"
	}
	if number.Sign() < 0 {
		// Int64 on a value outside int64 range truncates, which could fold an
		// out-of-range negative onto a known tag (-2..-5). Reject it here so
		// checkedBlockNumArg fails the request locally instead.
		if !number.IsInt64() {
			return ""
		}
		switch number.Int64() {
		case -1:
			return "pending"
		case -2:
			return "latest"
		case -3:
			return "finalized"
		case -4:
			return "safe"
		case -5:
			return "0x0"
		default:
			return ""
		}
	}
	return hexutil.EncodeBig(number)
}

func checkedBlockNumArg(number *big.Int) (string, error) {
	arg := toBlockNumArg(number)
	if arg == "" {
		return "", fmt.Errorf("ethguard: unsupported block number %s: %w", number.String(), ErrInvalidRequest)
	}
	return arg, nil
}
