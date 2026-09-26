package methods

import "ethguard/rpc"

// compareOpts pins the coarse comparison-key class beside the normalizer.
// WithRuleCompareKind is not read by Prepare, Normalize, or Resolve.
func compareOpts(kind string, opts []rpc.StrictOption) []rpc.StrictOption {
	return append([]rpc.StrictOption{rpc.WithRuleCompareKind(kind)}, opts...)
}
