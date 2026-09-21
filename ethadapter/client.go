// Package ethadapter presents EthGuard's corroborated aggregation behind an
// ethclient-shaped surface, so code written against go-ethereum's client can
// switch with minimal friction:
//
//	client, err := ethadapter.New(cfg)   // was: ethclient.Dial(url)
//
// Compatibility is method-shaped, not interface-claimed: the adapter offers
// same-named methods with identical signatures for the subset backed by
// corroborated policies. Anything beyond the subset stays reachable through the
// wrapped *ethguard.Client (Unwrap) and its generic Call.
//
// Semantics worth knowing before switching:
//
//   - Moving references ("latest") resolve through the configured anchoring
//     strategy; each invocation establishes its own reference and results
//     are pinned to a cross-corroborated reference rather than racing heads.
//   - BlockNumber reports the height of that corroborated reference — a source-agreement
//     override in the proxyd tradition, not one arbitrary provider's tip.
//   - Every failure keeps EthGuard's classified sentinels: errors.Is works
//     against the ethguard error surface unchanged.
package ethadapter

import (
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

	"ethguard"
	"ethguard/config"
	"ethguard/internal/refcontext"
	"ethguard/rpc"
	"ethguard/rpc/methods"
)

// Client is the primary supported-method client, with ethclient-shaped calls.
// Deterministic reads require corroboration before returning a typed value.
type Client struct {
	eg *ethguard.Client
}

// New builds the underlying corroborated client from configuration and returns
// it in ethclient shape. Options pass through unchanged.
func New(cfg config.Config, opts ...ethguard.Option) (*Client, error) {
	eg, err := ethguard.New(cfg, opts...)
	if err != nil {
		return nil, err
	}
	return &Client{eg: eg}, nil
}

// Wrap adapts an already-built ethguard.Client.
func Wrap(eg *ethguard.Client) *Client {
	return &Client{eg: eg}
}

// Unwrap exposes the underlying client for methods beyond this adapter's
// corroborated subset (generic Call, Pool, lifecycle).
func (c *Client) Unwrap() *ethguard.Client { return c.eg }

// Close releases all upstream connections owned by the client.
func (c *Client) Close() { c.eg.Close() }

func (c *Client) requireContext(ctx context.Context) context.Context {
	if c.eg.Mode() == rpc.CorroborationForwarded {
		return refcontext.WithForwarded(ctx)
	}
	return refcontext.RequireCorroborated(ctx)
}

// ChainID returns the fleet-corroborated chain ID.
func (c *Client) ChainID(ctx context.Context) (*big.Int, error) {
	ctx = c.requireContext(ctx)
	id, _, err := c.eg.ChainID(ctx)
	return id, err
}

// BalanceAt returns the corroborated balance of account at blockNumber; nil
// means the currently corroborated reference behind "latest".
func (c *Client) BalanceAt(ctx context.Context, account common.Address, blockNumber *big.Int) (*big.Int, error) {
	ctx = c.requireContext(ctx)
	blockArg, err := checkedBlockNumArg(blockNumber)
	if err != nil {
		return nil, err
	}
	value, _, err := ethguard.CallInto[hexutil.Big](
		ctx, c.eg, methods.MethodGetBalance, account.Hex(), blockArg)
	if err != nil {
		return nil, err
	}
	return (*big.Int)(value), nil
}

// BlockByHash returns the corroborated block with the given hash.
func (c *Client) BlockByHash(ctx context.Context, hash common.Hash) (*types.Block, error) {
	ctx = c.requireContext(ctx)
	block, _, err := c.eg.BlockByHash(ctx, hash, true)
	return block, err
}

// BlockByNumber returns the corroborated block at number; nil pins the moving
// "latest" tag to the currently corroborated reference first.
func (c *Client) BlockByNumber(ctx context.Context, number *big.Int) (*types.Block, error) {
	ctx = c.requireContext(ctx)
	if number != nil && number.Sign() >= 0 && !number.IsUint64() {
		return nil, fmt.Errorf("block number exceeds supported height range: %w", ethguard.ErrInvalidRequest)
	}
	if number != nil && number.Sign() < 0 {
		blockArg, err := checkedBlockNumArg(number)
		if err != nil {
			return nil, err
		}
		return c.blockAt(ctx, blockArg)
	}
	if number == nil {
		ref, err := c.eg.AnchorBlockHash(ctx)
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

// BlockNumber returns the height of the currently corroborated block reference —
// a derivation over corroborated primitives, never one provider's tip. The full
// selection reasoning lives on the root Client.BlockNumber.
func (c *Client) BlockNumber(ctx context.Context) (uint64, error) {
	ctx = c.requireContext(ctx)
	return c.eg.BlockNumber(ctx)
}

// TransactionReceipt returns the fleet-corroborated receipt for txHash;
// ethereum.NotFound when the fleet corroborates the transaction has no receipt
// yet, matching go-ethereum's own client. The hash addresses immutable
// content, so no anchoring or freshness machinery applies.
func (c *Client) TransactionReceipt(ctx context.Context, txHash common.Hash) (*types.Receipt, error) {
	ctx = c.requireContext(ctx)
	receipt, _, err := c.eg.TransactionReceipt(ctx, txHash)
	return receipt, err
}

// TransactionByHash returns the fleet-corroborated transaction for txHash and
// whether the fleet saw it as still pending; ethereum.NotFound when the
// fleet corroborates no such transaction exists. Pending transactions are
// ordinary corroborated objects here: while they stay pending, honest sources
// hold identical payloads.
func (c *Client) TransactionByHash(ctx context.Context, txHash common.Hash) (*types.Transaction, bool, error) {
	ctx = c.requireContext(ctx)
	decision, err := c.eg.Call(ctx, methods.MethodGetTransactionByHash, txHash.Hex())
	if err != nil {
		return nil, false, err
	}
	tx, pending, err := ethguard.DecodeTransaction(decision.Value)
	if err != nil {
		return nil, false, err
	}
	if tx.Hash() != txHash {
		return nil, false, fmt.Errorf("%w: transaction hash %s does not match requested hash %s",
			ethguard.ErrMalformedResponse, tx.Hash(), txHash)
	}
	return tx, pending, nil
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
	ctx = c.requireContext(ctx)
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
				return nil, fmt.Errorf("ethadapter: requireCanonical with moving block tag %s is unsupported: %w", number, ethguard.ErrInvalidRequest)
			}
			ref = map[string]any{"blockNumber": refText, "requireCanonical": true}
		} else if hash, ok := blockNrOrHash.Hash(); ok {
			ref = map[string]any{"blockHash": hash, "requireCanonical": true}
			canonicalHash = &hash
		}
	}
	decision, err := c.eg.Call(ctx, methods.MethodGetBlockReceipts, ref)
	if err != nil {
		return nil, err
	}
	receipts, err := ethguard.DecodeReceipts(decision.Value)
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
					ethguard.ErrMalformedResponse,
					ethguard.ErrAnchorMismatch,
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
		return nil, errors.Join(ethguard.ErrMalformedResponse, ethguard.ErrAnchorMismatch,
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
				ethguard.ErrMalformedResponse,
				ethguard.ErrAnchorMismatch,
				fmt.Errorf("receipt %d block hash %s does not match requested hash %s", i, receipt.BlockHash, hash),
			)
		}
	}
	return receipts, nil
}

// NonceAt returns the corroborated account nonce of account at blockNumber;
// nil pins the moving tag to the currently corroborated reference.
func (c *Client) NonceAt(ctx context.Context, account common.Address, blockNumber *big.Int) (uint64, error) {
	ctx = c.requireContext(ctx)
	blockArg, err := checkedBlockNumArg(blockNumber)
	if err != nil {
		return 0, err
	}
	nonce, _, err := ethguard.CallInto[hexutil.Uint64](
		ctx, c.eg, methods.MethodGetTransactionCount, account.Hex(), blockArg)
	if err != nil {
		return 0, err
	}
	return uint64(*nonce), nil
}

// StorageAt returns the corroborated value of key in account's storage at
// blockNumber; nil pins the moving tag to the currently corroborated reference.
func (c *Client) StorageAt(ctx context.Context, account common.Address, key common.Hash, blockNumber *big.Int) ([]byte, error) {
	ctx = c.requireContext(ctx)
	blockArg, err := checkedBlockNumArg(blockNumber)
	if err != nil {
		return nil, err
	}
	value, _, err := ethguard.CallInto[hexutil.Bytes](
		ctx, c.eg, methods.MethodGetStorageAt, account.Hex(), key.Hex(), blockArg)
	if err != nil {
		return nil, err
	}
	return *value, nil
}

// CodeAt returns the corroborated contract code of account at blockNumber; nil
// pins the moving tag to the currently corroborated reference. An externally
// owned account answers empty code, not an error.
func (c *Client) CodeAt(ctx context.Context, account common.Address, blockNumber *big.Int) ([]byte, error) {
	ctx = c.requireContext(ctx)
	blockArg, err := checkedBlockNumArg(blockNumber)
	if err != nil {
		return nil, err
	}
	code, _, err := ethguard.CallInto[hexutil.Bytes](
		ctx, c.eg, methods.MethodGetCode, account.Hex(), blockArg)
	if err != nil {
		return nil, err
	}
	return *code, nil
}

// SuggestGasTipCap returns the fleet's lower-median priority-fee estimate —
// a variable result resolved by robust selection, never claimed corroborated.
func (c *Client) SuggestGasTipCap(ctx context.Context) (*big.Int, error) {
	ctx = c.requireContext(ctx)
	tip, _, err := c.eg.MaxPriorityFeePerGas(ctx)
	return tip, err
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
				return "", fmt.Errorf("ethadapter: unsupported block number %d: %w", number, ethguard.ErrInvalidRequest)
			}
			return checkedBlockNumArg(big.NewInt(number.Int64()))
		}
	}
	return "", fmt.Errorf("ethadapter: empty block number or hash")
}

// SuggestGasPrice returns the fleet's lower-median gas price — a variable
// result resolved by robust selection, never claimed corroborated.
func (c *Client) SuggestGasPrice(ctx context.Context) (*big.Int, error) {
	ctx = c.requireContext(ctx)
	price, _, err := c.eg.GasPrice(ctx)
	return price, err
}

// CallContract returns the fleet-corroborated result of executing msg at
// blockNumber; nil pins the moving tag to the currently corroborated reference.
// A fleet-corroborated revert is an observation, not a pipeline failure: it is
// returned as a *RevertError carrying the revert context, while the
// corroboration claim stays on the decision evidence.
func (c *Client) CallContract(ctx context.Context, msg ethereum.CallMsg, blockNumber *big.Int) ([]byte, error) {
	ctx = c.requireContext(ctx)
	blockArg, err := checkedBlockNumArg(blockNumber)
	if err != nil {
		return nil, err
	}
	decision, err := c.eg.Call(ctx, methods.MethodCall, toCallArg(msg), blockArg)
	if err != nil {
		return nil, err
	}
	return decodeCallResult(decision.Value)
}

// EstimateGas returns the fleet's lower-median gas estimate for msg at the
// currently corroborated reference — a variable result, Selected at best, never
// claimed corroborated. A fleet that unanimously fails estimation fails as
// no-valid-response; the revert is never laundered into a number.
func (c *Client) EstimateGas(ctx context.Context, msg ethereum.CallMsg) (uint64, error) {
	ctx = c.requireContext(ctx)
	decision, err := c.eg.Call(ctx, methods.MethodEstimateGas, toCallArg(msg), "latest")
	if err != nil {
		return 0, err
	}
	var gas hexutil.Uint64
	if err := json.Unmarshal(decision.Value, &gas); err != nil {
		return 0, fmt.Errorf("%w: decode gas estimate: %v", rpc.ErrMalformedResponse, err)
	}
	return uint64(gas), nil
}

// FilterLogs returns the fleet-corroborated logs matching q. Range filters are
// pinned to the corroborated reference exactly like any other anchored read —
// the policy rewrites moving tags and defaults missing bounds inside the
// filter object; a blockHash query addresses immutable content directly.
func (c *Client) FilterLogs(ctx context.Context, q ethereum.FilterQuery) ([]types.Log, error) {
	ctx = c.requireContext(ctx)
	arg, err := toFilterArg(q)
	if err != nil {
		return nil, err
	}
	decision, err := c.eg.Call(ctx, methods.MethodGetLogs, arg)
	if err != nil {
		return nil, err
	}
	if string(decision.Value) == "null" {
		return nil, fmt.Errorf("%w: log array is JSON null", rpc.ErrMalformedResponse)
	}
	var logs []types.Log
	if err := json.Unmarshal(decision.Value, &logs); err != nil {
		return nil, fmt.Errorf("%w: decode logs: %v", rpc.ErrMalformedResponse, err)
	}
	return logs, nil
}

// RevertError reports a fleet-corroborated execution revert of an eth_call. It
// is an observation rather than a pipeline failure — none of the classified
// sentinels apply — and its message and data are provider context:
// uncorroborated content with the same standing decision 0015 gives the
// server-provided `from` field.
type RevertError struct {
	// Code is the JSON-RPC error code supplied by the provider. Geth uses
	// code 3 for execution reverts while a number of providers use -32000;
	// retaining the wire value lets callers distinguish those dialects.
	Code    int
	Message string
	Data    []byte
	// DataRaw retains a non-hex data value (some providers return a structured
	// object) without forcing it through the byte-oriented ethclient helper.
	DataRaw json.RawMessage
}

func (e *RevertError) Error() string {
	if len(e.Data) > 0 {
		return fmt.Sprintf("ethguard: call reverted (corroborated observation): %s: 0x%x", e.Message, e.Data)
	}
	return fmt.Sprintf("ethguard: call reverted (corroborated observation): %s", e.Message)
}

// ErrorCode makes RevertError compatible with go-ethereum's rpc.Error. The
// original provider code remains available through Code.
func (e *RevertError) ErrorCode() int {
	if e == nil {
		return 0
	}
	return e.Code
}

// ErrorData makes RevertError compatible with go-ethereum's rpc.DataError.
// Geth's RevertErrorData helper expects the conventional hex string; callers
// that need a structured provider extension can inspect DataRaw directly.
func (e *RevertError) ErrorData() interface{} {
	if e == nil {
		return nil
	}
	if len(e.DataRaw) > 0 && string(e.DataRaw) != "null" {
		var value interface{}
		if json.Unmarshal(e.DataRaw, &value) == nil {
			return value
		}
	}
	if e.Data != nil {
		return hexutil.Encode(e.Data)
	}
	return "0x"
}

// decodeCallResult separates a corroborated revert envelope from return data.
func decodeCallResult(raw json.RawMessage) ([]byte, error) {
	var envelope struct {
		Code    int             `json:"code"`
		Message string          `json:"message"`
		Data    json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(raw, &envelope); err == nil && (envelope.Code != 0 || envelope.Message != "") {
		revert := &RevertError{Code: envelope.Code, Message: envelope.Message, DataRaw: append(json.RawMessage(nil), envelope.Data...)}
		var hexData string
		if len(envelope.Data) > 0 && json.Unmarshal(envelope.Data, &hexData) == nil {
			if data, err := hexutil.Decode(hexData); err == nil {
				revert.Data = data
			}
		}
		return nil, revert
	}
	if string(raw) == "null" {
		return nil, fmt.Errorf("%w: null call result", rpc.ErrMalformedResponse)
	}
	var out hexutil.Bytes
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("%w: decode call result: %v", rpc.ErrMalformedResponse, err)
	}
	return out, nil
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
			return nil, fmt.Errorf("ethadapter: cannot specify both block hash and from/to blocks")
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

// PeerCount returns one healthy source's count of its own peers.
func (c *Client) PeerCount(ctx context.Context) (uint64, error) {
	ctx = c.requireContext(ctx)
	count, _, err := c.eg.PeerCount(ctx)
	return count, err
}

// SyncProgress reports the consulted source's sync state; nil means it
// considers itself fully synced.
func (c *Client) SyncProgress(ctx context.Context) (*ethereum.SyncProgress, error) {
	ctx = c.requireContext(ctx)
	progress, _, err := c.eg.Syncing(ctx)
	return progress, err
}

// blockAt fetches and strictly compares one block identified explicitly by
// hash or height — never by a moving tag. Which method the reference rides
// is a wire-portability decision: the first live soak run caught public
// endpoints rejecting a 32-byte hash in eth_getBlockByNumber's number
// position ("hex number > 64 bits"), so hash-looking references take the
// hash-addressed method and everything else the number-addressed one.
func (c *Client) blockAt(ctx context.Context, ref string) (*types.Block, error) {
	ctx = c.requireContext(ctx)
	method := methods.MethodGetBlockByNumber
	if len(ref) == 2+2*common.HashLength && strings.HasPrefix(ref, "0x") {
		method = methods.MethodGetBlockByHash
	}
	decision, err := c.eg.Call(ctx, method, ref, true)
	if err != nil {
		return nil, err
	}
	block, err := ethguard.DecodeBlockWithEvidence(decision.Value, decision.Evidence)
	if err != nil {
		return nil, err
	}
	// Request-answer binding rides the adapter path too: the root wrapper
	// holds the same check, and an adapter-only caller gets identical
	// integrity guarantees against a coherent fabricated fleet.
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

// toBlockNumArg mirrors go-ethereum's conversion of a block number argument.
func toBlockNumArg(number *big.Int) string {
	if number == nil {
		return "latest"
	}
	if number.Sign() < 0 {
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
		return "", fmt.Errorf("ethadapter: unsupported block number %s: %w", number.String(), ethguard.ErrInvalidRequest)
	}
	return arg, nil
}
