package ethguard

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math/big"
	"strconv"
	"strings"

	ethereum "github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common/hexutil"

	"github.com/codermaybe/ethguard/ethapi"
	objectlog "github.com/codermaybe/ethguard/internal/ethobject/log"
	"github.com/codermaybe/ethguard/internal/methods"
	"github.com/codermaybe/ethguard/internal/typed"
	"github.com/codermaybe/ethguard/rpc"
)

// TransactionByHash returns the fleet-corroborated transaction for the hash and
// whether the fleet saw it as still pending; ethereum.NotFound when the
// fleet corroborates no such transaction exists. Pending transactions are
// ordinary corroborated objects here: while they stay pending, honest sources
// hold identical payloads.
func (c *Client) TransactionByHash(ctx context.Context, hash ethapi.Hash) (*ethapi.Transaction, bool, CallResult, error) {
	return operationPair(c, ctx, false, func(opCtx context.Context) (*ethapi.Transaction, bool, CallResult, error) {
		return c.transactionByHashExec(opCtx, hash)
	})
}

func (c *Client) transactionByHashExec(ctx context.Context, hash ethapi.Hash) (*ethapi.Transaction, bool, CallResult, error) {
	hash, err := validateHash(hash)
	if err != nil {
		return nil, false, CallResult{}, err
	}
	decision, err := c.callTyped(ctx, methods.MethodGetTransactionByHash, string(hash))
	if err != nil {
		decision.Value = nil
		return nil, false, CallResult{Decision: decisionOf(decision)}, err
	}
	tx, pending, err := typed.DecodeTransaction(decision.Value)
	if err != nil {
		return nil, false, CallResult{Decision: decisionOf(decision)}, err
	}
	declared, err := typed.TransactionHash(tx)
	if err != nil || declared != hash {
		got := string(declared)
		if err != nil {
			got = err.Error()
		}
		return nil, false, CallResult{Decision: decisionOf(decision)}, fmt.Errorf("%w: transaction hash %s does not match requested hash %s",
			rpc.ErrMalformedResponse, got, hash)
	}
	return tx, pending, CallResult{Decision: decisionOf(decision)}, nil
}

// TransactionCount returns the fleet-corroborated transaction count for an
// immutable block hash. A corroborated JSON null maps to ethereum.NotFound.
func (c *Client) TransactionCount(ctx context.Context, blockHash ethapi.Hash) (uint, CallResult, error) {
	return operation(c, ctx, false, func(opCtx context.Context) (uint, CallResult, error) {
		return c.transactionCountExec(opCtx, blockHash)
	})
}

func (c *Client) transactionCountExec(ctx context.Context, blockHash ethapi.Hash) (uint, CallResult, error) {
	blockHash, err := validateHash(blockHash)
	if err != nil {
		return 0, CallResult{}, err
	}
	decision, err := c.callTyped(ctx, methods.MethodGetBlockTransactionCountByHash, string(blockHash))
	if err != nil {
		decision.Value = nil
		return 0, CallResult{Decision: decisionOf(decision)}, err
	}
	if bytes.Equal(bytes.TrimSpace(decision.Value), []byte("null")) {
		return 0, CallResult{Decision: decisionOf(decision)}, ethereum.NotFound
	}
	canonical, err := c.Normalize(methods.MethodGetBlockTransactionCountByHash, decision.Value)
	if err != nil {
		return 0, CallResult{Decision: decisionOf(decision)}, fmt.Errorf("%w: decode %s: %v", rpc.ErrMalformedResponse, methods.MethodGetBlockTransactionCountByHash, err)
	}
	value, ok := new(big.Int).SetString(canonical, 10)
	if !ok || !value.IsUint64() || (strconv.IntSize == 32 && value.BitLen() > 32) {
		return 0, CallResult{Decision: decisionOf(decision)}, fmt.Errorf("%w: transaction count out of range %q", rpc.ErrMalformedResponse, canonical)
	}
	return uint(value.Uint64()), CallResult{Decision: decisionOf(decision)}, nil
}

// TransactionInBlock returns the transaction at an index in an immutable
// block, or ethereum.NotFound when the block/index has no transaction.
func (c *Client) TransactionInBlock(ctx context.Context, blockHash ethapi.Hash, index uint) (*ethapi.Transaction, CallResult, error) {
	return operation(c, ctx, false, func(opCtx context.Context) (*ethapi.Transaction, CallResult, error) {
		return c.transactionInBlockExec(opCtx, blockHash, index)
	})
}

func (c *Client) transactionInBlockExec(ctx context.Context, blockHash ethapi.Hash, index uint) (*ethapi.Transaction, CallResult, error) {
	blockHash, err := validateHash(blockHash)
	if err != nil {
		return nil, CallResult{}, err
	}
	decision, err := c.callTyped(ctx, methods.MethodGetTransactionByBlockHashAndIndex, string(blockHash), hexutil.Uint64(index))
	if err != nil {
		decision.Value = nil
		return nil, CallResult{Decision: decisionOf(decision)}, err
	}
	decoded, err := typed.DecodeTransactionInBlock(decision.Value, blockHash, uint64(index))
	if err != nil {
		return nil, CallResult{Decision: decisionOf(decision)}, err
	}
	return decoded.Tx, CallResult{Decision: decisionOf(decision)}, nil
}

// TransactionSender returns the sender reported for the transaction at the
// requested immutable block/index. It deliberately performs a fresh RPC read
// and never relies on hidden sender state attached to the transaction value;
// the returned address is the RPC-declared sender at this decision's trust
// level, not a recovered or inclusion-proven identity.
func (c *Client) TransactionSender(ctx context.Context, tx *ethapi.Transaction, blockHash ethapi.Hash, index uint) (ethapi.Address, CallResult, error) {
	return operation(c, ctx, false, func(opCtx context.Context) (ethapi.Address, CallResult, error) {
		return c.transactionSenderExec(opCtx, tx, blockHash, index)
	})
}

func (c *Client) transactionSenderExec(ctx context.Context, tx *ethapi.Transaction, blockHash ethapi.Hash, index uint) (ethapi.Address, CallResult, error) {
	// The caller's value is re-validated and its signed-payload hash
	// recomputed before any network contact: a nil, zero-selected,
	// multi-selected, structurally illegal, unsigned, or misdeclared input
	// fails locally instead of binding a foreign sender.
	payload, err := typed.TransactionPayload(tx)
	if err != nil {
		return "", CallResult{}, err
	}
	blockHash, err = validateHash(blockHash)
	if err != nil {
		return "", CallResult{}, err
	}
	decision, err := c.callTyped(ctx, methods.MethodGetTransactionByBlockHashAndIndex, string(blockHash), hexutil.Uint64(index))
	if err != nil {
		decision.Value = nil
		return "", CallResult{Decision: decisionOf(decision)}, err
	}
	decoded, err := typed.DecodeTransactionInBlock(decision.Value, blockHash, uint64(index))
	if err != nil {
		return "", CallResult{Decision: decisionOf(decision)}, err
	}
	if string(decoded.TxHash) != payload.Hash().Hex() {
		return "", CallResult{Decision: decisionOf(decision)}, fmt.Errorf("%w: remote transaction hash %s does not match input %s", rpc.ErrMalformedResponse, decoded.TxHash, payload.Hash())
	}
	if !decoded.HasFrom {
		return "", CallResult{Decision: decisionOf(decision)}, fmt.Errorf("%w: transaction response has no sender", rpc.ErrMalformedResponse)
	}
	return decoded.From, CallResult{Decision: decisionOf(decision)}, nil
}

// PendingTransactionCount returns a node-local observation of the pending
// transaction count. Pending mempools are not a network-wide consensus value.
func (c *Client) PendingTransactionCount(ctx context.Context) (uint, CallResult, error) {
	return operation(c, ctx, false, c.pendingTransactionCountExec)
}

// pendingTransactionCountExec returns the selected local observation of the
// pending transaction pool. The method plan marks the pending argument as
// PendingObserve, so the decision is Selected rather than Corroborated and no
// moving reference is established.
func (c *Client) pendingTransactionCountExec(ctx context.Context) (uint, CallResult, error) {
	value, decision, err := aggregateInto[hexutil.Uint](ctx, c, methods.MethodGetBlockTransactionCountByNumber, "pending")
	if err != nil {
		return 0, CallResult{Decision: decisionOf(decision)}, err
	}
	return uint(*value), CallResult{Decision: decisionOf(decision)}, nil
}

// TransactionReceipt returns the fleet-corroborated receipt for the transaction
// hash; ethereum.NotFound when the fleet corroborates the transaction has no
// receipt yet, matching go-ethereum's own client. The hash addresses immutable
// content, so no anchoring or freshness machinery applies.
func (c *Client) TransactionReceipt(ctx context.Context, hash ethapi.Hash) (*ethapi.Receipt, CallResult, error) {
	return operation(c, ctx, false, func(opCtx context.Context) (*ethapi.Receipt, CallResult, error) {
		return c.transactionReceiptExec(opCtx, hash)
	})
}

func (c *Client) transactionReceiptExec(ctx context.Context, hash ethapi.Hash) (*ethapi.Receipt, CallResult, error) {
	hash, err := validateHash(hash)
	if err != nil {
		return nil, CallResult{}, err
	}
	decision, err := c.callTyped(ctx, methods.MethodGetTransactionReceipt, string(hash))
	if err != nil {
		decision.Value = nil
		return nil, CallResult{Decision: decisionOf(decision)}, err
	}
	receipt, err := typed.DecodeReceipt(decision.Value)
	if err != nil {
		return nil, CallResult{Decision: decisionOf(decision)}, err
	}
	if receipt.TransactionHash != hash {
		return nil, CallResult{Decision: decisionOf(decision)}, fmt.Errorf("%w: receipt transaction hash %s does not match requested hash %s",
			rpc.ErrMalformedResponse, receipt.TransactionHash, hash)
	}
	return receipt, CallResult{Decision: decisionOf(decision)}, nil
}

// BlockReceipts returns the fleet-corroborated receipts of one block. The
// reference is the generated BlockReference: bare quantities, moving tags,
// bare hashes, EIP-1898 number objects and EIP-1898 hash objects. Numbers
// and moving tags are pinned to the corroborated reference before fan-out;
// hashes address immutable content directly. Corroborated absence of the
// block maps to ethereum.NotFound.
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
func (c *Client) BlockReceipts(ctx context.Context, blockRef ethapi.BlockReference) ([]ethapi.Receipt, CallResult, error) {
	return operation(c, ctx, false, func(opCtx context.Context) ([]ethapi.Receipt, CallResult, error) {
		return c.blockReceiptsExec(opCtx, blockRef)
	})
}

func (c *Client) blockReceiptsExec(ctx context.Context, blockRef ethapi.BlockReference) ([]ethapi.Receipt, CallResult, error) {
	frozen, err := freezeBlockReference(blockRef)
	if err != nil {
		return nil, CallResult{}, err
	}
	refArg, bindHash, bindHeight, movingTag, err := blockReceiptsWire(frozen)
	if err != nil {
		return nil, CallResult{}, err
	}
	decision, err := c.callTyped(ctx, methods.MethodGetBlockReceipts, refArg)
	if err != nil {
		decision.Value = nil
		return nil, CallResult{Decision: decisionOf(decision)}, err
	}
	receipts, err := typed.DecodeReceipts(decision.Value)
	if err != nil {
		return nil, CallResult{Decision: decisionOf(decision)}, err
	}
	if bindHash != nil {
		receipts, err = bindReceiptsToHash(receipts, *bindHash)
		return receipts, CallResult{Decision: decisionOf(decision)}, err
	}
	if bindHeight != nil {
		// The earliest sentinel is an explicit height in disguise: it binds
		// like any other explicit number and never through the moving-tag pin
		// path below, which has no reference to bind.
		for i := range receipts {
			height, heightErr := typed.QuantityHeight(receipts[i].BlockNumber)
			if heightErr != nil || height != *bindHeight {
				return nil, CallResult{Decision: decisionOf(decision)}, errors.Join(
					ErrMalformedResponse,
					ErrAnchorMismatch,
					fmt.Errorf("receipt %d height %s does not match requested number %d", i, receipts[i].BlockNumber, *bindHeight),
				)
			}
		}
		return receipts, CallResult{Decision: decisionOf(decision)}, nil
	}
	// Moving tag: the policy rewrote it to the corroborated reference, and
	// decision.BlockRef carries that pin. Bind against the pin, not against
	// a fresh anchor call.
	if !movingTag || decision.BlockRef == "" {
		return nil, CallResult{Decision: decisionOf(decision)}, errors.Join(ErrMalformedResponse, ErrAnchorMismatch,
			errors.New("pinned receipts carry no block reference"))
	}
	pinned := ethapi.Hash(strings.ToLower(decision.BlockRef))
	receipts, err = bindReceiptsToHash(receipts, pinned)
	return receipts, CallResult{Decision: decisionOf(decision)}, err
}

// bindReceiptsToHash applies the per-receipt block-hash binding shared by
// hash-addressed and tag-pinned requests.
func bindReceiptsToHash(receipts []ethapi.Receipt, hash ethapi.Hash) ([]ethapi.Receipt, error) {
	for i := range receipts {
		if receipts[i].BlockHash != hash {
			return nil, errors.Join(
				ErrMalformedResponse,
				ErrAnchorMismatch,
				fmt.Errorf("receipt %d block hash %s does not match requested hash %s", i, receipts[i].BlockHash, hash),
			)
		}
	}
	return receipts, nil
}

// blockReceiptsWire maps one frozen generated reference onto the wire
// argument BlockReceipts sends and the request-binding facts the answer must
// satisfy. Exactly one binding fact is produced:
//
//   - a bare hash, or a hash object, binds every receipt's BlockHash — the
//     object keeps the declared requireCanonical presence exactly: an
//     explicit false stays on the wire as false, an absent member stays
//     absent and is never rewritten into an explicit false;
//   - a bare quantity or a number object binds every receipt's BlockNumber —
//     the number object carries only the quantity, no tag and no
//     requireCanonical member;
//   - the earliest tag keeps the project's explicit-0x0 contract and binds
//     like a number;
//   - a moving tag (latest/safe/finalized) is sent as the tag itself and
//     binds through the reference the policy pins;
//   - pending is rejected locally before any contact.
//
// The bare quantity and bare hash anyOf branches overlap on the wire: a
// 32-byte word satisfies the quantity pattern, so the union's first-match
// branch is never treated as caller intent. The prep layer keeps the
// established bare-32-byte-word hash interpretation — a 66-character
// lowercase-hex string binds as a hash, everything else as a height.
func blockReceiptsWire(ref ethapi.BlockReference) (arg any, bindHash *ethapi.Hash, bindHeight *uint64, movingTag bool, err error) {
	switch {
	case ref.BlockHash != nil:
		arg = string(*ref.BlockHash)
		bindHash = ref.BlockHash
	case ref.BlockNumber != nil:
		if isBareBlockHash(string(*ref.BlockNumber)) {
			hash := ethapi.Hash(strings.ToLower(string(*ref.BlockNumber)))
			arg = string(hash)
			bindHash = &hash
			break
		}
		height, heightErr := rpc.ParseHexQuantity(string(*ref.BlockNumber))
		if heightErr != nil {
			return nil, nil, nil, false, fmt.Errorf("ethguard: block number %s: %w", *ref.BlockNumber, ErrInvalidRequest)
		}
		arg = string(*ref.BlockNumber)
		bindHeight = &height
	case ref.BlockTag != nil:
		switch *ref.BlockTag {
		case "pending":
			return nil, nil, nil, false, fmt.Errorf("ethguard: pending block reference is not supported: %w", ErrInvalidRequest)
		case "earliest":
			height := uint64(0)
			arg = "0x0"
			bindHeight = &height
		case "latest", "safe", "finalized":
			arg = string(*ref.BlockTag)
			movingTag = true
		default:
			return nil, nil, nil, false, fmt.Errorf("ethguard: block tag %q is not a reference this method binds: %w", *ref.BlockTag, ErrInvalidRequest)
		}
	case ref.Eip1898BlockNumberObject != nil:
		height, heightErr := rpc.ParseHexQuantity(string(ref.Eip1898BlockNumberObject.BlockNumber))
		if heightErr != nil {
			return nil, nil, nil, false, fmt.Errorf("ethguard: block number %s: %w", ref.Eip1898BlockNumberObject.BlockNumber, ErrInvalidRequest)
		}
		arg = map[string]any{"blockNumber": string(ref.Eip1898BlockNumberObject.BlockNumber)}
		bindHeight = &height
	case ref.Eip1898BlockHashObject != nil:
		object := ref.Eip1898BlockHashObject
		objectArg := map[string]any{"blockHash": string(object.BlockHash)}
		// Presence is part of the request: an explicit requireCanonical —
		// true or false — rides the wire as declared, an absent member is
		// never fabricated into an explicit default.
		if object.RequireCanonical != nil {
			objectArg["requireCanonical"] = *object.RequireCanonical
		}
		arg = objectArg
		bindHash = &object.BlockHash
	default:
		return nil, nil, nil, false, fmt.Errorf("ethguard: empty block reference: %w", ErrInvalidRequest)
	}
	return arg, bindHash, bindHeight, movingTag, nil
}

// FilterLogs returns the fleet-corroborated logs matching q. Range filters are
// pinned to the corroborated reference exactly like any other anchored read —
// the policy rewrites moving tags and defaults missing bounds inside the
// filter object; a blockHash query addresses immutable content directly.
func (c *Client) FilterLogs(ctx context.Context, q ethapi.FilterQuery) ([]ethapi.Log, CallResult, error) {
	return operation(c, ctx, false, func(opCtx context.Context) ([]ethapi.Log, CallResult, error) {
		return c.filterLogsExec(opCtx, q)
	})
}

func (c *Client) filterLogsExec(ctx context.Context, q ethapi.FilterQuery) ([]ethapi.Log, CallResult, error) {
	arg, err := objectlog.FilterArgument(q)
	if err != nil {
		return nil, CallResult{}, err
	}
	decision, err := c.callTyped(ctx, methods.MethodGetLogs, arg)
	if err != nil {
		decision.Value = nil
		return nil, CallResult{Decision: decisionOf(decision)}, err
	}
	logs, err := typed.DecodeLogs(decision.Value)
	return logs, CallResult{Decision: decisionOf(decision)}, err
}
