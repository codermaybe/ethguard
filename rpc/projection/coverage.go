package projection

import "sort"

// ProductionStandardFieldCoverageGaps compares every pinned official path
// consumed by a production projector with that projector's declaration. A
// gap is a migration obligation: the field may be valid in an official
// response, but it must not silently remain an Extension in Decision.Value.
//
// The comparison treats a declared object or array root as covering its
// descendants because the runtime projector receives flattened leaf paths
// while object encoders retain the complete standard subtree.
func ProductionStandardFieldCoverageGaps() (map[string][]string, error) {
	declarations := map[string][]StandardField{
		"eth_getBlockByHash":        BlockStandardFields(),
		"eth_getBlockByNumber":      BlockStandardFields(),
		"eth_getBlockReceipts":      TransactionReceiptStandardFields(),
		"eth_getLogs":               LogsStandardFields(),
		"eth_feeHistory":            FeeHistoryStandardFields(),
		"eth_getTransactionByHash":  TransactionStandardFields(),
		"eth_getTransactionReceipt": TransactionReceiptStandardFields(),
		"eth_getBalance":            {{Path: "", Required: true}},
		"eth_call":                  {{Path: "", Required: true}},
	}
	gaps := make(map[string][]string)
	for method, fields := range declarations {
		methodGaps, err := StandardFieldCoverageGaps(method, fields)
		if err != nil {
			return nil, err
		}
		if len(methodGaps) > 0 {
			gaps[method] = methodGaps
		}
	}
	return gaps, nil
}

// StandardFieldCoverageGaps reports official paths not covered by a declared
// field set. It is shared by production projector checks and encoder tests so
// both seams use the same path matching rules.
func StandardFieldCoverageGaps(method string, declarations []StandardField) ([]string, error) {
	official, err := ExecutionAPIStandardFields(method)
	if err != nil {
		return nil, err
	}
	gaps := make([]string, 0)
	for _, candidate := range official {
		covered := false
		for _, declared := range declarations {
			if coversStandardPath(declared.Path, candidate.Path) {
				covered = true
				break
			}
		}
		if !covered {
			gaps = append(gaps, candidate.Path)
		}
	}
	sort.Strings(gaps)
	return gaps, nil
}

func coversStandardPath(declared, official string) bool {
	if declared == "" {
		return true
	}
	if declared == official || matchJSONPointerPattern(declared, official) {
		return true
	}
	// A root declaration covers the complete nested object/array it owns.
	if len(official) > len(declared) && official[:len(declared)] == declared && official[len(declared)] == '/' {
		return true
	}
	// Receipt schemas are reused both at the root and below the block-receipts
	// result array. Compare the same path after removing that outer wildcard.
	if len(official) > 2 && official[:2] == "/*" {
		return coversStandardPath(declared, official[2:])
	}
	return false
}
