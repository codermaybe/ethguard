package ethguard

import (
	"context"
	"fmt"

	"github.com/codermaybe/ethguard/ethapi"
	"github.com/codermaybe/ethguard/internal/methods"
	"github.com/codermaybe/ethguard/rpc"
)

// SimulateV1 executes a generated simulation request through the native method plan.
// nil base means latest. Inner failed calls are ordinary simulation results;
// synthetic block/log identities are not claims of real-chain inclusion.
func (c *Client) SimulateV1(ctx context.Context, payload ethapi.SimulateV1Request, base *ethapi.SimulateBlockReference) ([]ethapi.SimulateBlock, CallResult, error) {
	return operation(c, ctx, false, func(opCtx context.Context) ([]ethapi.SimulateBlock, CallResult, error) {
		return c.simulateV1Exec(opCtx, payload, base)
	})
}

func (c *Client) simulateV1Exec(ctx context.Context, payload ethapi.SimulateV1Request, base *ethapi.SimulateBlockReference) ([]ethapi.SimulateBlock, CallResult, error) {
	var reference any
	if base != nil {
		reference = *base
	}
	// All entry points share the plan's one input snapshot and schema checks.
	decision, err := c.callTyped(ctx, methods.MethodSimulateV1, payload, reference)
	if err != nil {
		decision.Value = nil
		return nil, CallResult{Decision: decisionOf(decision)}, err
	}
	result, err := ethapi.NewSimulateV1Result(decision.Value)
	if err != nil {
		return nil, CallResult{Decision: decisionOf(decision)}, fmt.Errorf("%w: simulate projection: %v", rpc.ErrMalformedResponse, err)
	}
	return result, CallResult{Decision: decisionOf(decision)}, nil
}
