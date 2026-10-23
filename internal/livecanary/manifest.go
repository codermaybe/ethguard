// Package livecanary builds the machine-generated provenance manifest for the
// T10 live canary. It performs no network I/O: registry bytes are supplied by
// the caller and every manifest field is derived from those bytes, fixed
// constants, a snapshot descriptor, and runner-detected provenance.
package livecanary

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"
	"time"
)

const (
	SchemaVersion         = "ethguard.live-manifest/v1"
	SnapshotSchemaVersion = "ethguard.registry-snapshot/v1"
	ToolVersion           = "ethguard-live-canary/v1"
	SelectionAlgorithm    = "chainlist-sorted-v1"
	PlanID                = "mainnet-canary-v1"
	ArtifactName          = "registry.json"

	// ChainlistURL is the only registry source accepted by v1.
	ChainlistURL = "https://chainlist.org/rpcs.json"

	// FrozenPlanSHA256 is the canonical hash of FixedPlan without its own
	// sha256 field. Changing the plan requires a new plan ID and schema.
	FrozenPlanSHA256 = "d99a78e6a56ffe34ee5351fbd74b14951afb5fef31023280d9d9412b3b2124d8"

	ChainID    uint64 = 1
	MinMembers        = 3
	MaxMembers        = 12

	ReferencePolicy = "finalized"
	MaxLagBlocks    = 32

	RunTimeoutMS       = 300000
	RegistryTimeoutMS  = 60000
	ReferenceTimeoutMS = 30000
	StepTimeoutMS      = 30000
	RegistryMaxBytes   = 33554432
)

const (
	RedactionNone           = "none"
	RedactionURLCredentials = "url_credentials"
)

var (
	timestampRE    = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z$`)
	sourceCommitRE = regexp.MustCompile(`^[0-9a-f]{40}$`)
	sha256RE       = regexp.MustCompile(`^[0-9a-f]{64}$`)
	artifactRE     = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)
)

type Manifest struct {
	SchemaVersion string      `json:"schema_version"`
	RunID         string      `json:"run_id"`
	GeneratedAt   string      `json:"generated_at"`
	SourceCommit  string      `json:"source_commit"`
	ToolVersion   string      `json:"tool_version"`
	GoVersion     string      `json:"go_version"`
	Environment   Environment `json:"environment"`
	ChainID       uint64      `json:"chain_id"`
	Registry      Registry    `json:"registry"`
	Selection     *Selection  `json:"selection"`
	Reference     Reference   `json:"reference"`
	Timeouts      Timeouts    `json:"timeouts"`
	Limits        Limits      `json:"limits"`
	Plan          Plan        `json:"plan"`
}

type Environment struct {
	Kind        string         `json:"kind"`
	RunnerClass string         `json:"runner_class"`
	CI          *CIEnvironment `json:"ci"`
}

type CIEnvironment struct {
	Provider    string `json:"provider"`
	Repository  string `json:"repository"`
	WorkflowRef string `json:"workflow_ref"`
	RunID       string `json:"run_id"`
	RunAttempt  string `json:"run_attempt"`
	Job         string `json:"job"`
}

type Registry struct {
	Status         string  `json:"status"`
	URL            string  `json:"url"`
	FetchedAt      string  `json:"fetched_at"`
	RawSHA256      *string `json:"raw_sha256"`
	Artifact       *string `json:"artifact"`
	ArtifactSHA256 *string `json:"artifact_sha256"`
	Redaction      string  `json:"redaction"`
}

type Selection struct {
	Algorithm         string           `json:"algorithm"`
	MinMembers        int              `json:"min_members"`
	MaxMembers        int              `json:"max_members"`
	CandidateCount    int              `json:"candidate_count"`
	Selected          []SelectedMember `json:"selected"`
	Filtered          []FilteredEntry  `json:"filtered"`
	VotingBasisSHA256 string           `json:"voting_basis_sha256"`
}

type SelectedMember struct {
	SourceID string `json:"source_id"`
	URL      string `json:"url"`
}

type FilteredEntry struct {
	URLSHA256 string `json:"url_sha256"`
	Reason    string `json:"reason"`
}

type Reference struct {
	Policy       string `json:"policy"`
	MaxLagBlocks uint64 `json:"max_lag_blocks"`
}

type Timeouts struct {
	RunMS       int `json:"run_ms"`
	RegistryMS  int `json:"registry_ms"`
	ReferenceMS int `json:"reference_ms"`
	StepMS      int `json:"step_ms"`
}

type Limits struct {
	RegistryMaxBytes int `json:"registry_max_bytes"`
}

type Plan struct {
	ID     string     `json:"id"`
	SHA256 string     `json:"sha256"`
	Steps  []PlanStep `json:"steps"`
}

type PlanStep struct {
	ID     string     `json:"id"`
	Phase  string     `json:"phase"`
	Path   string     `json:"path"`
	Method string     `json:"method"`
	Params []any      `json:"params"`
	Expect PlanExpect `json:"expect"`
}

type PlanExpect struct {
	Decision  string   `json:"decision"`
	Statuses  []string `json:"statuses"`
	Error     string   `json:"error"`
	Reference string   `json:"reference"`
	Binding   string   `json:"binding"`
	ValueKind string   `json:"value_kind"`
}

// FixedPlan returns the frozen mainnet canary plan. The plan is a constant:
// callers must not derive it from provider results or environment input.
func FixedPlan() Plan {
	return Plan{
		ID:     PlanID,
		SHA256: FrozenPlanSHA256,
		Steps: []PlanStep{
			{
				ID:     "identity.chain-id",
				Phase:  "identity",
				Path:   "client_call",
				Method: "eth_chainId",
				Params: []any{},
				Expect: PlanExpect{
					Decision:  "required",
					Statuses:  []string{"corroborated"},
					Error:     "none",
					Reference: "none",
					Binding:   "none",
					ValueKind: "quantity",
				},
			},
			{
				ID:     "reference.finalized-balance",
				Phase:  "reference",
				Path:   "client_call",
				Method: "eth_getBalance",
				Params: []any{"0xd8dA6BF26964aF9D7eEd9e03E53415D37aA96045", "finalized"},
				Expect: PlanExpect{
					Decision:  "required",
					Statuses:  []string{"corroborated"},
					Error:     "none",
					Reference: "corroborated",
					Binding:   "reference_hash",
					ValueKind: "quantity",
				},
			},
			{
				ID:     "pinned.block",
				Phase:  "business",
				Path:   "client_call",
				Method: "eth_getBlockByHash",
				Params: []any{"$reference.hash", false},
				Expect: PlanExpect{
					Decision:  "required",
					Statuses:  []string{"corroborated"},
					Error:     "none",
					Reference: "none",
					Binding:   "none",
					ValueKind: "object",
				},
			},
			{
				ID:     "business.fee-history.finalized",
				Phase:  "business",
				Path:   "client_call",
				Method: "eth_feeHistory",
				Params: []any{2, "finalized", []any{25, 50, 75}},
				Expect: PlanExpect{
					Decision:  "required",
					Statuses:  []string{"corroborated"},
					Error:     "none",
					Reference: "corroborated",
					Binding:   "reference_height",
					ValueKind: "object",
				},
			},
			{
				ID:     "typed.block",
				Phase:  "typed",
				Path:   "typed_block",
				Method: "eth_getBlockByHash",
				Params: []any{"$reference.hash"},
				Expect: PlanExpect{
					Decision:  "none",
					Statuses:  []string{},
					Error:     "none",
					Reference: "none",
					Binding:   "none",
					ValueKind: "none",
				},
			},
			{
				ID:     "negative.notfound-receipt",
				Phase:  "negative",
				Path:   "typed_receipt",
				Method: "eth_getTransactionReceipt",
				Params: []any{"0x0000000000000000000000000000000000000000000000000000000000000000"},
				Expect: PlanExpect{
					Decision:  "required",
					Statuses:  []string{"corroborated"},
					Error:     "not_found",
					Reference: "none",
					Binding:   "none",
					ValueKind: "null",
				},
			},
		},
	}
}

// ComputePlanSHA256 hashes the canonical JSON of {id, steps}; sha256 itself is
// excluded so the digest is not self-referential.
func ComputePlanSHA256(plan Plan) (string, error) {
	payload := struct {
		ID    string     `json:"id"`
		Steps []PlanStep `json:"steps"`
	}{ID: plan.ID, Steps: plan.Steps}
	return canonicalSHA256(payload)
}

func (p Plan) validateFixed() error {
	if p.ID != PlanID {
		return fmt.Errorf("plan.id: got %q, want %q", p.ID, PlanID)
	}
	if p.SHA256 != FrozenPlanSHA256 {
		return fmt.Errorf("plan.sha256: got %q, want %q", p.SHA256, FrozenPlanSHA256)
	}
	got, err := ComputePlanSHA256(p)
	if err != nil {
		return fmt.Errorf("plan.sha256: %w", err)
	}
	if got != FrozenPlanSHA256 {
		return fmt.Errorf("plan.sha256: recomputed %q, want %q", got, FrozenPlanSHA256)
	}
	fixed := FixedPlan()
	want, err := canonicalJSON(struct {
		ID    string     `json:"id"`
		Steps []PlanStep `json:"steps"`
	}{ID: fixed.ID, Steps: fixed.Steps})
	if err != nil {
		return fmt.Errorf("plan: %w", err)
	}
	have, err := canonicalJSON(struct {
		ID    string     `json:"id"`
		Steps []PlanStep `json:"steps"`
	}{ID: p.ID, Steps: p.Steps})
	if err != nil {
		return fmt.Errorf("plan: %w", err)
	}
	if !bytes.Equal(have, want) {
		return fmt.Errorf("plan.steps: does not match frozen %s", PlanID)
	}
	return nil
}

func (e Environment) validate() error {
	switch e.Kind {
	case "local":
		if e.RunnerClass != "local" {
			return fmt.Errorf("environment.runner_class: local runs require %q", "local")
		}
		if e.CI != nil {
			return fmt.Errorf("environment.ci: must be null for local runs")
		}
	case "ci":
		switch e.RunnerClass {
		case "github-hosted", "self-hosted":
		default:
			return fmt.Errorf("environment.runner_class: %q is not a CI runner class", e.RunnerClass)
		}
		if e.CI == nil {
			return fmt.Errorf("environment.ci: required for ci runs")
		}
		return e.CI.validate()
	default:
		return fmt.Errorf("environment.kind: %q is not ci or local", e.Kind)
	}
	return nil
}

func (c CIEnvironment) validate() error {
	if c.Provider != "github-actions" {
		return fmt.Errorf("environment.ci.provider: %q is not github-actions", c.Provider)
	}
	for name, value := range map[string]string{
		"repository":   c.Repository,
		"workflow_ref": c.WorkflowRef,
		"run_id":       c.RunID,
		"run_attempt":  c.RunAttempt,
		"job":          c.Job,
	} {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("environment.ci.%s: required", name)
		}
	}
	return nil
}

func (r Registry) validate() error {
	if r.URL != ChainlistURL {
		return fmt.Errorf("registry.url: got %q, want %q", r.URL, ChainlistURL)
	}
	if err := validateTimestamp("registry.fetched_at", r.FetchedAt); err != nil {
		return err
	}
	switch r.Status {
	case "ok":
		if err := validateSHA256("registry.raw_sha256", r.RawSHA256); err != nil {
			return err
		}
		if r.Artifact == nil {
			return fmt.Errorf("registry.artifact: required when status is ok")
		}
		if err := validateArtifactName(*r.Artifact); err != nil {
			return err
		}
		if err := validateSHA256("registry.artifact_sha256", r.ArtifactSHA256); err != nil {
			return err
		}
		switch r.Redaction {
		case RedactionNone:
			if *r.RawSHA256 != *r.ArtifactSHA256 {
				return fmt.Errorf("registry.redaction: none requires raw_sha256 == artifact_sha256")
			}
		case RedactionURLCredentials:
		default:
			return fmt.Errorf("registry.redaction: %q is not a redaction mode", r.Redaction)
		}
	case "unavailable":
		if r.RawSHA256 != nil || r.Artifact != nil || r.ArtifactSHA256 != nil {
			return fmt.Errorf("registry: unavailable entries must not publish artifact fields")
		}
		if r.Redaction != RedactionNone {
			return fmt.Errorf("registry.redaction: unavailable entries cannot have a redacted artifact")
		}
	default:
		return fmt.Errorf("registry.status: %q is not ok or unavailable", r.Status)
	}
	return nil
}

func (s *Selection) validate() error {
	if s.Algorithm != SelectionAlgorithm {
		return fmt.Errorf("selection.algorithm: got %q, want %q", s.Algorithm, SelectionAlgorithm)
	}
	if s.MinMembers != MinMembers {
		return fmt.Errorf("selection.min_members: got %d, want %d", s.MinMembers, MinMembers)
	}
	if s.MaxMembers != MaxMembers {
		return fmt.Errorf("selection.max_members: got %d, want %d", s.MaxMembers, MaxMembers)
	}
	if s.CandidateCount < 0 {
		return fmt.Errorf("selection.candidate_count: must not be negative")
	}
	if len(s.Selected) > s.MaxMembers {
		return fmt.Errorf("selection.selected: %d members exceeds max_members %d", len(s.Selected), s.MaxMembers)
	}
	if s.Selected == nil || s.Filtered == nil {
		return fmt.Errorf("selection: selected and filtered must be arrays")
	}
	if s.CandidateCount != len(s.Selected)+len(s.Filtered) {
		return fmt.Errorf("selection.candidate_count: %d != selected(%d)+filtered(%d)",
			s.CandidateCount, len(s.Selected), len(s.Filtered))
	}
	previous := ""
	for i, member := range s.Selected {
		wantID := fmt.Sprintf("node-%02d", i+1)
		if member.SourceID != wantID {
			return fmt.Errorf("selection.selected[%d].source_id: got %q, want %q", i, member.SourceID, wantID)
		}
		if err := validateSelectedURL(fmt.Sprintf("selection.selected[%d].url", i), member.URL); err != nil {
			return err
		}
		identity, err := EndpointIdentity(member.URL)
		if err != nil {
			return fmt.Errorf("selection.selected[%d].url: %w", i, err)
		}
		if i > 0 && previous >= identity {
			return fmt.Errorf("selection.selected[%d]: not sorted by endpoint identity", i)
		}
		previous = identity
	}
	for i, entry := range s.Filtered {
		if !sha256RE.MatchString(entry.URLSHA256) {
			return fmt.Errorf("selection.filtered[%d].url_sha256: not a lowercase SHA-256", i)
		}
		if !validFilterReason(entry.Reason) {
			return fmt.Errorf("selection.filtered[%d].reason: %q is not a filter reason", i, entry.Reason)
		}
	}
	if s.VotingBasisSHA256 != VotingBasisSHA256(s.Selected) {
		return fmt.Errorf("selection.voting_basis_sha256: does not match selected members")
	}
	return nil
}

func (m *Manifest) Validate() error {
	if m == nil {
		return fmt.Errorf("manifest: nil")
	}
	if m.SchemaVersion != SchemaVersion {
		return fmt.Errorf("schema_version: got %q, want %q", m.SchemaVersion, SchemaVersion)
	}
	if strings.TrimSpace(m.RunID) == "" {
		return fmt.Errorf("run_id: required")
	}
	if err := validateTimestamp("generated_at", m.GeneratedAt); err != nil {
		return err
	}
	if !sourceCommitRE.MatchString(m.SourceCommit) {
		return fmt.Errorf("source_commit: must be a 40-character lowercase git SHA")
	}
	if m.ToolVersion != ToolVersion {
		return fmt.Errorf("tool_version: got %q, want %q", m.ToolVersion, ToolVersion)
	}
	if !strings.HasPrefix(m.GoVersion, "go") || len(m.GoVersion) < 3 {
		return fmt.Errorf("go_version: %q is not a Go toolchain version", m.GoVersion)
	}
	if err := m.Environment.validate(); err != nil {
		return err
	}
	if m.ChainID != ChainID {
		return fmt.Errorf("chain_id: got %d, want %d", m.ChainID, ChainID)
	}
	if err := m.Registry.validate(); err != nil {
		return err
	}
	if m.Registry.Status == "ok" {
		if m.Selection == nil {
			return fmt.Errorf("selection: required when registry.status is ok")
		}
		if err := m.Selection.validate(); err != nil {
			return err
		}
	} else if m.Selection != nil {
		return fmt.Errorf("selection: must be null when registry.status is unavailable")
	}
	if m.Reference.Policy != ReferencePolicy {
		return fmt.Errorf("reference.policy: got %q, want %q", m.Reference.Policy, ReferencePolicy)
	}
	if m.Reference.MaxLagBlocks != MaxLagBlocks {
		return fmt.Errorf("reference.max_lag_blocks: got %d, want %d", m.Reference.MaxLagBlocks, MaxLagBlocks)
	}
	if m.Timeouts != (Timeouts{
		RunMS:       RunTimeoutMS,
		RegistryMS:  RegistryTimeoutMS,
		ReferenceMS: ReferenceTimeoutMS,
		StepMS:      StepTimeoutMS,
	}) {
		return fmt.Errorf("timeouts: do not match the frozen values")
	}
	if m.Limits.RegistryMaxBytes != RegistryMaxBytes {
		return fmt.Errorf("limits.registry_max_bytes: got %d, want %d", m.Limits.RegistryMaxBytes, RegistryMaxBytes)
	}
	return m.Plan.validateFixed()
}

// Marshal validates before serializing so a manifest with missing provenance
// cannot be written as a run artifact.
func (m Manifest) Marshal() ([]byte, error) {
	if err := m.Validate(); err != nil {
		return nil, err
	}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("manifest: marshal: %w", err)
	}
	return append(data, '\n'), nil
}

func ParseManifest(data []byte) (*Manifest, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	dec.DisallowUnknownFields()
	var manifest Manifest
	if err := dec.Decode(&manifest); err != nil {
		return nil, fmt.Errorf("manifest: parse: %w", err)
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return nil, fmt.Errorf("manifest: trailing data")
	}
	if err := manifest.Validate(); err != nil {
		return nil, err
	}
	return &manifest, nil
}

func ManifestSHA256(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func formatProvenanceTime(field string, value time.Time) (string, error) {
	if value.IsZero() {
		return "", fmt.Errorf("%s: required", field)
	}
	return value.UTC().Truncate(time.Second).Format(time.RFC3339), nil
}

func validateTimestamp(field, value string) error {
	if !timestampRE.MatchString(value) {
		return fmt.Errorf("%s: must be RFC3339 UTC with second precision", field)
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return fmt.Errorf("%s: %w", field, err)
	}
	if parsed.IsZero() {
		return fmt.Errorf("%s: zero timestamp is not provenance", field)
	}
	return nil
}

func validateSHA256(field string, value *string) error {
	if value == nil {
		return fmt.Errorf("%s: required", field)
	}
	if !sha256RE.MatchString(*value) {
		return fmt.Errorf("%s: not a lowercase SHA-256", field)
	}
	return nil
}

func validateArtifactName(name string) error {
	if !artifactRE.MatchString(name) || name == "." || name == ".." {
		return fmt.Errorf("registry.artifact: %q is not a plain artifact file name", name)
	}
	return nil
}

func canonicalSHA256(value any) (string, error) {
	data, err := canonicalJSON(value)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

// canonicalJSON emits compact JSON with object keys sorted at every level.
// Arrays keep their order; plan hash stability depends on that split.
func canonicalJSON(value any) ([]byte, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var decoded any
	if err := dec.Decode(&decoded); err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	if err := writeCanonical(&buf, decoded); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func writeCanonical(buf *bytes.Buffer, value any) error {
	switch typed := value.(type) {
	case nil:
		buf.WriteString("null")
	case bool:
		if typed {
			buf.WriteString("true")
		} else {
			buf.WriteString("false")
		}
	case string:
		encoded, err := json.Marshal(typed)
		if err != nil {
			return err
		}
		buf.Write(encoded)
	case json.Number:
		buf.WriteString(typed.String())
	case map[string]any:
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		buf.WriteByte('{')
		for i, key := range keys {
			if i > 0 {
				buf.WriteByte(',')
			}
			encoded, err := json.Marshal(key)
			if err != nil {
				return err
			}
			buf.Write(encoded)
			buf.WriteByte(':')
			if err := writeCanonical(buf, typed[key]); err != nil {
				return err
			}
		}
		buf.WriteByte('}')
	case []any:
		buf.WriteByte('[')
		for i, item := range typed {
			if i > 0 {
				buf.WriteByte(',')
			}
			if err := writeCanonical(buf, item); err != nil {
				return err
			}
		}
		buf.WriteByte(']')
	default:
		return fmt.Errorf("canonical json: unsupported value %T", value)
	}
	return nil
}
