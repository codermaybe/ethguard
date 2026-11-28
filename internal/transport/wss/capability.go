package wss

// SupportsBoundedSubscriptions declares the WSS dialer's bounded stream support
// without a handshake or capability probe.
func (Dialer) SupportsBoundedSubscriptions() bool { return true }
