package projection

import (
	"sort"
	"strings"
)

// ProductionStandardFieldCoverageGaps compares every pinned official path
// consumed by a production projector with that projector's declaration. A
// gap is a migration obligation: the field may be valid in an official
// response, but it must not silently remain an Extension in Decision.Value.
//
// The comparison treats a declared object or array root as covering its
// descendants because the runtime projector receives flattened leaf paths
// while object encoders retain the complete standard subtree.
func ProductionStandardFieldCoverageGaps() (map[string][]string, error) {
	methods := []string{
		"eth_getBlockByHash", "eth_getBlockByNumber", "eth_getBlockReceipts",
		"eth_getLogs", "eth_feeHistory", "eth_getTransactionByHash",
		"eth_getTransactionReceipt", "eth_getBalance", "eth_call",
	}
	gaps := make(map[string][]string)
	for _, method := range methods {
		if _, err := ExecutionAPIStandardFields(method); err != nil {
			return nil, err
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
	// A canonical encoder's leaf tags imply the object and array containers
	// that hold them. The official schema exposes those containers as paths,
	// while encoder coverage intentionally reports leaves only.
	if len(declared) > len(official) && strings.HasPrefix(declared, official+"/") {
		return true
	}
	// Receipt schemas are reused both at the root and below the block-receipts
	// result array. Compare the same path after removing that outer wildcard.
	if len(official) > 2 && official[:2] == "/*" {
		return coversStandardPath(declared, official[2:])
	}
	return false
}
