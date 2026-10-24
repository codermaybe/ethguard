package livecanary

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func compareExample(t *testing.T) ([]byte, []byte) {
	t.Helper()
	manifestBytes, err := os.ReadFile(filepath.Join("..", "..", "docs", "design", "live-canary", "manifest.example.json"))
	if err != nil {
		t.Fatalf("read manifest example: %v", err)
	}
	reportBytes, err := os.ReadFile(filepath.Join("..", "..", "docs", "design", "live-canary", "report.example.json"))
	if err != nil {
		t.Fatalf("read report example: %v", err)
	}
	return manifestBytes, reportBytes
}

func mutateReport(t *testing.T, data []byte, mutate func(*Report)) []byte {
	t.Helper()
	report, err := ParseReport(data)
	if err != nil {
		t.Fatalf("ParseReport: %v", err)
	}
	mutate(report)
	encoded, err := report.Marshal()
	if err != nil {
		t.Fatalf("marshal mutated report: %v", err)
	}
	return encoded
}

func compareReportStep(t *testing.T, report *Report, id string) *StepReport {
	t.Helper()
	for i := range report.Steps {
		if report.Steps[i].ID == id {
			return &report.Steps[i]
		}
	}
	t.Fatalf("step %q not found", id)
	return nil
}

func TestCompareIdenticalReports(t *testing.T) {
	manifestBytes, reportBytes := compareExample(t)
	comparison, err := CompareReports(manifestBytes, reportBytes, reportBytes)
	if err != nil {
		t.Fatalf("CompareReports: %v", err)
	}
	if comparison.Classification != DiffIdentical || !comparison.Repeatable || len(comparison.Differences) != 0 {
		t.Fatalf("comparison = %+v", comparison)
	}
	if _, err := comparison.Marshal(); err != nil {
		t.Fatalf("Marshal: %v", err)
	}
}

func TestCompareProviderFluctuationIsRepeatable(t *testing.T) {
	manifestBytes, reportBytes := compareExample(t)
	changed := mutateReport(t, reportBytes, func(report *Report) {
		report.SourceHealth[0].Successes++
		report.SourceHealth[1].RateLimited++
	})
	comparison, err := CompareReports(manifestBytes, reportBytes, changed)
	if err != nil {
		t.Fatalf("CompareReports: %v", err)
	}
	if comparison.Classification != DiffProviderFluctuation || !comparison.Repeatable {
		t.Fatalf("comparison = %+v", comparison)
	}
	if len(comparison.Differences) != 2 {
		t.Fatalf("differences = %+v", comparison.Differences)
	}
}

func TestCompareValueDriftIsProviderFluctuation(t *testing.T) {
	manifestBytes, reportBytes := compareExample(t)
	changed := mutateReport(t, reportBytes, func(report *Report) {
		compareReportStep(t, report, "identity.chain-id").Decision.ValueSHA256 = strings.Repeat("a", 64)
	})
	comparison, err := CompareReports(manifestBytes, reportBytes, changed)
	if err != nil {
		t.Fatalf("CompareReports: %v", err)
	}
	if comparison.Classification != DiffProviderFluctuation || !comparison.Repeatable {
		t.Fatalf("comparison = %+v", comparison)
	}
}

func TestCompareReferenceDriftIsProviderFluctuation(t *testing.T) {
	manifestBytes, reportBytes := compareExample(t)
	changed := mutateReport(t, reportBytes, func(report *Report) {
		compareReportStep(t, report, "reference.finalized-balance").Reference.Height++
	})
	comparison, err := CompareReports(manifestBytes, reportBytes, changed)
	if err != nil {
		t.Fatalf("CompareReports: %v", err)
	}
	if comparison.Classification != DiffProviderFluctuation || !comparison.Repeatable {
		t.Fatalf("comparison = %+v", comparison)
	}
}

func TestCompareRunReferenceDriftIsProviderFluctuation(t *testing.T) {
	manifestBytes, reportBytes := compareExample(t)
	changed := mutateReport(t, reportBytes, func(report *Report) {
		step := compareReportStep(t, report, "pinned.block")
		step.LogicalRequest.Params[0] = strings.Repeat("0x", 1) + strings.Repeat("b", 64)
		step.PreparedRequest.Params[0] = strings.Repeat("0x", 1) + strings.Repeat("b", 64)
	})
	comparison, err := CompareReports(manifestBytes, reportBytes, changed)
	if err != nil {
		t.Fatalf("CompareReports: %v", err)
	}
	if comparison.Classification != DiffProviderFluctuation || !comparison.Repeatable {
		t.Fatalf("comparison = %+v", comparison)
	}
	for _, difference := range comparison.Differences {
		if difference.Classification == DiffCodeMismatch {
			t.Fatalf("run-reference drift was classified as code mismatch: %+v", difference)
		}
	}
}

func TestCompareFalseInvariantIsCodeMismatch(t *testing.T) {
	manifestBytes, reportBytes := compareExample(t)
	changed := mutateReport(t, reportBytes, func(report *Report) {
		compareReportStep(t, report, "pinned.block").Invariants.RequestBinding = boolPointer(false)
	})
	comparison, err := CompareReports(manifestBytes, reportBytes, changed)
	if err != nil {
		t.Fatalf("CompareReports: %v", err)
	}
	if comparison.Classification != DiffCodeMismatch || comparison.Repeatable {
		t.Fatalf("comparison = %+v", comparison)
	}
}

func TestCompareStatusChangeIsCodeMismatch(t *testing.T) {
	manifestBytes, reportBytes := compareExample(t)
	base := mutateReport(t, reportBytes, func(report *Report) {
		report.State = StateInconclusive
		report.StateReasons = []StateReason{{Category: "provider_timeout", Detail: "synthetic"}}
	})
	changed := mutateReport(t, base, func(report *Report) {
		compareReportStep(t, report, "identity.chain-id").Decision.Status = "selected"
	})
	comparison, err := CompareReports(manifestBytes, base, changed)
	if err != nil {
		t.Fatalf("CompareReports: %v", err)
	}
	if comparison.Classification != DiffCodeMismatch || comparison.Repeatable {
		t.Fatalf("comparison = %+v", comparison)
	}
}

func TestCompareFailedStateIsCodeMismatch(t *testing.T) {
	manifestBytes, reportBytes := compareExample(t)
	changed := mutateReport(t, reportBytes, func(report *Report) {
		report.State = StateFailed
		report.StateReasons = []StateReason{{Category: "code_error", Detail: "synthetic"}}
	})
	comparison, err := CompareReports(manifestBytes, reportBytes, changed)
	if err != nil {
		t.Fatalf("CompareReports: %v", err)
	}
	if comparison.Classification != DiffCodeMismatch || comparison.Repeatable {
		t.Fatalf("comparison = %+v", comparison)
	}
}

func TestCompareBothFailedIsCodeMismatch(t *testing.T) {
	manifestBytes, reportBytes := compareExample(t)
	failed := mutateReport(t, reportBytes, func(report *Report) {
		report.State = StateFailed
		report.StateReasons = []StateReason{{Category: "code_error", Detail: "synthetic"}}
	})
	comparison, err := CompareReports(manifestBytes, failed, failed)
	if err != nil {
		t.Fatalf("CompareReports: %v", err)
	}
	if comparison.Classification != DiffCodeMismatch || comparison.Repeatable {
		t.Fatalf("comparison = %+v", comparison)
	}
}

func TestCompareBothWrongRunIDIsCodeMismatch(t *testing.T) {
	manifestBytes, reportBytes := compareExample(t)
	wrong := mutateReport(t, reportBytes, func(report *Report) {
		report.RunID = "01JAZ3K4M5N6P7Q8R9S0T1V2WY"
	})
	comparison, err := CompareReports(manifestBytes, wrong, wrong)
	if err != nil {
		t.Fatalf("CompareReports: %v", err)
	}
	if comparison.Classification != DiffCodeMismatch || comparison.Repeatable {
		t.Fatalf("comparison = %+v", comparison)
	}
}

func TestCompareManifestBindingMismatchIsRegistryDrift(t *testing.T) {
	manifestBytes, reportBytes := compareExample(t)
	changed := mutateReport(t, reportBytes, func(report *Report) {
		report.ManifestSHA256 = strings.Repeat("0", 64)
	})
	comparison, err := CompareReports(manifestBytes, reportBytes, changed)
	if err != nil {
		t.Fatalf("CompareReports: %v", err)
	}
	if comparison.Classification != DiffRegistryDrift || comparison.Repeatable {
		t.Fatalf("comparison = %+v", comparison)
	}
}

func TestCompareMissingStepIsCodeMismatch(t *testing.T) {
	manifestBytes, reportBytes := compareExample(t)
	changed := mutateReport(t, reportBytes, func(report *Report) {
		report.Steps = report.Steps[:len(report.Steps)-1]
		report.State = StateInconclusive
		report.StateReasons = []StateReason{{Category: "provider_timeout", Detail: "synthetic"}}
	})
	comparison, err := CompareReports(manifestBytes, reportBytes, changed)
	if err != nil {
		t.Fatalf("CompareReports: %v", err)
	}
	if comparison.Classification != DiffCodeMismatch || comparison.Repeatable {
		t.Fatalf("comparison = %+v", comparison)
	}
}

func TestComparisonMarshalRejectsInconsistentReport(t *testing.T) {
	report := ComparisonReport{
		SchemaVersion:  ComparisonSchemaVersion,
		Classification: DiffIdentical,
		Differences:    []ComparisonDifference{{Field: "state", Classification: DiffProviderFluctuation}},
		Repeatable:     true,
	}
	if _, err := report.Marshal(); err == nil {
		t.Fatal("Marshal accepted identical with differences")
	}
	report = ComparisonReport{
		SchemaVersion:  ComparisonSchemaVersion,
		Classification: DiffCodeMismatch,
		Repeatable:     true,
	}
	if _, err := report.Marshal(); err == nil {
		t.Fatal("Marshal accepted repeatable code mismatch")
	}
}
