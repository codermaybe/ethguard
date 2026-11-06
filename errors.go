package ethguard

import (
	"errors"

	"github.com/codermaybe/ethguard/pool"
	"github.com/codermaybe/ethguard/rpc"
)

// Sentinel errors classify every failure mode of an aggregate call.
// EthGuard errors wrap exactly one class each; match with errors.Is rather
// than string comparison. The classes are part of the public stability
// contract (decision 0009): new classes may appear, existing ones will not
// be removed or merged.
//
//	ErrNoEligibleSource   fan-out impossible: nothing registered, or every
//	                      source is quarantined, cooling down, or closed
//	ErrAllSourcesDown     fleet was contacted but no source produced a usable
//	                      transport-level response
//	ErrChainIDMismatch    a source reported a foreign chain and was rejected
//	ErrPoolClosed         the client's pool has been closed
//	ErrConflict           sources disagreed under corroboration, or a quorum
//	                      went unmet; Decision evidence names the clusters
//	ErrNoValidResponse    sources answered but no response survived
//	                      normalization
//	ErrMalformedResponse  a resolved value cannot be shaped at a typed
//	                      typed boundary, anchor head)
//	ErrInvalidRequest     the request cannot be prepared at all (pending on
//	                      incomparable methods, contradictory anchors)
//	ErrCaptureReused      a ResultCapture handle entered a second independent
//	                      call; its result delivery is permanently invalid
//	ErrCaptureAborted     the call bound to a ResultCapture exited abnormally
//	                      (a propagated panic), so no normal result was
//	                      published
var (
	ErrNoEligibleSource       = pool.ErrNoEligibleSource
	ErrAllSourcesDown         = pool.ErrAllSourcesDown
	ErrChainIDMismatch        = pool.ErrChainIDMismatch
	ErrPoolClosed             = pool.ErrClosed
	ErrConflict               = rpc.ErrConflict
	ErrNoValidResponse        = rpc.ErrNoValidResponse
	ErrMalformedResponse      = rpc.ErrMalformedResponse
	ErrInvalidRequest         = rpc.ErrInvalidRequest
	ErrAnchorMismatch         = rpc.ErrAnchorMismatch
	ErrReorgDetected          = rpc.ErrReorgDetected
	ErrUnsupportedTypedObject = rpc.ErrUnsupportedTypedObject
	ErrCaptureReused          = errors.New("ethguard: result capture already bound to another call")
	ErrCaptureAborted         = errors.New("ethguard: result capture bound to an abnormally terminated call")
)
