package pool

import upstream "github.com/codermaybe/ethguard/internal/transport"

// Transport is the pool-owned minimum upstream seam. The implementation is
// supplied by a connection Adapter; pool only manages its lifetime and use.
type Transport = upstream.Transport

// ErrBodyTooLarge remains available for callers that classified the existing
// HTTP capture failure through pool; the implementation now lives in the
// connection Adapter package.
var ErrBodyTooLarge = upstream.ErrBodyTooLarge

// dialer is kept private because production callers choose the adapter through
// pool construction while tests inject a fake seam.
type dialer = upstream.Dialer
