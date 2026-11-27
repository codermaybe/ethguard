package accesslist

import (
	"strings"
	"testing"

	"github.com/codermaybe/ethguard/ethapi"
	"github.com/codermaybe/ethguard/internal/projection"
	"github.com/codermaybe/ethguard/internal/testkit/coverage"
)

func TestCanonicalAccessListEncoderCoversOfficialPaths(t *testing.T) {
	paths := coverage.JSONPaths(ethapi.AccessListResult{})
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
