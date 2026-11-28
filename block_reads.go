package ethguard

import (
	"context"
	"fmt"
	"math/big"
	"strings"

	"github.com/ethereum/go-ethereum/common"

	"github.com/codermaybe/ethguard/ethapi"
	"github.com/codermaybe/ethguard/internal/methods"
	"github.com/codermaybe/ethguard/internal/typed"
	"github.com/codermaybe/ethguard/rpc"
)

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
		ref, err := c.establishBusinessReference(ctx, "")
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
		ref, err := c.establishBusinessReference(ctx, tag)
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
		ref, err := c.establishBusinessReference(ctx, "")
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
			ref, err := c.establishBusinessReference(ctx, tags[number.Int64()])
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

// BlockNumber returns the height of the current business reference, with a
// Forwarded decision on a single-source client. In corroborating mode it is a
// derivation over corroborated primitives, with the following selection rule:
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
// Reference establishment follows the client's configured anchoring policy,
// such as WithFinalizedAnchoring.
func (c *Client) BlockNumber(ctx context.Context) (uint64, CallResult, error) {
	return operation(c, ctx, false, c.blockNumberExec)
}

func (c *Client) blockNumberExec(ctx context.Context) (uint64, CallResult, error) {
	ref, err := c.establishBusinessReference(ctx, "")
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
// The business value is returned with the underlying decision. Projection
// and binding failures keep the aggregated facts.
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
