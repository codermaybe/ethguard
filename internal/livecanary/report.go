package livecanary

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"
)

const (
	ReportSchemaVersion = "ethguard.live-report/v1"
	ReportArtifactName  = "report.json"
)

const (
	StatePassed       = "passed"
	StateFailed       = "failed"
	StateInconclusive = "inconclusive"
)

const (
	OutcomeOK                   = "ok"
	OutcomeDegraded             = "degraded"
	OutcomeProviderInconclusive = "provider_inconclusive"
	OutcomeInvariantFailed      = "invariant_failed"
)

const (
	ValueKindQuantity = "quantity"
	ValueKindData     = "data"
	ValueKindArray    = "array"
	ValueKindObject   = "object"
	ValueKindNull     = "null"
	ValueKindBool     = "bool"
	ValueKindString   = "string"
)

const (
	TypedKindBlock          = "block"
	TypedKindReceiptAbsence = "receipt_absence"
	ErrorClassNotFound      = "not_found"
)

var reportTimestampRE = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}(?:\.[0-9]{1,9})?Z$`)
var referenceHashRE = regexp.MustCompile(`^0x[0-9a-f]{64}$`)

// Failed and inconclusive categories are the closed reporting vocabularies
// from the live-canary contract. Nothing outside these sets may reach a
// state reason or a fault entry.
var failedCategories = map[string]bool{
	"code_error":          true,
	"request_binding":     true,
	"evidence_missing":    true,
	"illegal_state":       true,
	"identity_mismatch":   true,
	"denominator_changed": true,
	"schema_error":        true,
	"typed_decode":        true,
}

var inconclusiveCategories = map[string]bool{
	"registry_unavailable":       true,
	"insufficient_members":       true,
	"identity_basis_invalid":     true,
	"reference_not_corroborated": true,
	"provider_unavailable":       true,
	"provider_rate_limited":      true,
	"provider_timeout":           true,
	"provider_malformed":         true,
	"provider_error":             true,
	"provider_divergence":        true,
	"quorum_unmet":               true,
	"run_timeout":                true,
}

func validCategory(category string) bool {
	return failedCategories[category] || inconclusiveCategories[category]
}

func categoryFailed(category string) bool { return failedCategories[category] }

type Report struct {
	SchemaVersion   string               `json:"schema_version"`
	RunID           string               `json:"run_id"`
	ManifestSHA256  string               `json:"manifest_sha256"`
	PlanSHA256      string               `json:"plan_sha256"`
	StartedAt       string               `json:"started_at"`
	FinishedAt      string               `json:"finished_at"`
	DurationMS      int64                `json:"duration_ms"`
	State           string               `json:"state"`
	StateReasons    []StateReason        `json:"state_reasons"`
	ReferenceStepID *string              `json:"reference_step_id"`
	Steps           []StepReport         `json:"steps"`
	SourceHealth    []SourceHealthReport `json:"source_health"`
}

type StateReason struct {
	Category string  `json:"category"`
	StepID   *string `json:"step_id"`
	SourceID string  `json:"source_id,omitempty"`
	Detail   string  `json:"detail"`
}

type StepReport struct {
	ID              string           `json:"id"`
	Phase           string           `json:"phase"`
	Path            string           `json:"path"`
	Method          string           `json:"method"`
	LogicalRequest  RequestReport    `json:"logical_request"`
	PreparedRequest *RequestReport   `json:"prepared_request"`
	Reference       *ReferenceReport `json:"reference"`
	Decision        *DecisionReport  `json:"decision"`
	Typed           *TypedReport     `json:"typed"`
	Invariants      InvariantReport  `json:"invariants"`
	Outcome         string           `json:"outcome"`
}

type RequestReport struct {
	Method string `json:"method"`
	Params []any  `json:"params"`
}

type DecisionReport struct {
	Status           string                  `json:"status"`
	ErrorClass       string                  `json:"error_class"`
	ErrorDetail      string                  `json:"error_detail"`
	N                int                     `json:"n"`
	Participants     []string                `json:"participants"`
	Support          []string                `json:"support"`
	Reject           []string                `json:"reject"`
	Faults           []SourceFaultReport     `json:"faults"`
	CollectionFaults []CollectionFaultReport `json:"collection_faults"`
	Representative   string                  `json:"representative"`
	ValueSHA256      string                  `json:"value_sha256"`
	ValueKind        string                  `json:"value_kind"`
}

type ReferenceReport struct {
	Status           string                  `json:"status"`
	Tag              string                  `json:"tag"`
	Height           uint64                  `json:"height"`
	Hash             string                  `json:"hash"`
	Origin           string                  `json:"origin"`
	ObservedAt       string                  `json:"observed_at"`
	N                int                     `json:"n"`
	Participants     []string                `json:"participants"`
	Support          []string                `json:"support"`
	Reject           []string                `json:"reject"`
	Faults           []SourceFaultReport     `json:"faults"`
	CollectionFaults []CollectionFaultReport `json:"collection_faults"`
}

type SourceFaultReport struct {
	SourceID string `json:"source_id"`
	Category string `json:"category"`
	Detail   string `json:"detail"`
}

type CollectionFaultReport struct {
	Category string `json:"category"`
	Detail   string `json:"detail"`
}

type TypedReport struct {
	Kind       string  `json:"kind"`
	OK         bool    `json:"ok"`
	Height     *uint64 `json:"height"`
	Hash       string  `json:"hash"`
	ErrorClass string  `json:"error_class"`
}

type InvariantReport struct {
	RequestBinding          *bool `json:"request_binding"`
	StatusAllowed           *bool `json:"status_allowed"`
	EvidenceConservation    *bool `json:"evidence_conservation"`
	RepresentativeInSupport *bool `json:"representative_in_support"`
	ReferenceStatus         *bool `json:"reference_status"`
	ErrorStatusExclusive    *bool `json:"error_status_exclusive"`
	BasisUnchanged          *bool `json:"basis_unchanged"`
	TypedDecode             *bool `json:"typed_decode"`
	ShapeMatchesPlan        *bool `json:"shape_matches_plan"`
}

type SourceHealthReport struct {
	SourceID             string `json:"source_id"`
	URL                  string `json:"url"`
	Successes            uint64 `json:"successes"`
	Failures             uint64 `json:"failures"`
	RateLimited          uint64 `json:"rate_limited"`
	ProtocolServerErrors uint64 `json:"protocol_server_errors"`
	P95MS                int64  `json:"p95_ms"`
	LastErrorClass       string `json:"last_error_class"`
}

func (r *Report) Validate() error {
	if r == nil {
		return fmt.Errorf("report: nil")
	}
	if r.SchemaVersion != ReportSchemaVersion {
		return fmt.Errorf("schema_version: got %q, want %q", r.SchemaVersion, ReportSchemaVersion)
	}
	if strings.TrimSpace(r.RunID) == "" {
		return fmt.Errorf("run_id: required")
	}
	for name, value := range map[string]string{
		"manifest_sha256": r.ManifestSHA256,
		"plan_sha256":     r.PlanSHA256,
	} {
		if !sha256RE.MatchString(value) {
			return fmt.Errorf("%s: not a lowercase SHA-256", name)
		}
	}
	if r.PlanSHA256 != FrozenPlanSHA256 {
		return fmt.Errorf("plan_sha256: got %q, want %q", r.PlanSHA256, FrozenPlanSHA256)
	}
	if err := validateReportTimestamp("started_at", r.StartedAt); err != nil {
		return err
	}
	if err := validateReportTimestamp("finished_at", r.FinishedAt); err != nil {
		return err
	}
	started, _ := time.Parse(time.RFC3339Nano, r.StartedAt)
	finished, _ := time.Parse(time.RFC3339Nano, r.FinishedAt)
	if finished.Before(started) {
		return fmt.Errorf("finished_at: precedes started_at")
	}
	if r.DurationMS < 0 {
		return fmt.Errorf("duration_ms: must not be negative")
	}
	switch r.State {
	case StatePassed, StateFailed, StateInconclusive:
	default:
		return fmt.Errorf("state: %q is not passed, failed, or inconclusive", r.State)
	}
	failedReasons, inconclusiveReasons := 0, 0
	for i, reason := range r.StateReasons {
		if !validCategory(reason.Category) {
			return fmt.Errorf("state_reasons[%d].category: %q is not a report category", i, reason.Category)
		}
		if categoryFailed(reason.Category) {
			failedReasons++
		} else {
			inconclusiveReasons++
		}
		if reason.StepID != nil && !fixedPlanStepExists(*reason.StepID) {
			return fmt.Errorf("state_reasons[%d].step_id: %q is not a plan step", i, *reason.StepID)
		}
		if err := validateDetail(fmt.Sprintf("state_reasons[%d].detail", i), reason.Detail); err != nil {
			return err
		}
	}
	switch {
	case failedReasons > 0:
		if r.State != StateFailed {
			return fmt.Errorf("state: %q contradicts %d failed reason(s)", r.State, failedReasons)
		}
	case inconclusiveReasons > 0:
		if r.State != StateInconclusive {
			return fmt.Errorf("state: %q contradicts %d inconclusive reason(s)", r.State, inconclusiveReasons)
		}
	default:
		if r.State != StatePassed {
			return fmt.Errorf("state: %q requires at least one state reason", r.State)
		}
	}
	if r.ReferenceStepID != nil {
		if *r.ReferenceStepID != "reference.finalized-balance" {
			return fmt.Errorf("reference_step_id: %q is not the run-reference step", *r.ReferenceStepID)
		}
		step, ok := findStep(r.Steps, *r.ReferenceStepID)
		if !ok || step.Reference == nil || step.Reference.Status != "corroborated" || step.Reference.Hash == "" {
			return fmt.Errorf("reference_step_id: run reference is not corroborated in steps")
		}
	}
	if r.State == StatePassed {
		if r.ReferenceStepID == nil {
			return fmt.Errorf("reference_step_id: passed runs require a corroborated run reference")
		}
		if len(r.Steps) != len(FixedPlan().Steps) {
			return fmt.Errorf("steps: passed runs execute the full fixed plan")
		}
		if len(r.SourceHealth) == 0 {
			return fmt.Errorf("source_health: passed runs require source health evidence")
		}
		for i, step := range r.Steps {
			if step.Decision != nil && step.Decision.Status != "corroborated" {
				return fmt.Errorf("steps[%d].decision.status: passed runs require corroborated decisions", i)
			}
		}
	}
	reportPlan := FixedPlan()
	if len(r.Steps) > len(reportPlan.Steps) {
		return fmt.Errorf("steps: %d steps exceed the fixed plan", len(r.Steps))
	}
	seen := make(map[string]bool, len(r.Steps))
	for i := range r.Steps {
		if err := r.Steps[i].validate(reportPlan.Steps[i]); err != nil {
			return fmt.Errorf("steps[%d]: %w", i, err)
		}
		if seen[r.Steps[i].ID] {
			return fmt.Errorf("steps[%d].id: duplicate %q", i, r.Steps[i].ID)
		}
		seen[r.Steps[i].ID] = true
		if r.State == StatePassed && r.Steps[i].Outcome != OutcomeOK && r.Steps[i].Outcome != OutcomeDegraded {
			return fmt.Errorf("steps[%d].outcome: passed runs cannot contain %q", i, r.Steps[i].Outcome)
		}
	}
	healthSeen := make(map[string]bool, len(r.SourceHealth))
	for i := range r.SourceHealth {
		if err := r.SourceHealth[i].validate(); err != nil {
			return fmt.Errorf("source_health[%d]: %w", i, err)
		}
		if healthSeen[r.SourceHealth[i].SourceID] {
			return fmt.Errorf("source_health[%d].source_id: duplicate %q", i, r.SourceHealth[i].SourceID)
		}
		healthSeen[r.SourceHealth[i].SourceID] = true
	}
	return nil
}

func (s *StepReport) validate(plan PlanStep) error {
	if s.ID != plan.ID {
		return fmt.Errorf("id: got %q, want %q", s.ID, plan.ID)
	}
	if s.Phase != plan.Phase {
		return fmt.Errorf("phase: got %q, want %q", s.Phase, plan.Phase)
	}
	if s.Path != plan.Path {
		return fmt.Errorf("path: got %q, want %q", s.Path, plan.Path)
	}
	if s.Method != plan.Method {
		return fmt.Errorf("method: got %q, want %q", s.Method, plan.Method)
	}
	if s.LogicalRequest.Method != plan.Method {
		return fmt.Errorf("logical_request.method: got %q, want %q", s.LogicalRequest.Method, plan.Method)
	}
	if s.LogicalRequest.Params == nil {
		return fmt.Errorf("logical_request.params: must be an array")
	}
	if s.PreparedRequest != nil {
		if s.PreparedRequest.Method == "" {
			return fmt.Errorf("prepared_request.method: required")
		}
		if s.PreparedRequest.Params == nil {
			return fmt.Errorf("prepared_request.params: must be an array")
		}
	}
	switch s.Outcome {
	case OutcomeOK, OutcomeDegraded, OutcomeProviderInconclusive, OutcomeInvariantFailed:
	default:
		return fmt.Errorf("outcome: %q is not a report outcome", s.Outcome)
	}
	if s.Decision == nil {
		if plan.Expect.Decision == "required" && s.PreparedRequest != nil && s.Outcome == OutcomeOK {
			return fmt.Errorf("decision: required decision is missing")
		}
	} else if err := s.Decision.validate(); err != nil {
		return fmt.Errorf("decision: %w", err)
	}
	if s.Reference != nil {
		if err := s.Reference.validate(); err != nil {
			return fmt.Errorf("reference: %w", err)
		}
	}
	if s.Typed != nil {
		if err := s.Typed.validate(); err != nil {
			return fmt.Errorf("typed: %w", err)
		}
	}
	return nil
}

func (d *DecisionReport) validate() error {
	if !validDecisionStatus(d.Status) {
		return fmt.Errorf("status: %q is not a decision status", d.Status)
	}
	if d.N != len(d.Participants) {
		return fmt.Errorf("n: %d != len(participants) %d", d.N, len(d.Participants))
	}
	if d.Status == "unknown" {
		if d.ErrorClass == "" {
			return fmt.Errorf("error_class: unknown status requires a category")
		}
		if !validCategory(d.ErrorClass) {
			return fmt.Errorf("error_class: %q is not a report category", d.ErrorClass)
		}
	} else {
		if d.ErrorClass != "" {
			return fmt.Errorf("error_class: successful decisions cannot carry %q", d.ErrorClass)
		}
		if !validValueKind(d.ValueKind) {
			return fmt.Errorf("value_kind: %q is not a value kind", d.ValueKind)
		}
		if !sha256RE.MatchString(d.ValueSHA256) {
			return fmt.Errorf("value_sha256: successful decisions require a lowercase SHA-256")
		}
	}
	if err := validateOptionalDetail("error_detail", d.ErrorDetail); err != nil {
		return err
	}
	if err := validateFaultPartition(d.Participants, d.Support, d.Reject, d.Faults); err != nil {
		return err
	}
	for i, fault := range d.Faults {
		if err := fault.validate(); err != nil {
			return fmt.Errorf("faults[%d]: %w", i, err)
		}
	}
	for i, fault := range d.CollectionFaults {
		if err := fault.validate(); err != nil {
			return fmt.Errorf("collection_faults[%d]: %w", i, err)
		}
	}
	if d.Representative != "" {
		if len(d.Support) == 0 || d.Representative != d.Support[0] {
			return fmt.Errorf("representative: %q is not the first supporter", d.Representative)
		}
	} else if d.Status != "unknown" {
		return fmt.Errorf("representative: successful decisions require the first supporter")
	}
	return nil
}

func (r *ReferenceReport) validate() error {
	if !validDecisionStatus(r.Status) {
		return fmt.Errorf("status: %q is not a decision status", r.Status)
	}
	if r.N != len(r.Participants) {
		return fmt.Errorf("n: %d != len(participants) %d", r.N, len(r.Participants))
	}
	if err := validateReportTimestamp("observed_at", r.ObservedAt); err != nil {
		return err
	}
	if r.Hash != "" && !referenceHashRE.MatchString(r.Hash) {
		return fmt.Errorf("hash: not a lowercase 32-byte hash")
	}
	if r.Status == "corroborated" {
		if r.Hash == "" || r.Height == 0 {
			return fmt.Errorf("corroborated references require a hash and height")
		}
		if r.Tag == "" {
			return fmt.Errorf("tag: corroborated references require the requested tag")
		}
		if len(r.Support) == 0 {
			return fmt.Errorf("support: corroborated references require at least one supporter")
		}
	}
	if err := validateFaultPartition(r.Participants, r.Support, r.Reject, r.Faults); err != nil {
		return err
	}
	for i, fault := range r.Faults {
		if err := fault.validate(); err != nil {
			return fmt.Errorf("faults[%d]: %w", i, err)
		}
	}
	for i, fault := range r.CollectionFaults {
		if err := fault.validate(); err != nil {
			return fmt.Errorf("collection_faults[%d]: %w", i, err)
		}
	}
	return nil
}

func (t *TypedReport) validate() error {
	switch t.Kind {
	case TypedKindBlock:
		if t.OK {
			if t.Height == nil || t.Hash == "" {
				return fmt.Errorf("ok block results require height and hash")
			}
			if t.ErrorClass != "" {
				return fmt.Errorf("ok results cannot carry error_class")
			}
		} else if t.ErrorClass == "" || !validCategory(t.ErrorClass) {
			return fmt.Errorf("failed typed results require a category")
		}
	case TypedKindReceiptAbsence:
		if t.OK {
			if t.ErrorClass != ErrorClassNotFound {
				return fmt.Errorf("ok receipt_absence requires error_class %q", ErrorClassNotFound)
			}
			if t.Height != nil || t.Hash != "" {
				return fmt.Errorf("receipt_absence carries no height or hash")
			}
		} else if t.ErrorClass == "" || !validCategory(t.ErrorClass) {
			return fmt.Errorf("failed typed results require a category")
		}
	default:
		return fmt.Errorf("kind: %q is not a typed result kind", t.Kind)
	}
	return nil
}

func (f *SourceFaultReport) validate() error {
	if strings.TrimSpace(f.SourceID) == "" {
		return fmt.Errorf("source_id: required")
	}
	if !validCategory(f.Category) {
		return fmt.Errorf("category: %q is not a report category", f.Category)
	}
	return validateDetail("detail", f.Detail)
}

func (f *CollectionFaultReport) validate() error {
	if !validCategory(f.Category) {
		return fmt.Errorf("category: %q is not a report category", f.Category)
	}
	return validateDetail("detail", f.Detail)
}

func (h *SourceHealthReport) validate() error {
	if strings.TrimSpace(h.SourceID) == "" {
		return fmt.Errorf("source_id: required")
	}
	if strings.TrimSpace(h.URL) == "" {
		return fmt.Errorf("url: required")
	}
	if h.P95MS < 0 {
		return fmt.Errorf("p95_ms: must not be negative")
	}
	if h.LastErrorClass != "" && !validCategory(h.LastErrorClass) {
		return fmt.Errorf("last_error_class: %q is not a report category", h.LastErrorClass)
	}
	return nil
}

func validateFaultPartition(participants, support, reject []string, faults []SourceFaultReport) error {
	index := make(map[string]int, len(participants))
	for i, id := range participants {
		if id == "" {
			return fmt.Errorf("participants[%d]: empty source id", i)
		}
		if _, exists := index[id]; exists {
			return fmt.Errorf("participants[%d]: duplicate source id %q", i, id)
		}
		index[id] = i
	}
	claimed := make(map[string]string, len(participants))
	claim := func(id, group string) error {
		if _, ok := index[id]; !ok {
			return fmt.Errorf("%s: source %q is not a participant", group, id)
		}
		if previous, ok := claimed[id]; ok {
			return fmt.Errorf("%s: source %q already claimed by %s", group, id, previous)
		}
		claimed[id] = group
		return nil
	}
	last := -1
	for _, id := range support {
		if err := claim(id, "support"); err != nil {
			return err
		}
		if index[id] <= last {
			return fmt.Errorf("support: source order does not follow participants")
		}
		last = index[id]
	}
	for _, id := range reject {
		if err := claim(id, "reject"); err != nil {
			return err
		}
	}
	for _, fault := range faults {
		if err := claim(fault.SourceID, "faults"); err != nil {
			return err
		}
	}
	if len(claimed) != len(participants) {
		for _, id := range participants {
			if _, ok := claimed[id]; !ok {
				return fmt.Errorf("participants: source %q has no disposition", id)
			}
		}
	}
	return nil
}

func validateDetail(field, detail string) error {
	if strings.TrimSpace(detail) == "" {
		return fmt.Errorf("%s: required", field)
	}
	return validateOptionalDetail(field, detail)
}

func validateOptionalDetail(field, detail string) error {
	if len(detail) > 1024 {
		return fmt.Errorf("%s: exceeds 1024 bytes", field)
	}
	return nil
}

func validateReportTimestamp(field, value string) error {
	if !reportTimestampRE.MatchString(value) {
		return fmt.Errorf("%s: must be RFC3339 UTC", field)
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return fmt.Errorf("%s: %w", field, err)
	}
	if parsed.IsZero() {
		return fmt.Errorf("%s: zero timestamp is not provenance", field)
	}
	return nil
}

func validDecisionStatus(status string) bool {
	switch status {
	case "unknown", "corroborated", "selected", "forwarded":
		return true
	default:
		return false
	}
}

func validValueKind(kind string) bool {
	switch kind {
	case ValueKindQuantity, ValueKindData, ValueKindArray, ValueKindObject, ValueKindNull, ValueKindBool, ValueKindString:
		return true
	default:
		return false
	}
}

func fixedPlanStepExists(id string) bool {
	for _, step := range FixedPlan().Steps {
		if step.ID == id {
			return true
		}
	}
	return false
}

func findStep(steps []StepReport, id string) (*StepReport, bool) {
	for i := range steps {
		if steps[i].ID == id {
			return &steps[i], true
		}
	}
	return nil, false
}

// Marshal validates before serializing so an incomplete report cannot be
// written as a run artifact.
func (r Report) Marshal() ([]byte, error) {
	if err := r.Validate(); err != nil {
		return nil, err
	}
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("report: marshal: %w", err)
	}
	return append(data, '\n'), nil
}

func ParseReport(data []byte) (*Report, error) {
	if err := checkReportRequiredFields(data); err != nil {
		return nil, err
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	dec.DisallowUnknownFields()
	var report Report
	if err := dec.Decode(&report); err != nil {
		return nil, fmt.Errorf("report: parse: %w", err)
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return nil, fmt.Errorf("report: trailing data")
	}
	if err := report.Validate(); err != nil {
		return nil, err
	}
	return &report, nil
}

// checkReportRequiredFields makes required-key presence explicit. The struct
// decoder alone cannot distinguish an absent required field from its zero
// value, and a report missing evidence must not parse as valid.
func checkReportRequiredFields(data []byte) error {
	var top map[string]json.RawMessage
	if err := json.Unmarshal(data, &top); err != nil {
		return fmt.Errorf("report: parse: %w", err)
	}
	if err := requireKeys("$", top,
		"schema_version", "run_id", "manifest_sha256", "plan_sha256",
		"started_at", "finished_at", "duration_ms", "state", "state_reasons",
		"reference_step_id", "steps", "source_health"); err != nil {
		return err
	}
	if err := eachArray("$.state_reasons", top["state_reasons"], func(path string, raw json.RawMessage) error {
		return checkObject(path, raw, []string{"category", "step_id", "detail"}, nil)
	}); err != nil {
		return err
	}
	if err := eachArray("$.steps", top["steps"], checkStepFields); err != nil {
		return err
	}
	if err := eachArray("$.source_health", top["source_health"], func(path string, raw json.RawMessage) error {
		return checkObject(path, raw, []string{
			"source_id", "url", "successes", "failures", "rate_limited",
			"protocol_server_errors", "p95_ms", "last_error_class",
		}, nil)
	}); err != nil {
		return err
	}
	return nil
}

func checkStepFields(path string, raw json.RawMessage) error {
	return checkObject(path, raw, []string{
		"id", "phase", "path", "method", "logical_request", "prepared_request",
		"reference", "decision", "typed", "invariants", "outcome",
	}, func(object map[string]json.RawMessage) error {
		if err := checkRequest(path+".logical_request", object["logical_request"]); err != nil {
			return err
		}
		if err := checkNullableObject(path+".prepared_request", object["prepared_request"], func(path string, object map[string]json.RawMessage) error {
			return checkRequestObject(path, object)
		}); err != nil {
			return err
		}
		if err := checkNullableObject(path+".reference", object["reference"], func(path string, object map[string]json.RawMessage) error {
			return checkFaultLists(path, object, []string{
				"status", "tag", "height", "hash", "origin", "observed_at", "n",
				"participants", "support", "reject", "faults", "collection_faults",
			})
		}); err != nil {
			return err
		}
		if err := checkNullableObject(path+".decision", object["decision"], func(path string, object map[string]json.RawMessage) error {
			return checkFaultLists(path, object, []string{
				"status", "error_class", "error_detail", "n", "participants",
				"support", "reject", "faults", "collection_faults", "representative",
				"value_sha256", "value_kind",
			})
		}); err != nil {
			return err
		}
		if err := checkNullableObject(path+".typed", object["typed"], func(path string, object map[string]json.RawMessage) error {
			return requireKeys(path, object, "kind", "ok", "height", "hash", "error_class")
		}); err != nil {
			return err
		}
		return checkObject(path+".invariants", object["invariants"], []string{
			"request_binding", "status_allowed", "evidence_conservation",
			"representative_in_support", "reference_status",
			"error_status_exclusive", "basis_unchanged", "typed_decode",
			"shape_matches_plan",
		}, nil)
	})
}

func checkRequest(path string, raw json.RawMessage) error {
	return checkObject(path, raw, []string{"method", "params"}, func(object map[string]json.RawMessage) error {
		return checkArray(path+".params", object["params"])
	})
}

func checkRequestObject(path string, object map[string]json.RawMessage) error {
	if err := requireKeys(path, object, "method", "params"); err != nil {
		return err
	}
	return checkArray(path+".params", object["params"])
}

func checkFaultLists(path string, object map[string]json.RawMessage, keys []string) error {
	if err := requireKeys(path, object, keys...); err != nil {
		return err
	}
	if err := eachArray(path+".faults", object["faults"], func(path string, raw json.RawMessage) error {
		return checkObject(path, raw, []string{"source_id", "category", "detail"}, nil)
	}); err != nil {
		return err
	}
	return eachArray(path+".collection_faults", object["collection_faults"], func(path string, raw json.RawMessage) error {
		return checkObject(path, raw, []string{"category", "detail"}, nil)
	})
}

func checkObject(path string, raw json.RawMessage, keys []string, next func(map[string]json.RawMessage) error) error {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil || object == nil {
		return fmt.Errorf("%s: must be an object", path)
	}
	if err := requireKeys(path, object, keys...); err != nil {
		return err
	}
	if next != nil {
		return next(object)
	}
	return nil
}

func checkNullableObject(path string, raw json.RawMessage, check func(string, map[string]json.RawMessage) error) error {
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil || object == nil {
		return fmt.Errorf("%s: must be an object or null", path)
	}
	return check(path, object)
}

func eachArray(path string, raw json.RawMessage, check func(string, json.RawMessage) error) error {
	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil || items == nil {
		return fmt.Errorf("%s: must be an array", path)
	}
	for i, item := range items {
		if err := check(fmt.Sprintf("%s[%d]", path, i), item); err != nil {
			return err
		}
	}
	return nil
}

func checkArray(path string, raw json.RawMessage) error {
	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil || items == nil {
		return fmt.Errorf("%s: must be an array", path)
	}
	return nil
}

func requireKeys(path string, object map[string]json.RawMessage, keys ...string) error {
	for _, key := range keys {
		if _, ok := object[key]; !ok {
			return fmt.Errorf("%s: missing required property %q", path, key)
		}
	}
	return nil
}

func ReportSHA256(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
