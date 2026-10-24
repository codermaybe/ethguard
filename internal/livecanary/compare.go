package livecanary

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

const ComparisonSchemaVersion = "ethguard.live-comparison/v1"

const (
	DiffIdentical           = "identical"
	DiffProviderFluctuation = "provider_fluctuation"
	DiffCodeMismatch        = "code_mismatch"
	DiffRegistryDrift       = "registry_drift"
)

// ComparisonReport is the machine-readable T10.7 difference report. A
// provider-only difference keeps Repeatable=true because the contract-level
// values, references, typed results, and invariants still match; code and
// registry differences make it false.
type ComparisonReport struct {
	SchemaVersion        string                 `json:"schema_version"`
	ManifestSHA256       string                 `json:"manifest_sha256"`
	PlanSHA256           string                 `json:"plan_sha256"`
	OriginalReportSHA256 string                 `json:"original_report_sha256"`
	RecheckReportSHA256  string                 `json:"recheck_report_sha256"`
	OriginalState        string                 `json:"original_state"`
	RecheckState         string                 `json:"recheck_state"`
	Repeatable           bool                   `json:"repeatable"`
	Classification       string                 `json:"classification"`
	Differences          []ComparisonDifference `json:"differences"`
}

type ComparisonDifference struct {
	StepID         *string `json:"step_id"`
	Field          string  `json:"field"`
	Original       string  `json:"original"`
	Recheck        string  `json:"recheck"`
	Classification string  `json:"classification"`
}

func (r ComparisonReport) Marshal() ([]byte, error) {
	if r.SchemaVersion != ComparisonSchemaVersion {
		return nil, fmt.Errorf("comparison: schema_version %q", r.SchemaVersion)
	}
	switch r.Classification {
	case DiffIdentical, DiffProviderFluctuation, DiffCodeMismatch, DiffRegistryDrift:
	default:
		return nil, fmt.Errorf("comparison: classification %q", r.Classification)
	}
	if r.Classification == DiffIdentical && len(r.Differences) != 0 {
		return nil, fmt.Errorf("comparison: identical classification carries differences")
	}
	if r.Classification != DiffIdentical && len(r.Differences) == 0 {
		return nil, fmt.Errorf("comparison: %s classification requires differences", r.Classification)
	}
	repeatable := r.Classification != DiffCodeMismatch && r.Classification != DiffRegistryDrift
	if r.Repeatable != repeatable {
		return nil, fmt.Errorf("comparison: repeatable=%v contradicts classification %q", r.Repeatable, r.Classification)
	}
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("comparison: marshal: %w", err)
	}
	return append(data, '\n'), nil
}

// CompareReports checks two observations of the same manifest. It records
// every difference classified as provider fluctuation, code mismatch, or
// registry drift; timestamps and digest-only changes are expected and not
// differences.
func CompareReports(manifestBytes, originalBytes, recheckBytes []byte) (ComparisonReport, error) {
	manifest, err := ParseManifest(manifestBytes)
	if err != nil {
		return ComparisonReport{}, fmt.Errorf("compare: %w", err)
	}
	original, err := ParseReport(originalBytes)
	if err != nil {
		return ComparisonReport{}, fmt.Errorf("compare: original: %w", err)
	}
	recheck, err := ParseReport(recheckBytes)
	if err != nil {
		return ComparisonReport{}, fmt.Errorf("compare: recheck: %w", err)
	}
	manifestSHA := ManifestSHA256(manifestBytes)
	report := ComparisonReport{
		SchemaVersion:        ComparisonSchemaVersion,
		ManifestSHA256:       manifestSHA,
		PlanSHA256:           manifest.Plan.SHA256,
		OriginalReportSHA256: ReportSHA256(originalBytes),
		RecheckReportSHA256:  ReportSHA256(recheckBytes),
		OriginalState:        original.State,
		RecheckState:         recheck.State,
		Differences:          []ComparisonDifference{},
	}
	add := func(stepID *string, field, a, b, class string) {
		if a == b {
			return
		}
		report.Differences = append(report.Differences, ComparisonDifference{
			StepID: stepID, Field: field, Original: a, Recheck: b, Classification: class,
		})
	}
	stepID := func(step StepReport) *string {
		id := step.ID
		return &id
	}

	if original.SchemaVersion != recheck.SchemaVersion {
		add(nil, "schema_version", original.SchemaVersion, recheck.SchemaVersion, DiffCodeMismatch)
	}
	if original.ManifestSHA256 != manifestSHA {
		add(nil, "original.manifest_sha256", original.ManifestSHA256, manifestSHA, DiffRegistryDrift)
	}
	if recheck.ManifestSHA256 != manifestSHA {
		add(nil, "recheck.manifest_sha256", recheck.ManifestSHA256, manifestSHA, DiffRegistryDrift)
	}
	if original.RunID != manifest.RunID || recheck.RunID != manifest.RunID {
		report.Differences = append(report.Differences, ComparisonDifference{
			Field: "run_id", Original: original.RunID, Recheck: recheck.RunID, Classification: DiffCodeMismatch,
		})
	}
	if original.PlanSHA256 != manifest.Plan.SHA256 || recheck.PlanSHA256 != manifest.Plan.SHA256 {
		report.Differences = append(report.Differences, ComparisonDifference{
			Field: "plan_sha256", Original: original.PlanSHA256, Recheck: recheck.PlanSHA256, Classification: DiffCodeMismatch,
		})
	}
	if original.State == StateFailed || recheck.State == StateFailed {
		report.Differences = append(report.Differences, ComparisonDifference{
			Field: "state.failed", Original: original.State, Recheck: recheck.State, Classification: DiffCodeMismatch,
		})
	} else {
		add(nil, "state", original.State, recheck.State, classifyStateDiff(original.State, recheck.State))
	}
	add(nil, "reference_step_id", pointerText(original.ReferenceStepID), pointerText(recheck.ReferenceStepID), DiffCodeMismatch)
	add(nil, "state_reasons", reasonSignature(original.StateReasons), reasonSignature(recheck.StateReasons),
		classifyReasonDiff(original.StateReasons, recheck.StateReasons))
	compareSourceHealth(&report, original.SourceHealth, recheck.SourceHealth)
	compareSteps(manifest, original.Steps, recheck.Steps, add, stepID)

	hasCode, hasRegistry, hasProvider := false, false, false
	for _, diff := range report.Differences {
		switch diff.Classification {
		case DiffCodeMismatch:
			hasCode = true
		case DiffRegistryDrift:
			hasRegistry = true
		case DiffProviderFluctuation:
			hasProvider = true
		}
	}
	switch {
	case hasCode:
		report.Classification = DiffCodeMismatch
	case hasRegistry:
		report.Classification = DiffRegistryDrift
	case hasProvider:
		report.Classification = DiffProviderFluctuation
	default:
		report.Classification = DiffIdentical
	}
	report.Repeatable = !hasCode && !hasRegistry
	if _, err := report.Marshal(); err != nil {
		return ComparisonReport{}, err
	}
	return report, nil
}

func compareSteps(manifest *Manifest, original, recheck []StepReport, add func(*string, string, string, string, string), stepID func(StepReport) *string) {
	if len(original) != len(recheck) {
		add(nil, "steps.length", strconv.Itoa(len(original)), strconv.Itoa(len(recheck)), DiffCodeMismatch)
	}
	recheckByID := make(map[string]StepReport, len(recheck))
	for _, step := range recheck {
		recheckByID[step.ID] = step
	}
	for _, left := range original {
		right, ok := recheckByID[left.ID]
		if !ok {
			add(stepID(left), "steps.missing", "present", "missing", DiffCodeMismatch)
			continue
		}
		id := stepID(left)
		add(id, "phase", left.Phase, right.Phase, DiffCodeMismatch)
		add(id, "path", left.Path, right.Path, DiffCodeMismatch)
		add(id, "method", left.Method, right.Method, DiffCodeMismatch)
		add(id, "logical_request.method", left.LogicalRequest.Method, right.LogicalRequest.Method, DiffCodeMismatch)
		add(id, "logical_request.params", jsonText(left.LogicalRequest.Params), jsonText(right.LogicalRequest.Params), DiffCodeMismatch)
		comparePrepared(manifest, left, right, id, add)
		compareInvariants(id, left.Invariants, right.Invariants, add)
		compareDecision(id, left.Decision, right.Decision, add)
		compareReference(manifest, left, right, id, add)
		compareTyped(id, left.Typed, right.Typed, add)
		add(id, "outcome", left.Outcome, right.Outcome, DiffProviderFluctuation)
	}
}

func comparePrepared(manifest *Manifest, left, right StepReport, id *string, add func(*string, string, string, string, string)) {
	a, b := left.PreparedRequest, right.PreparedRequest
	if a == nil || b == nil {
		if (a == nil) != (b == nil) {
			add(id, "prepared_request", requestText(a), requestText(b), DiffCodeMismatch)
		}
		return
	}
	add(id, "prepared_request.method", a.Method, b.Method, DiffCodeMismatch)
	class := DiffProviderFluctuation
	if step, ok := manifestPlanStep(manifest, left.ID); ok && step.Expect.Binding == "none" {
		class = DiffCodeMismatch
	}
	add(id, "prepared_request.params", jsonText(a.Params), jsonText(b.Params), class)
}

func compareInvariants(id *string, a, b InvariantReport, add func(*string, string, string, string, string)) {
	fields := []struct {
		name  string
		value func(InvariantReport) *bool
		class string
	}{
		{"request_binding", func(v InvariantReport) *bool { return v.RequestBinding }, DiffCodeMismatch},
		{"status_allowed", func(v InvariantReport) *bool { return v.StatusAllowed }, DiffCodeMismatch},
		{"evidence_conservation", func(v InvariantReport) *bool { return v.EvidenceConservation }, DiffCodeMismatch},
		{"representative_in_support", func(v InvariantReport) *bool { return v.RepresentativeInSupport }, DiffCodeMismatch},
		{"reference_status", func(v InvariantReport) *bool { return v.ReferenceStatus }, DiffProviderFluctuation},
		{"error_status_exclusive", func(v InvariantReport) *bool { return v.ErrorStatusExclusive }, DiffCodeMismatch},
		{"basis_unchanged", func(v InvariantReport) *bool { return v.BasisUnchanged }, DiffCodeMismatch},
		{"typed_decode", func(v InvariantReport) *bool { return v.TypedDecode }, DiffCodeMismatch},
		{"shape_matches_plan", func(v InvariantReport) *bool { return v.ShapeMatchesPlan }, DiffProviderFluctuation},
	}
	for _, field := range fields {
		left, right := field.value(a), field.value(b)
		if left == nil && right == nil {
			continue
		}
		if left == nil || right == nil || *left != *right || !*left {
			add(id, "invariants."+field.name, pointerBoolText(left), pointerBoolText(right), field.class)
		}
	}
}

func compareDecision(id *string, a, b *DecisionReport, add func(*string, string, string, string, string)) {
	if a == nil && b == nil {
		return
	}
	if a == nil || b == nil {
		add(id, "decision", decisionText(a), decisionText(b), DiffCodeMismatch)
		return
	}
	class := DiffCodeMismatch
	if a.Status == "unknown" || b.Status == "unknown" {
		class = DiffProviderFluctuation
	}
	add(id, "decision.status", a.Status, b.Status, class)
	add(id, "decision.n", strconv.Itoa(a.N), strconv.Itoa(b.N), DiffCodeMismatch)
	add(id, "decision.participants", strings.Join(a.Participants, ","), strings.Join(b.Participants, ","), DiffCodeMismatch)
	add(id, "decision.value_sha256", a.ValueSHA256, b.ValueSHA256, DiffProviderFluctuation)
	add(id, "decision.value_kind", a.ValueKind, b.ValueKind, DiffProviderFluctuation)
	add(id, "decision.support", strings.Join(a.Support, ","), strings.Join(b.Support, ","), DiffProviderFluctuation)
	add(id, "decision.reject", strings.Join(a.Reject, ","), strings.Join(b.Reject, ","), DiffProviderFluctuation)
	add(id, "decision.faults", sourceFaultSignature(a.Faults), sourceFaultSignature(b.Faults), DiffProviderFluctuation)
	add(id, "decision.collection_faults", collectionFaultSignature(a.CollectionFaults), collectionFaultSignature(b.CollectionFaults), DiffProviderFluctuation)
	add(id, "decision.representative", a.Representative, b.Representative, DiffProviderFluctuation)
	add(id, "decision.error_class", a.ErrorClass, b.ErrorClass, class)
	add(id, "decision.error_detail", a.ErrorDetail, b.ErrorDetail, DiffProviderFluctuation)
}

func compareReference(manifest *Manifest, left, right StepReport, id *string, add func(*string, string, string, string, string)) {
	a, b := left.Reference, right.Reference
	if a == nil && b == nil {
		return
	}
	class := DiffProviderFluctuation
	if step, ok := manifestPlanStep(manifest, left.ID); ok && step.Expect.Reference != "corroborated" {
		class = DiffCodeMismatch
	}
	if a == nil || b == nil {
		add(id, "reference", referenceText(a), referenceText(b), class)
		return
	}
	add(id, "reference.status", a.Status, b.Status, class)
	add(id, "reference.tag", a.Tag, b.Tag, class)
	add(id, "reference.height", strconv.FormatUint(a.Height, 10), strconv.FormatUint(b.Height, 10), class)
	add(id, "reference.hash", a.Hash, b.Hash, class)
	add(id, "reference.origin", a.Origin, b.Origin, DiffProviderFluctuation)
	add(id, "reference.n", strconv.Itoa(a.N), strconv.Itoa(b.N), DiffCodeMismatch)
	add(id, "reference.participants", strings.Join(a.Participants, ","), strings.Join(b.Participants, ","), DiffCodeMismatch)
	add(id, "reference.support", strings.Join(a.Support, ","), strings.Join(b.Support, ","), DiffProviderFluctuation)
	add(id, "reference.reject", strings.Join(a.Reject, ","), strings.Join(b.Reject, ","), DiffProviderFluctuation)
	add(id, "reference.faults", sourceFaultSignature(a.Faults), sourceFaultSignature(b.Faults), DiffProviderFluctuation)
	add(id, "reference.collection_faults", collectionFaultSignature(a.CollectionFaults), collectionFaultSignature(b.CollectionFaults), DiffProviderFluctuation)
}

func compareTyped(id *string, a, b *TypedReport, add func(*string, string, string, string, string)) {
	if a == nil && b == nil {
		return
	}
	if a == nil || b == nil {
		add(id, "typed", typedText(a), typedText(b), DiffCodeMismatch)
		return
	}
	add(id, "typed.kind", a.Kind, b.Kind, DiffCodeMismatch)
	add(id, "typed.ok", strconv.FormatBool(a.OK), strconv.FormatBool(b.OK), DiffProviderFluctuation)
	add(id, "typed.height", pointerUintText(a.Height), pointerUintText(b.Height), DiffProviderFluctuation)
	add(id, "typed.hash", a.Hash, b.Hash, DiffProviderFluctuation)
	add(id, "typed.error_class", a.ErrorClass, b.ErrorClass, DiffProviderFluctuation)
}

func compareSourceHealth(report *ComparisonReport, original, recheck []SourceHealthReport) {
	if len(original) != len(recheck) {
		report.Differences = append(report.Differences, ComparisonDifference{
			Field: "source_health.length", Original: strconv.Itoa(len(original)), Recheck: strconv.Itoa(len(recheck)),
			Classification: DiffCodeMismatch,
		})
		return
	}
	for i := range original {
		a, b := original[i], recheck[i]
		addHealth := func(field, left, right, class string) {
			if left == right {
				return
			}
			report.Differences = append(report.Differences, ComparisonDifference{
				Field: "source_health[" + a.SourceID + "]." + field, Original: left, Recheck: right, Classification: class,
			})
		}
		addHealth("source_id", a.SourceID, b.SourceID, DiffRegistryDrift)
		addHealth("url", a.URL, b.URL, DiffRegistryDrift)
		addHealth("successes", strconv.FormatUint(a.Successes, 10), strconv.FormatUint(b.Successes, 10), DiffProviderFluctuation)
		addHealth("failures", strconv.FormatUint(a.Failures, 10), strconv.FormatUint(b.Failures, 10), DiffProviderFluctuation)
		addHealth("rate_limited", strconv.FormatUint(a.RateLimited, 10), strconv.FormatUint(b.RateLimited, 10), DiffProviderFluctuation)
		addHealth("protocol_server_errors", strconv.FormatUint(a.ProtocolServerErrors, 10), strconv.FormatUint(b.ProtocolServerErrors, 10), DiffProviderFluctuation)
		addHealth("p95_ms", strconv.FormatInt(a.P95MS, 10), strconv.FormatInt(b.P95MS, 10), DiffProviderFluctuation)
		addHealth("last_error_class", a.LastErrorClass, b.LastErrorClass, DiffProviderFluctuation)
	}
}

func classifyStateDiff(a, b string) string {
	if a == b {
		return DiffIdentical
	}
	if a == StateFailed || b == StateFailed {
		return DiffCodeMismatch
	}
	return DiffProviderFluctuation
}

func classifyReasonDiff(a, b []StateReason) string {
	if reasonSignature(a) == reasonSignature(b) {
		return DiffIdentical
	}
	for _, reason := range append(append([]StateReason{}, a...), b...) {
		if categoryFailed(reason.Category) {
			return DiffCodeMismatch
		}
	}
	return DiffProviderFluctuation
}

func reasonSignature(reasons []StateReason) string {
	parts := make([]string, 0, len(reasons))
	for _, reason := range reasons {
		parts = append(parts, reason.Category+"|"+pointerText(reason.StepID)+"|"+reason.SourceID)
	}
	sort.Strings(parts)
	return strings.Join(parts, ";")
}

func sourceFaultSignature(faults []SourceFaultReport) string {
	parts := make([]string, 0, len(faults))
	for _, fault := range faults {
		parts = append(parts, fault.SourceID+":"+fault.Category)
	}
	sort.Strings(parts)
	return strings.Join(parts, ",")
}

func collectionFaultSignature(faults []CollectionFaultReport) string {
	parts := make([]string, 0, len(faults))
	for _, fault := range faults {
		parts = append(parts, fault.Category)
	}
	sort.Strings(parts)
	return strings.Join(parts, ",")
}

func manifestPlanStep(manifest *Manifest, id string) (PlanStep, bool) {
	for _, step := range manifest.Plan.Steps {
		if step.ID == id {
			return step, true
		}
	}
	return PlanStep{}, false
}

func jsonText(value any) string {
	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Sprintf("%v", value)
	}
	return string(data)
}

func requestText(request *RequestReport) string {
	if request == nil {
		return "null"
	}
	return request.Method + " " + jsonText(request.Params)
}

func referenceText(reference *ReferenceReport) string {
	if reference == nil {
		return "null"
	}
	return fmt.Sprintf("%s/%s/%d/%s", reference.Status, reference.Tag, reference.Height, reference.Hash)
}

func decisionText(decision *DecisionReport) string {
	if decision == nil {
		return "null"
	}
	return decision.Status + "/" + decision.ValueKind + "/" + decision.ValueSHA256
}

func typedText(typed *TypedReport) string {
	if typed == nil {
		return "null"
	}
	return fmt.Sprintf("%s/%t/%s/%s/%s", typed.Kind, typed.OK, pointerUintText(typed.Height), typed.Hash, typed.ErrorClass)
}

func pointerText(value *string) string {
	if value == nil {
		return "null"
	}
	return *value
}

func pointerBoolText(value *bool) string {
	if value == nil {
		return "null"
	}
	return strconv.FormatBool(*value)
}

func pointerUintText(value *uint64) string {
	if value == nil {
		return "null"
	}
	return strconv.FormatUint(*value, 10)
}
