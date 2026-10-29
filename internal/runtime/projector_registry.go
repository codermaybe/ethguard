package runtime

import (
	"fmt"

	"github.com/codermaybe/ethguard/rpc"
	"github.com/codermaybe/ethguard/rpc/methods"
	"github.com/codermaybe/ethguard/rpc/projection"
)

// The projector registry binds the runtime-owned semantic projectors by
// method. The binding lives outside the execution plans on purpose: a plan
// names its projector, and this assembly decides what that name resolves
// to, so rpc/methods stays clear of the projection and typed layers.

// buildProjectorRegistry assembles the observation projectors the default
// composition binds.
func buildProjectorRegistry() (map[string]rpc.ObservationProjector, error) {
	projectors := map[string]rpc.ObservationProjector{
		methods.MethodGetBalance: projection.ScalarStandardProjector(),
		methods.MethodCall:       projection.ScalarStandardProjector(),
	}
	for _, method := range []string{
		methods.MethodGetTransactionReceipt,
		methods.MethodGetBlockReceipts,
		methods.MethodGetBlockByHash,
		methods.MethodGetBlockByNumber,
		methods.MethodGetTransactionByHash,
		methods.MethodGetLogs,
		methods.MethodFeeHistory,
	} {
		projector, err := projection.NewSchemaProjector(method)
		if err != nil {
			return nil, fmt.Errorf("build %s projector: %w", method, err)
		}
		projectors[method] = projector
	}
	return projectors, nil
}
