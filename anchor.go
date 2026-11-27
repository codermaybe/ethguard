package ethguard

import (
	"context"
	"fmt"
	"strings"

	"github.com/codermaybe/ethguard/internal/runtime"
	"github.com/codermaybe/ethguard/rpc"
)

// The anchor facade is query-only: a Client owns its runtime-built service,
// and callers may ask it for established references but cannot inject or
// replace one. The establishment algorithms live in internal/runtime; the
// option below tunes the preset the composition root installs.

type FinalizedAnchorOption = runtime.FinalizedAnchorOption

// WithMaxFinalizedLag bounds lag behind the lower-median reported height.
func WithMaxFinalizedLag(blocks uint64) FinalizedAnchorOption {
	return runtime.WithMaxFinalizedLag(blocks)
}

// anchorResult assembles the standalone anchor operation's completion: the
// top-level decision states the query itself — a corroborated establishment
// reads Corroborated with the evidence as its reference, a failed
// establishment reads Unknown with whatever evidence formed — and no value
// is claimed. The establishment's supporters live on the Reference; the
// decision does not duplicate the adjudication chain.
func anchorResult(ref rpc.ReferenceEvidence, err error) CallResult {
	evidence := ref.Clone()
	status := rpc.Unknown
	if err == nil {
		status = ref.Status
	}
	return CallResult{Decision: &rpc.Decision{
		Status:    status,
		Reference: &evidence,
	}}
}

// attachEstablishedReference carries a composite read's successfully
// established anchor evidence onto the business decision. The top-level
// decision keeps stating the business phase — the reference's supporters
// never join the business source tables — but the prerequisite the read
// actually rode stays visible on Decision.Reference for both success and
// later business failure.
func attachEstablishedReference(decision *rpc.Decision, ref rpc.ReferenceEvidence) {
	if decision != nil && decision.Reference == nil {
		decision.Reference = anchorReference(ref)
	}
}

// anchorReference exposes a failed establishment's evidence for a composite
// read's result: whatever the anchor round formed travels on the decision.
// A zero evidence maps to nil.
func anchorReference(ref rpc.ReferenceEvidence) *rpc.ReferenceEvidence {
	if ref.Status == rpc.Unknown && ref.Hash == "" && ref.Height == 0 && len(ref.Faults) == 0 && len(ref.SourceFaults) == 0 && len(ref.CollectionFaults) == 0 && len(ref.Sources) == 0 && len(ref.Participants) == 0 && len(ref.Support) == 0 && len(ref.Reject) == 0 && ref.Adjudication.Basis == 0 && ref.Adjudication.Required == 0 && len(ref.Adjudication.Candidates) == 0 && ref.Adjudication.Reason == rpc.ReasonUnassessed {
		return nil
	}
	evidence := ref.Clone()
	return &evidence
}

// AnchorBlockHash returns a newly established corroborated block reference
// behind moving-tag reads through the library-owned anchor strategy.
func (c *Client) AnchorBlockHash(ctx context.Context) (string, CallResult, error) {
	return operation(c, ctx, false, func(opCtx context.Context) (string, CallResult, error) {
		ref, err := c.establishAnchor(opCtx, "")
		return ref.Hash, anchorResult(ref, err), err
	})
}

// AnchorBlockHashForTag establishes a corroborated block reference for a
// moving tag ("latest", "safe", or "finalized") and returns its hash. The
// default strict preset follows the requested tag. WithFinalizedAnchoring
// makes default/latest use finalized candidate selection and confirmation;
// safe still uses the strict safe algorithm, and finalized uses the
// finalized algorithm. ReferenceEvidence.Tag records the requested intent,
// not independent proof of the actual chain level. A reference that cannot
// reach Corroborated fails instead of returning an uncorroborated hash.
// Pending and earliest are not moving tags and are rejected here (pending
// keeps its node-state path, earliest is an explicit height).
func (c *Client) AnchorBlockHashForTag(ctx context.Context, tag string) (string, CallResult, error) {
	// Validation runs inside the operation body: the capture is bound
	// before any parameter check, so a local rejection still publishes its
	// completion result.
	return operation(c, ctx, false, func(opCtx context.Context) (string, CallResult, error) {
		lowered := strings.ToLower(tag)
		switch lowered {
		case "latest", "safe", "finalized":
		default:
			rejected := fmt.Errorf("ethguard: anchor tag %q is not an anchorable moving tag: %w", tag, rpc.ErrInvalidRequest)
			return "", CallResult{Decision: &rpc.Decision{Status: rpc.Unknown}}, rejected
		}
		ref, err := c.establishAnchor(opCtx, lowered)
		ref.Tag = lowered
		return ref.Hash, anchorResult(ref, err), err
	})
}

// AnchorBlockHeight returns the height of the currently corroborated block
// reference. It rides the same establishment as AnchorBlockHash — the
// response carries number and hash together, so no extra fan-out is
// needed.
func (c *Client) AnchorBlockHeight(ctx context.Context) (uint64, CallResult, error) {
	return operation(c, ctx, false, func(opCtx context.Context) (uint64, CallResult, error) {
		ref, err := c.establishAnchor(opCtx, "")
		return ref.Height, anchorResult(ref, err), err
	})
}
