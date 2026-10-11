package feehistory

import (
	"strings"
	"testing"

	"ethguard/rpc/internal/objects/coverage"
	"ethguard/rpc/projection"
)

func TestCanonicalFeeHistoryEncoderCoversOfficialPaths(t *testing.T) {
	paths := coverage.JSONPaths(canonical{})
	declared := make([]projection.StandardField, len(paths))
	for i, path := range paths {
		declared[i] = projection.StandardField{Path: path}
	}
	gaps, err := projection.StandardFieldCoverageGaps("eth_feeHistory", declared)
	if err != nil {
		t.Fatal(err)
	}
	if len(gaps) != 0 {
		t.Fatalf("fee history encoder gaps: %s", strings.Join(gaps, ", "))
	}
}
