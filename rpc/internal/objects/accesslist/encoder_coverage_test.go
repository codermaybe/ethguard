package accesslist

import (
	"strings"
	"testing"

	"github.com/codermaybe/ethguard/rpc/internal/objects/coverage"
	"github.com/codermaybe/ethguard/rpc/projection"
)

func TestCanonicalAccessListEncoderCoversOfficialPaths(t *testing.T) {
	paths := coverage.JSONPaths(canonical{})
	for i, path := range paths {
		if path == "/accessList/*" {
			paths[i] = "/accessList/*/"
		}
	}
	declared := make([]projection.StandardField, len(paths))
	for i, path := range paths {
		declared[i] = projection.StandardField{Path: path}
	}
	gaps, err := projection.StandardFieldCoverageGaps("eth_createAccessList", declared)
	if err != nil {
		t.Fatal(err)
	}
	if len(gaps) != 0 {
		t.Fatalf("access-list encoder gaps: %s", strings.Join(gaps, ", "))
	}
}
