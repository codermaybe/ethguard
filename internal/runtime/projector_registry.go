package runtime

import (
	"fmt"

	"github.com/codermaybe/ethguard/rpc"
	"github.com/codermaybe/ethguard/rpc/methods"
	"github.com/codermaybe/ethguard/rpc/projection"
)

// The projector registry binds the runtime-owned semantic projectors by the
// projector ID the specs declare. The binding lives outside the execution
// plans on purpose: a plan names its projector as a business identity —
// two methods sharing one observation shape share one ID, and a registry
// rename or alias cannot silently detach a method's projection — and this
// assembly decides what each ID resolves to, so rpc/methods stays clear of
// the projection and typed layers. A plan with an empty projector ID stays
// unprojected by name.

// projectionSchemas pairs each projector ID with the registered method whose
// official result schema the projection derives from. One ID per schema
// family: methods that share a result shape (both block methods) share the
// ID and the projector.
func projectionSchemas() []struct {
	id     string
	schema string
} {
	return []struct {
		id     string
		schema string
	}{
		{methods.ProjectorBlock, methods.MethodGetBlockByNumber},
		{methods.ProjectorReceipt, methods.MethodGetTransactionReceipt},
		{methods.ProjectorBlockReceipts, methods.MethodGetBlockReceipts},
		{methods.ProjectorTransaction, methods.MethodGetTransactionByHash},
		{methods.ProjectorLogs, methods.MethodGetLogs},
		{methods.ProjectorFeeHistory, methods.MethodFeeHistory},
	}
}

// buildProjectorRegistry assembles the observation projectors the default
// composition binds, keyed by the projector IDs the specs declare.
func buildProjectorRegistry() (map[string]rpc.ObservationProjector, error) {
	projectors := map[string]rpc.ObservationProjector{
		methods.ProjectorScalarStandard: projection.ScalarStandardProjector(),
	}
	for _, binding := range projectionSchemas() {
		projector, err := projection.NewSchemaProjector(binding.schema)
		if err != nil {
			return nil, fmt.Errorf("build %s projector: %w", binding.schema, err)
		}
		projectors[binding.id] = projector
	}
	return projectors, nil
}
