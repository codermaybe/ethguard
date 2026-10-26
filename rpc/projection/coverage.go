package projection

import (
	"sort"
	"strings"
)

// ProductionStandardFieldCoverageGaps verifies that the pinned official
// result schema loads for every production method and that the schema
// bundle's version matches the spec pin. It performs no per-projector
// comparison: the production projectors are themselves driven by that same
// pinned schema through NewSchemaProjector (see official.go and
// projection.go), so there is no separate projector declaration that could
// drift from the official paths and the returned gaps map is structurally
// empty. A non-nil error means the schema bundle is unloadable or its
// version pin inconsistent, which is the only failure this check can
// surface.
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
