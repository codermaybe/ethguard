package log

import (
	"strings"
	"testing"

	"github.com/codermaybe/ethguard/internal/projection"
	"github.com/codermaybe/ethguard/internal/testkit/coverage"
)

func TestCanonicalLogEncoderCoversOfficialPaths(t *testing.T) {
	paths := coverage.JSONPaths(CanonicalLog{})
	declared := []projection.StandardField{{Path: "/*"}}
	for _, path := range paths {
		declared = append(declared, projection.StandardField{Path: "/*" + path})
	}
	gaps, err := projection.StandardFieldCoverageGaps("eth_getLogs", declared)
	if err != nil {
		t.Fatal(err)
	}
	if len(gaps) != 0 {
		t.Fatalf("logs encoder gaps: %s", strings.Join(gaps, ", "))
	}
}
