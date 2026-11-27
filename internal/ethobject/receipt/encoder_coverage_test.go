package receipt

import (
	"strings"
	"testing"

	"github.com/codermaybe/ethguard/ethapi"
	"github.com/codermaybe/ethguard/internal/projection"
	"github.com/codermaybe/ethguard/internal/testkit/coverage"
)

func TestCanonicalReceiptEncoderCoversOfficialPaths(t *testing.T) {
	paths := coverage.JSONPaths(ethapi.Receipt{})
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
