package rpc

import "errors"

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
// established before the request can be rewritten. The preparing decorator
// returns it when the invocation carries no usable reference; the aggregator
// answers it by running the reference stage and preparing again. It is a
// pipeline-internal protocol, never a user-facing classification.
var ErrReferenceRequired = errors.New("rpc: reference required for moving-tag read")
