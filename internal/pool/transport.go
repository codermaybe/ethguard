package pool

import upstream "github.com/codermaybe/ethguard/internal/transport"

// dialer is kept private because production callers choose the adapter through
// pool construction while tests inject a fake seam.
type dialer = upstream.Dialer
