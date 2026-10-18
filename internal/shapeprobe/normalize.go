package shapeprobe

import (
	"encoding/json"
	"fmt"

	"github.com/codermaybe/ethguard/rpc"
	"github.com/codermaybe/ethguard/rpc/methods"
)

// NormalizeResult calls the method's existing Policy.Normalize. It does not
// define a comparison of its own. Methods outside the probe have no dispatch.
func NormalizeResult(method string, result json.RawMessage) (string, error) {
	policy, err := existingPolicy(method)
	if err != nil {
		return "", err
	}
	return policy.Normalize(result)
}

func existingPolicy(method string) (rpc.Policy, error) {
	switch method {
	case methods.MethodGetLogs:
		return methods.LogsPolicy(nil), nil
	case methods.MethodFeeHistory:
		return methods.FeeHistoryPolicy(nil), nil
	case methods.MethodChainID:
		return methods.ChainIDPolicy(), nil
	case methods.MethodNetVersion:
		return methods.NetVersionPolicy(), nil
	case methods.MethodGasPrice:
		return methods.GasPricePolicy(), nil
	case methods.MethodMaxPriorityFeePerGas:
		return methods.MaxPriorityFeePerGasPolicy(), nil
	case methods.MethodBlobBaseFee:
		return methods.BlobBaseFeePolicy(), nil
	case methods.MethodBlockNumber:
		return methods.BlockNumberPolicy(), nil
	case methods.MethodSyncing:
		return methods.SyncingPolicy(), nil
	case methods.MethodPeerCount:
		return methods.PeerCountPolicy(), nil
	case methods.MethodListening:
		return methods.ListeningPolicy(), nil
	default:
		return nil, fmt.Errorf("shapeprobe: %s has no existing normalizer", method)
	}
}
