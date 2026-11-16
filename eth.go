package ethguard

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strings"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"

	"github.com/codermaybe/ethguard/ethapi"
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

// aggregateInto runs one corroborated read and decodes the resolved value
// into T. On an aggregation failure the public value is cleared while every
// other formed fact stays; a decode failure keeps the aggregated value — the
// failure came after the aggregation succeeded.
func aggregateInto[T any](ctx context.Context, c *Client, method string, params ...any) (*T, rpc.Decision, error) {
	decision, err := c.callTyped(ctx, method, params...)
	if err != nil {
		decision.Value = nil
		return nil, decision, err
	}
	var out T
	if err := jsonsafe.Decode(decision.Value, &out); err != nil {
		return nil, decision, fmt.Errorf("%w: decode %s: %w", rpc.ErrMalformedResponse, method, err)
	}
	return &out, decision, nil
}

// decisionOf snapshots the aggregator-produced decision onto the result.
// A local rejection before any adjudication constructs an empty CallResult
// directly; this helper is only called with an aggregator outcome, so the
// pointer always carries that outcome's facts.
func decisionOf(decision rpc.Decision) *rpc.Decision {
	return &decision
}

// validateAddress re-validates one caller-supplied generated address value
// through the generated constructor and returns its canonical lowercase
// spelling. Directly constructed values can carry any string — including
// the empty one — so every entry validates before it builds a reference or
// contacts the network, and only the canonical value rides the wire. The
// empty string, a wrong prefix, wrong length, or non-hex text is rejected
// locally as ErrInvalidRequest; the explicit all-zero address is legal.
func validateAddress(v ethapi.Address) (ethapi.Address, error) {
	quoted, err := json.Marshal(string(v))
	if err != nil {
		return "", fmt.Errorf("ethguard: invalid address: %w", ErrInvalidRequest)
	}
	canon, err := ethapi.NewAddress(quoted)
	if err != nil {
		return "", fmt.Errorf("ethguard: invalid address %q: %w", string(v), ErrInvalidRequest)
	}
	return canon, nil
}

// validateHash is the hash-side twin of validateAddress: the generated
// constructor is the acceptance gate (fixed-hex-v1, 32 bytes, any-case
// input, lowercase canonical output) and no entry sends the raw spelling.
func validateHash(v ethapi.Hash) (ethapi.Hash, error) {
	quoted, err := json.Marshal(string(v))
	if err != nil {
		return "", fmt.Errorf("ethguard: invalid hash: %w", ErrInvalidRequest)
	}
	canon, err := ethapi.NewHash(quoted)
	if err != nil {
		return "", fmt.Errorf("ethguard: invalid hash %q: %w", string(v), ErrInvalidRequest)
	}
	return canon, nil
}

// readQuantity is the shared shape of the quantity reads: validate inside
// the operation, aggregate, project through the owning method's normalizer.
// The projection failure keeps the aggregated facts.
func readQuantity(ctx context.Context, c *Client, view bool, method string, params ...any) (*big.Int, CallResult, error) {
	return operation(c, ctx, view, func(opCtx context.Context) (*big.Int, CallResult, error) {
		return readQuantityExec(opCtx, c, method, params...)
	})
}

func readQuantityExec(ctx context.Context, c *Client, method string, params ...any) (*big.Int, CallResult, error) {
	decision, err := c.callTyped(ctx, method, params...)
	if err != nil {
		decision.Value = nil
		return nil, CallResult{Decision: decisionOf(decision)}, err
	}
	value, projectErr := c.decodedQuantity(method, decision)
	return value, CallResult{Decision: decisionOf(decision)}, projectErr
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
func (c *Client) ChainID(ctx context.Context) (*big.Int, CallResult, error) {
	return readQuantity(ctx, c, false, methods.MethodChainID)
}

// NetworkID returns the corroborated legacy chain identity (net_version
// spells in decimal what eth_chainId spells in hex).
func (c *Client) NetworkID(ctx context.Context) (*big.Int, CallResult, error) {
	return readQuantity(ctx, c, false, methods.MethodNetVersion)
}

// BalanceAt returns the corroborated balance of account at blockNumber; nil
// means the currently corroborated reference behind "latest".
func (c *Client) BalanceAt(ctx context.Context, account ethapi.Address, blockNumber *big.Int) (*big.Int, CallResult, error) {
	return operation(c, ctx, false, func(opCtx context.Context) (*big.Int, CallResult, error) {
		return c.balanceAtExec(opCtx, account, blockNumber)
	})
}

func (c *Client) balanceAtExec(ctx context.Context, account ethapi.Address, blockNumber *big.Int) (*big.Int, CallResult, error) {
	account, err := validateAddress(account)
	if err != nil {
		return nil, CallResult{}, err
	}
	blockArg, err := checkedBlockNumArg(blockNumber)
	if err != nil {
		return nil, CallResult{}, err
	}
	value, decision, err := aggregateInto[hexutil.Big](ctx, c, methods.MethodGetBalance, string(account), blockArg)
	if err != nil {
		return nil, CallResult{Decision: decisionOf(decision)}, err
	}
	return (*big.Int)(value), CallResult{Decision: decisionOf(decision)}, nil
}

// BalanceAtHash returns the corroborated balance of account in the state at
// blockHash. The hash pin is sent as EIP-1898 with requireCanonical=false,
// matching go-ethereum's BalanceAtHash contract.
func (c *Client) BalanceAtHash(ctx context.Context, account ethapi.Address, blockHash ethapi.Hash) (*big.Int, CallResult, error) {
	return operation(c, ctx, false, func(opCtx context.Context) (*big.Int, CallResult, error) {
		return c.balanceAtHashExec(opCtx, account, blockHash)
	})
}

func (c *Client) balanceAtHashExec(ctx context.Context, account ethapi.Address, blockHash ethapi.Hash) (*big.Int, CallResult, error) {
	account, err := validateAddress(account)
	if err != nil {
		return nil, CallResult{}, err
	}
	blockHash, err = validateHash(blockHash)
	if err != nil {
		return nil, CallResult{}, err
	}
	value, decision, err := aggregateInto[hexutil.Big](ctx, c, methods.MethodGetBalance, string(account), hashBlockArg(blockHash))
	if err != nil {
		return nil, CallResult{Decision: decisionOf(decision)}, err
	}
	return (*big.Int)(value), CallResult{Decision: decisionOf(decision)}, nil
}

// NonceAt returns the corroborated account nonce of account at blockNumber;
// nil pins the moving tag to the currently corroborated reference.
func (c *Client) NonceAt(ctx context.Context, account ethapi.Address, blockNumber *big.Int) (uint64, CallResult, error) {
	return operation(c, ctx, false, func(opCtx context.Context) (uint64, CallResult, error) {
		return c.nonceAtExec(opCtx, account, blockNumber)
	})
}

func (c *Client) nonceAtExec(ctx context.Context, account ethapi.Address, blockNumber *big.Int) (uint64, CallResult, error) {
	account, err := validateAddress(account)
	if err != nil {
		return 0, CallResult{}, err
	}
	blockArg, err := checkedBlockNumArg(blockNumber)
	if err != nil {
		return 0, CallResult{}, err
	}
	nonce, decision, err := aggregateInto[hexutil.Uint64](ctx, c, methods.MethodGetTransactionCount, string(account), blockArg)
	if err != nil {
		return 0, CallResult{Decision: decisionOf(decision)}, err
	}
	return uint64(*nonce), CallResult{Decision: decisionOf(decision)}, nil
}

// NonceAtHash returns the corroborated account nonce in the state at
// blockHash. The hash pin is sent as EIP-1898 with requireCanonical=false.
func (c *Client) NonceAtHash(ctx context.Context, account ethapi.Address, blockHash ethapi.Hash) (uint64, CallResult, error) {
	return operation(c, ctx, false, func(opCtx context.Context) (uint64, CallResult, error) {
		return c.nonceAtHashExec(opCtx, account, blockHash)
	})
}

func (c *Client) nonceAtHashExec(ctx context.Context, account ethapi.Address, blockHash ethapi.Hash) (uint64, CallResult, error) {
	account, err := validateAddress(account)
	if err != nil {
		return 0, CallResult{}, err
	}
	blockHash, err = validateHash(blockHash)
	if err != nil {
		return 0, CallResult{}, err
	}
	nonce, decision, err := aggregateInto[hexutil.Uint64](ctx, c, methods.MethodGetTransactionCount, string(account), hashBlockArg(blockHash))
	if err != nil {
		return 0, CallResult{Decision: decisionOf(decision)}, err
	}
	return uint64(*nonce), CallResult{Decision: decisionOf(decision)}, nil
}

// StorageAt returns the corroborated value of key in account's storage at
// blockNumber; nil pins the moving tag to the currently corroborated reference.
func (c *Client) StorageAt(ctx context.Context, account ethapi.Address, key ethapi.Hash, blockNumber *big.Int) ([]byte, CallResult, error) {
	return operation(c, ctx, false, func(opCtx context.Context) ([]byte, CallResult, error) {
		return c.storageAtExec(opCtx, account, key, blockNumber)
	})
}

func (c *Client) storageAtExec(ctx context.Context, account ethapi.Address, key ethapi.Hash, blockNumber *big.Int) ([]byte, CallResult, error) {
	account, key, err := validateAddressHash(account, key)
	if err != nil {
		return nil, CallResult{}, err
	}
	blockArg, err := checkedBlockNumArg(blockNumber)
	if err != nil {
		return nil, CallResult{}, err
	}
	value, decision, err := aggregateInto[hexutil.Bytes](ctx, c, methods.MethodGetStorageAt, string(account), string(key), blockArg)
	if err != nil {
		return nil, CallResult{Decision: decisionOf(decision)}, err
	}
	return *value, CallResult{Decision: decisionOf(decision)}, nil
}

// StorageAtHash returns the corroborated value of key in account's storage
// at blockHash. The hash pin is sent as EIP-1898 with
// requireCanonical=false.
func (c *Client) StorageAtHash(ctx context.Context, account ethapi.Address, key ethapi.Hash, blockHash ethapi.Hash) ([]byte, CallResult, error) {
	return operation(c, ctx, false, func(opCtx context.Context) ([]byte, CallResult, error) {
		return c.storageAtHashExec(opCtx, account, key, blockHash)
	})
}

func (c *Client) storageAtHashExec(ctx context.Context, account ethapi.Address, key ethapi.Hash, blockHash ethapi.Hash) ([]byte, CallResult, error) {
	account, key, err := validateAddressHash(account, key)
	if err != nil {
		return nil, CallResult{}, err
	}
	blockHash, err = validateHash(blockHash)
	if err != nil {
		return nil, CallResult{}, err
	}
	value, decision, err := aggregateInto[hexutil.Bytes](ctx, c, methods.MethodGetStorageAt, string(account), string(key), hashBlockArg(blockHash))
	if err != nil {
		return nil, CallResult{Decision: decisionOf(decision)}, err
	}
	return *value, CallResult{Decision: decisionOf(decision)}, nil
}

// CodeAt returns the corroborated contract code of account at blockNumber; nil
// pins the moving tag to the currently corroborated reference. An externally
// owned account answers empty code, not an error.
func (c *Client) CodeAt(ctx context.Context, account ethapi.Address, blockNumber *big.Int) ([]byte, CallResult, error) {
	return operation(c, ctx, false, func(opCtx context.Context) ([]byte, CallResult, error) {
		return c.codeAtExec(opCtx, account, blockNumber)
	})
}

func (c *Client) codeAtExec(ctx context.Context, account ethapi.Address, blockNumber *big.Int) ([]byte, CallResult, error) {
	account, err := validateAddress(account)
	if err != nil {
		return nil, CallResult{}, err
	}
	blockArg, err := checkedBlockNumArg(blockNumber)
	if err != nil {
		return nil, CallResult{}, err
	}
	code, decision, err := aggregateInto[hexutil.Bytes](ctx, c, methods.MethodGetCode, string(account), blockArg)
	if err != nil {
		return nil, CallResult{Decision: decisionOf(decision)}, err
	}
	return *code, CallResult{Decision: decisionOf(decision)}, nil
}

// CodeAtHash returns the corroborated contract code of account at blockHash.
// The hash pin is sent as EIP-1898 with requireCanonical=false.
func (c *Client) CodeAtHash(ctx context.Context, account ethapi.Address, blockHash ethapi.Hash) ([]byte, CallResult, error) {
	return operation(c, ctx, false, func(opCtx context.Context) ([]byte, CallResult, error) {
		return c.codeAtHashExec(opCtx, account, blockHash)
	})
}

func (c *Client) codeAtHashExec(ctx context.Context, account ethapi.Address, blockHash ethapi.Hash) ([]byte, CallResult, error) {
	account, err := validateAddress(account)
	if err != nil {
		return nil, CallResult{}, err
	}
	blockHash, err = validateHash(blockHash)
	if err != nil {
		return nil, CallResult{}, err
	}
	code, decision, err := aggregateInto[hexutil.Bytes](ctx, c, methods.MethodGetCode, string(account), hashBlockArg(blockHash))
	if err != nil {
		return nil, CallResult{Decision: decisionOf(decision)}, err
	}
	return *code, CallResult{Decision: decisionOf(decision)}, nil
}

// validateAddressHash validates one address together with one storage-slot
// hash — the shared shape of every StorageAt entry. The slot keeps the
// fixed-32-byte Hash contract.
func validateAddressHash(account ethapi.Address, key ethapi.Hash) (ethapi.Address, ethapi.Hash, error) {
	account, err := validateAddress(account)
	if err != nil {
		return "", "", err
	}
	key, err = validateHash(key)
	if err != nil {
		return "", "", err
	}
	return account, key, nil
}

// hashBlockArg is the explicit EIP-1898 form used by the hash-specific
// go-ethereum methods. Keeping the false flag present is part of the wire
// contract: providers must see {blockHash, requireCanonical:false}. The
// hash must already be validated; callers pass the canonical spelling.
func hashBlockArg(blockHash ethapi.Hash) map[string]any {
	return map[string]any{
		"blockHash":        string(blockHash),
		"requireCanonical": false,
	}
}

// BlockByHash returns the corroborated block with the given hash.
func (c *Client) BlockByHash(ctx context.Context, hash ethapi.Hash) (*ethapi.Block, CallResult, error) {
	return operation(c, ctx, false, func(opCtx context.Context) (*ethapi.Block, CallResult, error) {
		return c.blockByHashExec(opCtx, hash)
	})
}

// blockByHashExec is the shared block-by-hash body of both facades: the
// request hash is validated, then the dedicated by-hash method answers.
func (c *Client) blockByHashExec(ctx context.Context, hash ethapi.Hash) (*ethapi.Block, CallResult, error) {
	hash, err := validateHash(hash)
	if err != nil {
		return nil, CallResult{}, err
	}
	block, decision, err := c.blockAt(ctx, string(hash))
	return block, CallResult{Decision: decisionOf(decision)}, err
}

// BlockByNumber returns the corroborated block at number. nil and explicit
// moving tags (-2 latest, -3 finalized, -4 safe) pin that tag's own reference
// first and fetch by hash. Explicit heights, including earliest (-5 as 0x0),
// fetch directly. Pending (-1) is rejected locally: there is no pending-block
// typed projection, and -1 is not rewritten into an anchored or ordinary block.
func (c *Client) BlockByNumber(ctx context.Context, number *big.Int) (*ethapi.Block, CallResult, error) {
	return operation(c, ctx, false, func(opCtx context.Context) (*ethapi.Block, CallResult, error) {
		return c.blockByNumberExec(opCtx, number)
	})
}

func (c *Client) blockByNumberExec(ctx context.Context, number *big.Int) (*ethapi.Block, CallResult, error) {
	if number != nil && number.Sign() >= 0 && !number.IsUint64() {
		return nil, CallResult{}, fmt.Errorf("block number exceeds supported height range: %w", ErrInvalidRequest)
	}
	if number != nil && number.Sign() < 0 {
		return c.blockByNegativeNumber(ctx, number)
	}
	if number == nil {
		ref, err := c.establishAnchor(ctx, "")
		if err != nil {
			return nil, CallResult{Decision: &rpc.Decision{Status: rpc.Unknown, Reference: anchorReference(ref)}}, err
		}
		block, decision, blockErr := c.blockAt(ctx, ref.Hash)
		attachEstablishedReference(&decision, ref)
		return block, CallResult{Decision: decisionOf(decision)}, blockErr
	}
	blockArg, err := checkedBlockNumArg(number)
	if err != nil {
		return nil, CallResult{}, err
	}
	block, decision, blockErr := c.blockAt(ctx, blockArg)
	return block, CallResult{Decision: decisionOf(decision)}, blockErr
}

// blockByNegativeNumber resolves Geth's negative block numbers without
// collapsing them into one tag. -2/-3/-4 establish their own level's
// reference (never downgraded, never fetched as a moving tag) and read by
// hash. -1 is a local rejection: pending has no typed block projection, so
// it is not anchored and not sent as a business request or as "-0x1".
// -5 is earliest, an explicit height 0x0. Anything else — including values
// outside int64 range — fails locally before any upstream contact.
func (c *Client) blockByNegativeNumber(ctx context.Context, number *big.Int) (*ethapi.Block, CallResult, error) {
	if !number.IsInt64() {
		return nil, CallResult{}, fmt.Errorf("ethguard: unsupported block number %s: %w", number.String(), ErrInvalidRequest)
	}
	switch number.Int64() {
	case -1:
		return nil, CallResult{}, fmt.Errorf("ethguard: pending block typed projection is not supported: %w", ErrInvalidRequest)
	case -2, -3, -4:
		tags := map[int64]string{-2: "latest", -3: "finalized", -4: "safe"}
		tag := tags[number.Int64()]
		ref, err := c.establishAnchor(ctx, tag)
		if err != nil {
			return nil, CallResult{Decision: &rpc.Decision{Status: rpc.Unknown, Reference: anchorReference(ref)}}, err
		}
		block, decision, blockErr := c.blockAt(ctx, ref.Hash)
		attachEstablishedReference(&decision, ref)
		return block, CallResult{Decision: decisionOf(decision)}, blockErr
	case -5:
		block, decision, blockErr := c.blockAt(ctx, "0x0")
		return block, CallResult{Decision: decisionOf(decision)}, blockErr
	default:
		return nil, CallResult{}, fmt.Errorf("ethguard: unsupported block number %s: %w", number.String(), ErrInvalidRequest)
	}
}

// HeaderByHash returns the corroborated header with the given hash. It uses
// the same block admission and request binding as BlockByHash, but asks the
// provider for a header-only response so transaction and uncle bodies are not
// required or downloaded.
func (c *Client) HeaderByHash(ctx context.Context, hash ethapi.Hash) (*ethapi.Header, CallResult, error) {
	return operation(c, ctx, false, func(opCtx context.Context) (*ethapi.Header, CallResult, error) {
		return c.headerByHashExec(opCtx, hash)
	})
}

// headerByHashExec is the shared header-only body of both facades.
func (c *Client) headerByHashExec(ctx context.Context, hash ethapi.Hash) (*ethapi.Header, CallResult, error) {
	hash, err := validateHash(hash)
	if err != nil {
		return nil, CallResult{}, err
	}
	header, decision, err := c.headerAt(ctx, string(hash))
	return header, CallResult{Decision: decisionOf(decision)}, err
}

// HeaderByNumber returns a corroborated header at number. Moving tags are
// pinned through the same reference establishment as BlockByNumber; pending
// remains a local rejection because a pending header has no stable typed
// inclusion reference in EthGuard's current contract.
func (c *Client) HeaderByNumber(ctx context.Context, number *big.Int) (*ethapi.Header, CallResult, error) {
	return operation(c, ctx, false, func(opCtx context.Context) (*ethapi.Header, CallResult, error) {
		return c.headerByNumberExec(opCtx, number)
	})
}

func (c *Client) headerByNumberExec(ctx context.Context, number *big.Int) (*ethapi.Header, CallResult, error) {
	if number != nil && number.Sign() >= 0 && !number.IsUint64() {
		return nil, CallResult{}, fmt.Errorf("block number exceeds supported height range: %w", ErrInvalidRequest)
	}
	if number == nil {
		ref, err := c.establishAnchor(ctx, "")
		if err != nil {
			return nil, CallResult{Decision: &rpc.Decision{Status: rpc.Unknown, Reference: anchorReference(ref)}}, err
		}
		header, decision, headerErr := c.headerAt(ctx, ref.Hash)
		attachEstablishedReference(&decision, ref)
		return header, CallResult{Decision: decisionOf(decision)}, headerErr
	}
	if number.Sign() < 0 {
		if !number.IsInt64() {
			return nil, CallResult{}, fmt.Errorf("ethguard: unsupported block number %s: %w", number.String(), ErrInvalidRequest)
		}
		switch number.Int64() {
		case -1:
			return nil, CallResult{}, fmt.Errorf("ethguard: pending header typed projection is not supported: %w", ErrInvalidRequest)
		case -2, -3, -4:
			tags := map[int64]string{-2: "latest", -3: "finalized", -4: "safe"}
			ref, err := c.establishAnchor(ctx, tags[number.Int64()])
			if err != nil {
				return nil, CallResult{Decision: &rpc.Decision{Status: rpc.Unknown, Reference: anchorReference(ref)}}, err
			}
			header, decision, headerErr := c.headerAt(ctx, ref.Hash)
			attachEstablishedReference(&decision, ref)
			return header, CallResult{Decision: decisionOf(decision)}, headerErr
		case -5:
			header, decision, headerErr := c.headerAt(ctx, "0x0")
			return header, CallResult{Decision: decisionOf(decision)}, headerErr
		default:
			return nil, CallResult{}, fmt.Errorf("ethguard: unsupported block number %s: %w", number.String(), ErrInvalidRequest)
		}
	}
	blockArg, err := checkedBlockNumArg(number)
	if err != nil {
		return nil, CallResult{}, err
	}
	header, decision, headerErr := c.headerAt(ctx, blockArg)
	return header, CallResult{Decision: decisionOf(decision)}, headerErr
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
func (c *Client) BlockNumber(ctx context.Context) (uint64, CallResult, error) {
	return operation(c, ctx, false, c.blockNumberExec)
}

func (c *Client) blockNumberExec(ctx context.Context) (uint64, CallResult, error) {
	ref, err := c.establishAnchor(ctx, "")
	if err != nil {
		return 0, CallResult{Decision: &rpc.Decision{Status: rpc.Unknown, Reference: anchorReference(ref)}}, err
	}
	// The height lookup rides eth_getBlockByHash even though a "height of
	// this hash" read feels like eth_getBlockByNumber: the first live soak
	// run caught public endpoints rejecting a 32-byte hash in the number
	// position ("hex number > 64 bits"), so hash lookups use the
	// hash-addressed method, full stop. blockAt applies the same dispatch and
	// binds the answer to the requested reference.
	block, decision, blockErr := c.blockAt(ctx, ref.Hash)
	attachEstablishedReference(&decision, ref)
	if blockErr != nil {
		return 0, CallResult{Decision: decisionOf(decision)}, blockErr
	}
	// The height converts from the canonical quantity exactly: a value
	// beyond the uint64 range is reported, never truncated.
	height, heightErr := typed.QuantityHeight(block.Number)
	if heightErr != nil {
		return 0, CallResult{Decision: decisionOf(decision)}, fmt.Errorf("%w: block height %s: %v",
			rpc.ErrMalformedResponse, block.Number, heightErr)
	}
	return height, CallResult{Decision: decisionOf(decision)}, nil
}

// blockAt fetches and strictly compares one block identified explicitly by
// hash or height — never by a moving tag. Which method the reference rides
// is a wire-portability decision: the first live soak run caught public
// endpoints rejecting a 32-byte hash in eth_getBlockByNumber's number
// position ("hex number > 64 bits"), so hash-looking references take the
// hash-addressed method and everything else the number-addressed one.
// blockAt fetches one block by explicit reference and returns the business
// value with the underlying decision. The projection and binding failures
// keep the aggregated facts — the aggregation itself succeeded.
func (c *Client) blockAt(ctx context.Context, ref string) (*ethapi.Block, rpc.Decision, error) {
	method := methods.MethodGetBlockByNumber
	if len(ref) == 2+2*common.HashLength && strings.HasPrefix(ref, "0x") {
		method = methods.MethodGetBlockByHash
	}
	decision, err := c.callTyped(ctx, method, ref, true)
	if err != nil {
		decision.Value = nil
		return nil, decision, err
	}
	block, err := typed.DecodeBlockAdmitted(decision.Value)
	if err != nil {
		return nil, decision, err
	}
	if len(ref) == 2+2*common.HashLength && strings.HasPrefix(ref, "0x") {
		if string(block.Hash) != strings.ToLower(ref) {
			return nil, decision, fmt.Errorf("%w: block hash %s does not match requested hash %s",
				rpc.ErrMalformedResponse, block.Hash, ref)
		}
		return block, decision, nil
	}
	requested, ok := new(big.Int).SetString(strings.TrimPrefix(ref, "0x"), 16)
	if !ok {
		return nil, decision, fmt.Errorf("%w: block reference %s is not a height", rpc.ErrMalformedResponse, ref)
	}
	height, ok := new(big.Int).SetString(strings.TrimPrefix(string(block.Number), "0x"), 16)
	if !ok || height.Cmp(requested) != 0 {
		return nil, decision, fmt.Errorf("%w: block height %s does not match requested reference %s",
			rpc.ErrMalformedResponse, block.Number, ref)
	}
	return block, decision, nil
}

// headerAt fetches and strictly compares one header identified explicitly by
// hash or height. It shares the block plans and admission rules with blockAt,
// changing only the fullTx wire flag and the typed projection.
func (c *Client) headerAt(ctx context.Context, ref string) (*ethapi.Header, rpc.Decision, error) {
	method := methods.MethodGetBlockByNumber
	if len(ref) == 2+2*common.HashLength && strings.HasPrefix(ref, "0x") {
		method = methods.MethodGetBlockByHash
	}
	decision, err := c.callTyped(ctx, method, ref, false)
	if err != nil {
		decision.Value = nil
		return nil, decision, err
	}
	header, err := typed.DecodeBlockHeaderOnly(decision.Value)
	if err != nil {
		return nil, decision, err
	}
	if len(ref) == 2+2*common.HashLength && strings.HasPrefix(ref, "0x") {
		if string(header.Hash) != strings.ToLower(ref) {
			return nil, decision, fmt.Errorf("%w: header hash %s does not match requested hash %s",
				rpc.ErrMalformedResponse, header.Hash, ref)
		}
		return header, decision, nil
	}
	requested, ok := new(big.Int).SetString(strings.TrimPrefix(ref, "0x"), 16)
	if !ok {
		return nil, decision, fmt.Errorf("%w: header reference %s is not a height", rpc.ErrMalformedResponse, ref)
	}
	height, ok := new(big.Int).SetString(strings.TrimPrefix(string(header.Number), "0x"), 16)
	if !ok || height.Cmp(requested) != 0 {
		return nil, decision, fmt.Errorf("%w: header height %s does not match requested reference %s",
			rpc.ErrMalformedResponse, header.Number, ref)
	}
	return header, decision, nil
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

// freezeBlockReference re-validates one caller-supplied generated reference
// through the generated constructor and returns the frozen canonical value.
// Directly constructed unions can select zero or several branches; the
// marshal-reparse cycle is the single acceptance gate before any wire
// spelling is derived.
func freezeBlockReference(blockRef ethapi.BlockReference) (ethapi.BlockReference, error) {
	raw, err := json.Marshal(blockRef)
	if err != nil {
		return ethapi.BlockReference{}, fmt.Errorf("ethguard: block reference: %w", ErrInvalidRequest)
	}
	frozen, err := ethapi.NewBlockReference(raw)
	if err != nil {
		return ethapi.BlockReference{}, fmt.Errorf("ethguard: block reference: %w", ErrInvalidRequest)
	}
	return frozen, nil
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

// isBareBlockHash reports whether one bare reference string is the
// 32-byte-word spelling: exactly "0x" plus 64 lowercase-or-uppercase hex
// characters. This is the overlap between the quantity and hash anyOf
// branches, resolved by shape, not by first-match parsing.
func isBareBlockHash(ref string) bool {
	if len(ref) != 2+2*common.HashLength || !strings.HasPrefix(ref, "0x") {
		return false
	}
	for _, r := range ref[2:] {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f' || r >= 'A' && r <= 'F') {
			return false
		}
	}
	return true
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

// SuggestGasTipCap returns the fleet's lower-median priority-fee estimate —
// a variable result resolved by robust selection, never claimed corroborated.
func (c *Client) SuggestGasTipCap(ctx context.Context) (*big.Int, CallResult, error) {
	return readQuantity(ctx, c, false, methods.MethodMaxPriorityFeePerGas)
}

// SuggestGasPrice returns the fleet's lower-median gas price — a variable
// result resolved by robust selection, never claimed corroborated.
func (c *Client) SuggestGasPrice(ctx context.Context) (*big.Int, CallResult, error) {
	return readQuantity(ctx, c, false, methods.MethodGasPrice)
}

// CallContract returns the fleet-corroborated result of executing msg at
// blockNumber; nil pins the moving tag to the currently corroborated reference.
// A fleet-corroborated revert is an observation, not a pipeline failure: it is
// returned as a *RevertError carrying the revert context, while the
// corroboration claim stays on the decision evidence.
func (c *Client) CallContract(ctx context.Context, msg ethapi.CallMsg, blockNumber *big.Int) ([]byte, CallResult, error) {
	return operation(c, ctx, false, func(opCtx context.Context) ([]byte, CallResult, error) {
		return c.callContractExec(opCtx, msg, blockNumber)
	})
}

func (c *Client) callContractExec(ctx context.Context, msg ethapi.CallMsg, blockNumber *big.Int) ([]byte, CallResult, error) {
	blockArg, err := checkedBlockNumArg(blockNumber)
	if err != nil {
		return nil, CallResult{}, err
	}
	call, err := toCallArg(msg)
	if err != nil {
		return nil, CallResult{}, err
	}
	decision, err := c.callTyped(ctx, methods.MethodCall, call, blockArg)
	if err != nil {
		decision.Value = nil
		return nil, CallResult{Decision: decisionOf(decision)}, err
	}
	data, err := decodeCallDecision(decision)
	return data, CallResult{Decision: decisionOf(decision)}, err
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

// toCallArg validates the generated request model and freezes its complete
// official fields into ordinary JSON values before reference preparation.
func toCallArg(msg ethapi.CallMsg) (map[string]any, error) {
	raw, err := json.Marshal(msg)
	if err != nil {
		return nil, fmt.Errorf("%w: call message: %v", rpc.ErrInvalidRequest, err)
	}
	checked, err := ethapi.NewCallMsg(raw)
	if err != nil {
		return nil, fmt.Errorf("%w: call message: %v", rpc.ErrInvalidRequest, err)
	}
	raw, err = json.Marshal(checked)
	if err != nil {
		return nil, fmt.Errorf("%w: call message: %v", rpc.ErrInvalidRequest, err)
	}
	var arg map[string]any
	// Open request members can contain arbitrary exact JSON numbers. The
	// generated parser has already checked this JSON; decoding it into
	// interface values must not round those members through float64.
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&arg); err != nil {
		return nil, fmt.Errorf("%w: call message: %v", rpc.ErrInvalidRequest, err)
	}
	return arg, nil
}

// EstimateGas returns the fleet's lower-median gas estimate for msg at the
// currently corroborated reference — a variable result, Selected at best, never
// claimed corroborated. A fleet that unanimously fails estimation fails as
// no-valid-response; the revert is never laundered into a number.
func (c *Client) EstimateGas(ctx context.Context, msg ethapi.CallMsg) (uint64, CallResult, error) {
	return operation(c, ctx, false, func(opCtx context.Context) (uint64, CallResult, error) {
		return c.estimateGasExec(opCtx, msg)
	})
}

func (c *Client) estimateGasExec(ctx context.Context, msg ethapi.CallMsg) (uint64, CallResult, error) {
	call, err := toCallArg(msg)
	if err != nil {
		return 0, CallResult{}, err
	}
	decision, err := c.callTyped(ctx, methods.MethodEstimateGas, call, "latest")
	if err != nil {
		decision.Value = nil
		return 0, CallResult{Decision: decisionOf(decision)}, err
	}
	gas, err := typed.DecodeGasEstimate(decision.Value)
	return gas, CallResult{Decision: decisionOf(decision)}, err
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
	arg, err := toFilterArg(q)
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

// toFilterArg re-validates the generated filter union and spells the wire
// argument from the constructor's canonical copy: the re-validated model is
// re-marshalled and that frozen canonical spelling is what the request
// carries, so directly constructed values ride the wire canonicalized
// (lowercase addresses and hashes) rather than in their original case.
// Address, topics and open-object extras pass through the canonical bytes
// exactly, never through float64. The range branch keeps the project
// defaults — a missing fromBlock stays "0x0" and a missing toBlock "latest",
// exactly as before the typed migration — rewrites the earliest tag to the
// explicit 0x0 height, and rejects pending bounds locally. The hash branch
// is an open object, so from/to members cannot be schema-forbidden there;
// the method's own mutual exclusion rejects them as an input error.
func toFilterArg(q ethapi.FilterQuery) (map[string]any, error) {
	raw, err := json.Marshal(q)
	if err != nil {
		return nil, fmt.Errorf("%w: filter query: %v", ErrInvalidRequest, err)
	}
	checked, err := ethapi.NewFilterQuery(raw)
	if err != nil {
		return nil, fmt.Errorf("%w: filter query: %v", ErrInvalidRequest, err)
	}
	// Consume the constructor's canonical copy: the original bytes are
	// discarded after validation, so the wire spelling is the audited
	// canonical one.
	canonical, err := json.Marshal(checked)
	if err != nil {
		return nil, fmt.Errorf("%w: filter query: %v", ErrInvalidRequest, err)
	}
	var members map[string]any
	decoder := json.NewDecoder(bytes.NewReader(canonical))
	decoder.UseNumber()
	if err := decoder.Decode(&members); err != nil {
		return nil, fmt.Errorf("%w: filter query: %v", ErrInvalidRequest, err)
	}
	arg := make(map[string]any, len(members))
	if rawHash, present := members["blockHash"]; present {
		// Hash branch: the schema leaves the object open, so the method's
		// own contract rejects range members instead of the parser.
		for _, banned := range []string{"fromBlock", "toBlock"} {
			if _, present := members[banned]; present {
				return nil, fmt.Errorf("ethguard: cannot specify both block hash and from/to blocks: %w", ErrInvalidRequest)
			}
		}
		hashText, _ := rawHash.(string)
		arg["blockHash"] = hashText
	} else {
		fromBlock := "0x0"
		if raw, present := members["fromBlock"]; present {
			if fromBlock, err = filterBound(raw); err != nil {
				return nil, err
			}
		}
		toBlock := "latest"
		if raw, present := members["toBlock"]; present {
			if toBlock, err = filterBound(raw); err != nil {
				return nil, err
			}
		}
		arg["fromBlock"] = fromBlock
		arg["toBlock"] = toBlock
	}
	for _, field := range []string{"address", "topics"} {
		if raw, present := members[field]; present {
			arg[field] = raw
		}
	}
	// Open-object extras ride the frozen canonical bytes exactly as
	// declared — the generated parser has already checked their JSON safety.
	for key, value := range members {
		switch key {
		case "address", "topics", "fromBlock", "toBlock", "blockHash":
			continue
		}
		arg[key] = value
	}
	return arg, nil
}

// filterBound spells one declared range bound. Canonical quantities pass
// through; the earliest tag keeps the explicit 0x0 height contract; pending
// is rejected locally before any reference establishment.
func filterBound(raw any) (string, error) {
	text, ok := raw.(string)
	if !ok {
		return "", fmt.Errorf("%w: filter block bound must be a string", ErrInvalidRequest)
	}
	if strings.EqualFold(text, "pending") {
		return "", fmt.Errorf("ethguard: pending filter bound is not supported: %w", ErrInvalidRequest)
	}
	if strings.EqualFold(text, "earliest") {
		return "0x0", nil
	}
	return text, nil
}

// PeerCount returns one healthy source's count of its own peers — an
// upstream-local result, selected not compared.
func (c *Client) PeerCount(ctx context.Context) (uint64, CallResult, error) {
	return operation(c, ctx, false, c.peerCountExec)
}

func (c *Client) peerCountExec(ctx context.Context) (uint64, CallResult, error) {
	decision, err := c.callTyped(ctx, methods.MethodPeerCount)
	if err != nil {
		decision.Value = nil
		return 0, CallResult{Decision: decisionOf(decision)}, err
	}
	value, projectErr := c.decodedQuantity(methods.MethodPeerCount, decision)
	if projectErr != nil {
		return 0, CallResult{Decision: decisionOf(decision)}, projectErr
	}
	if !value.IsUint64() {
		return 0, CallResult{Decision: decisionOf(decision)}, fmt.Errorf("%w: invalid %s %q", rpc.ErrMalformedResponse, methods.MethodPeerCount, decision.Value)
	}
	return value.Uint64(), CallResult{Decision: decisionOf(decision)}, nil
}

// SyncProgress reports the consulted source's sync state; nil means it
// considers itself fully synced.
func (c *Client) SyncProgress(ctx context.Context) (*ethapi.SyncProgress, CallResult, error) {
	return operation(c, ctx, false, c.syncProgressExec)
}

func (c *Client) syncProgressExec(ctx context.Context) (*ethapi.SyncProgress, CallResult, error) {
	decision, err := c.callTyped(ctx, methods.MethodSyncing)
	if err != nil {
		decision.Value = nil
		return nil, CallResult{Decision: decisionOf(decision)}, err
	}
	progress, projectErr := typed.DecodeSyncProgress(decision.Value)
	return progress, CallResult{Decision: decisionOf(decision)}, projectErr
}

// FeeHistory returns the fleet-corroborated fee history ending at the
// corroborated reference: lastBlock nil pins the moving "latest" tag, an
// explicit number passes through. The range is chain-determined once
// lastBlock is pinned, so the decision is Corroborated when sources agree;
// cross-client percentile rounding of Reward is the known liveness gate and
// fails closed as conflict until the soak measures it.
func (c *Client) FeeHistory(ctx context.Context, blockCount uint64, lastBlock *big.Int, rewardPercentiles []float64) (*ethapi.FeeHistoryResult, CallResult, error) {
	return operation(c, ctx, false, func(opCtx context.Context) (*ethapi.FeeHistoryResult, CallResult, error) {
		return c.feeHistoryExec(opCtx, blockCount, lastBlock, rewardPercentiles)
	})
}

func (c *Client) feeHistoryExec(ctx context.Context, blockCount uint64, lastBlock *big.Int, rewardPercentiles []float64) (*ethapi.FeeHistoryResult, CallResult, error) {
	lastRef := "latest"
	if lastBlock != nil {
		lastRef = hexutil.EncodeBig(lastBlock)
	}
	decision, err := c.callTyped(ctx, methods.MethodFeeHistory, blockCount, lastRef, rewardPercentiles)
	if err != nil {
		decision.Value = nil
		return nil, CallResult{Decision: decisionOf(decision)}, err
	}
	history, projectErr := typed.DecodeFeeHistory(decision.Value)
	return history, CallResult{Decision: decisionOf(decision)}, projectErr
}

// BlobBaseFee returns the fleet's lower-median blob base fee (EIP-4844) — a
// variable result, so the decision is Selected at best, with outliers kept as
// evidence.
func (c *Client) BlobBaseFee(ctx context.Context) (*big.Int, CallResult, error) {
	return readQuantity(ctx, c, false, methods.MethodBlobBaseFee)
}

// CreateAccessList creates an EIP-2930 access list for a transaction at the
// corroborated reference block or an explicitly provided block argument.
// This is an EthGuard typed extension: go-ethereum's client does not expose
// eth_createAccessList, so the return value carries no decision evidence.
func (c *Client) CreateAccessList(ctx context.Context, msg any, blockNrOrHash ...any) (*ethapi.AccessListResult, CallResult, error) {
	return operation(c, ctx, false, func(opCtx context.Context) (*ethapi.AccessListResult, CallResult, error) {
		params := []any{msg}
		if len(blockNrOrHash) > 0 {
			params = append(params, blockNrOrHash[0])
		}
		decision, err := c.callTyped(opCtx, methods.MethodCreateAccessList, params...)
		if err != nil {
			decision.Value = nil
			return nil, CallResult{Decision: decisionOf(decision)}, err
		}
		result, err := typed.DecodeAccessListResult(decision.Value)
		return result, CallResult{Decision: decisionOf(decision)}, err
	})
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
