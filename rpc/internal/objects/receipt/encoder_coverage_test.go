package receipt

import (
	"strings"
	"testing"

	"github.com/codermaybe/ethguard/rpc/internal/objects/coverage"
	"github.com/codermaybe/ethguard/rpc/projection"
)

func TestCanonicalReceiptEncoderCoversOfficialPaths(t *testing.T) {
	paths := coverage.JSONPaths(canonicalReceipt{})
	root := make([]projection.StandardField, len(paths))
	for i, path := range paths {
		root[i] = projection.StandardField{Path: path}
	}
	for _, method := range []string{"eth_getTransactionReceipt", "eth_getBlockReceipts"} {
		declared := root
		if method == "eth_getBlockReceipts" {
			declared = make([]projection.StandardField, len(root))
			for i, field := range root {
				declared[i] = projection.StandardField{Path: "/*" + field.Path}
			}
		}
		gaps, err := projection.StandardFieldCoverageGaps(method, declared)
		if err != nil {
			t.Fatal(err)
		}
		if len(gaps) != 0 {
			t.Fatalf("%s encoder gaps: %s", method, strings.Join(gaps, ", "))
		}
	}
}
