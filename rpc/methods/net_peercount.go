package methods

import (
	"github.com/codermaybe/ethguard/rpc"
	"github.com/codermaybe/ethguard/rpc/internal/wire"
)

// PeerCountPolicy resolves net_peerCount as an upstream-local result: a
// peer table belongs to one provider alone (see rpc.Local).
func PeerCountPolicy() rpc.Policy {
	return rpc.Local(wire.NormalizeHexQuantity, rpc.WithLocalRuleCompareKind(rpc.RuleCompareQuantity), rpc.WithLocalCanonical(wire.EncodeFoldedQuantity))
}
