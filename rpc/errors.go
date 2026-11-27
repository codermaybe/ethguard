package rpc

import "errors"

// ErrInvalidReadPlan reports an executable read plan whose required resolver
// capability is absent. It is a plan configuration error, not a request or
// provider failure.
var ErrInvalidReadPlan = errors.New("rpc: invalid read plan")

// ErrAnchorMismatch means a response does not belong to the reference used
// to prepare the request. It can be joined with ErrMalformedResponse so old
// callers retain the malformed-response classification.
var ErrAnchorMismatch = errors.New("rpc: anchor mismatch")

// ErrReorgDetected means a reference or its bound response changed while an
// anchored operation was being corroborated. It is evidence of movement, not a
// claim about canonical chain membership.
var ErrReorgDetected = errors.New("rpc: reorg detected")

// ErrUnsupportedTypedObject means the upstream object was structurally
// present but this go-ethereum build does not know its transaction type.
var ErrUnsupportedTypedObject = errors.New("rpc: unsupported typed object")

// ErrReferenceRequired reports that a prepared read needs its reference
// established before the request can be rewritten. The plan rejects missing
// or unsuitable evidence before business collection; the coordinator also
// rejects a missing stage or an establishment without evidence.
var ErrReferenceRequired = errors.New("rpc: reference required for moving-tag read")
