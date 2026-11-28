package rpc

import (
	"context"
	"encoding/json"
)

// AdmissionCaller performs a dependent request against the same upstream
// source that produced the primary response.  Method policies use it for
// content that the primary JSON-RPC result references by commitment (for
// example uncle headers referenced by an execution block).
type AdmissionCaller func(context.Context, string, []any) (json.RawMessage, error)
