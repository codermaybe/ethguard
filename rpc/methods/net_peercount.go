package methods

import (
	"ethguard/rpc"
	"ethguard/rpc/internal/wire"
)

// MethodPeerCount is the JSON-RPC method name resolved by PeerCountPolicy.
// Note the namespace: this is a net_ method, corroborated like any other
// registered method.
const MethodPeerCount = "net_peerCount"

// PeerCountPolicy resolves net_peerCount as an upstream-local result: a
// peer table belongs to one provider alone (see rpc.Local).
func PeerCountPolicy() rpc.Policy { return rpc.Local(wire.NormalizeHexQuantity) }
