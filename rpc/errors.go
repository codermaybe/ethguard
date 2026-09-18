package rpc

import "errors"

// ErrAnchorMismatch means a response does not belong to the reference used
// to prepare the request. It can be joined with ErrMalformedResponse so old
// callers retain the malformed-response classification.
var ErrAnchorMismatch = errors.New("rpc: anchor mismatch")

// ErrReorgDetected means a reference or its bound response changed while an
// anchored operation was being verified. It is evidence of movement, not a
// claim about canonical chain membership.
var ErrReorgDetected = errors.New("rpc: reorg detected")

// ErrUnsupportedTypedObject means the upstream object was structurally
// present but this go-ethereum build does not know its transaction type.
var ErrUnsupportedTypedObject = errors.New("rpc: unsupported typed object")
