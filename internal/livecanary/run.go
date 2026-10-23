package livecanary

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strconv"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"

	"github.com/codermaybe/ethguard"
	"github.com/codermaybe/ethguard/config"
	"github.com/codermaybe/ethguard/ethadapter"
	"github.com/codermaybe/ethguard/rpc"
	"github.com/codermaybe/ethguard/rpc/methods"
)

const runReferenceStepID = "reference.finalized-balance"

var errBasisChanged = errors.New("livecanary: client voting basis differs from manifest selection")

// Executor executes the frozen plan against the members fixed by the manifest.
// NewClient is injectable for offline tests; production callers leave it nil
// so the executor builds the client from the selected HTTPS URLs.
type Executor struct {
	Now       func() time.Time
	NewClient func(Manifest) (*ethguard.Client, error)
}

type RunInput struct {
	Manifest       Manifest
	ManifestBytes  []byte
	ManifestSHA256 string
	Membership     MembershipState
}

// Run executes one fixed-plan observation and returns a report. A report with
// state failed or inconclusive is still a successful observation product; an
// error return means the report itself could not be established from this
// manifest, which is the contract's no-report exception.
func (e Executor) Run(ctx context.Context, input RunInput) (report Report, err error) {
	now := e.Now
	if now == nil {
		now = time.Now
	}
	started := now()
	var state *runState
	defer func() {
		if recovered := recover(); recovered != nil {
			if state == nil {
				report = Report{}
				err = fmt.Errorf("run: panic before run state: %v", recovered)
				return
			}
			state.addReason("code_error", nil, "", fmt.Sprintf("panic: %v", recovered))
			report, err = state.finish()
		}
	}()
	if len(input.ManifestBytes) == 0 {
		return Report{}, fmt.Errorf("run: manifest bytes are required")
	}
	actualManifestSHA := ManifestSHA256(input.ManifestBytes)
	parsed, err := ParseManifest(input.ManifestBytes)
	if err != nil {
		return Report{}, fmt.Errorf("run: %w", err)
	}
	manifest := *parsed
	state = &runState{
		manifest:       manifest,
		manifestSHA256: actualManifestSHA,
		started:        started,
		now:            now,
	}
	if input.ManifestSHA256 != actualManifestSHA {
		state.addReason("schema_error", nil, "",
			fmt.Sprintf("manifest_sha256 %q does not bind the manifest bytes (%s)", input.ManifestSHA256, actualManifestSHA))
		return state.finish()
	}
	if !manifestStructureMatches(manifest, input.Manifest) {
		state.addReason("schema_error", nil, "", "manifest argument does not match the manifest bytes")
		return state.finish()
	}
	if input.Membership != MembershipFixed {
		state.addPrePlanReason(input.Membership)
		return state.finish()
	}
	if manifest.Registry.Status != "ok" || manifest.Selection == nil {
		state.addReason("registry_unavailable", nil, "", "registry selection is unavailable")
		return state.finish()
	}
	if len(manifest.Selection.Selected) < MinMembers {
		state.addReason("insufficient_members", nil, "",
			fmt.Sprintf("selected %d members, need %d", len(manifest.Selection.Selected), MinMembers))
		return state.finish()
	}
	if err := ctx.Err(); err != nil {
		state.addReason("run_timeout", nil, "", sanitizeDetail(err.Error()))
		return state.finish()
	}
	client, err := e.clientFor(manifest)
	if err != nil {
		if errors.Is(err, errBasisChanged) {
			state.addReason("denominator_changed", nil, "", sanitizeDetail(err.Error()))
		} else {
			state.addReason("code_error", nil, "", sanitizeDetail(err.Error()))
		}
		return state.finish()
	}
	defer client.Close()
	state.client = client
	if err := state.checkBasis(); err != nil {
		state.addReason("denominator_changed", nil, "", sanitizeDetail(err.Error()))
		return state.finish()
	}
	state.execute(ctx)
	state.collectSourceHealth()
	return state.finish()
}

func manifestStructureMatches(a, b Manifest) bool {
	left, err := json.Marshal(a)
	if err != nil {
		return false
	}
	right, err := json.Marshal(b)
	if err != nil {
		return false
	}
	return bytes.Equal(left, right)
}

func (e Executor) clientFor(manifest Manifest) (*ethguard.Client, error) {
	if e.NewClient != nil {
		return e.NewClient(manifest)
	}
	selected := manifest.Selection.Selected
	upstreams := make([]config.Upstream, 0, len(selected))
	for _, member := range selected {
		upstreams = append(upstreams, config.Upstream{ID: member.SourceID, Endpoint: member.URL})
	}
	cfg := config.Config{
		ChainID:   manifest.ChainID,
		Upstreams: upstreams,
		Anchoring: &config.AnchoringConfig{
			Preset: "finalized",
			MaxLag: manifest.Reference.MaxLagBlocks,
		},
		CollectTimeout: fmt.Sprintf("%dms", manifest.Timeouts.StepMS),
	}
	client, err := ethguard.New(cfg,
		ethguard.WithCollectTimeout(time.Duration(manifest.Timeouts.StepMS)*time.Millisecond),
		ethguard.WithFinalizedAnchoring(ethguard.WithMaxFinalizedLag(manifest.Reference.MaxLagBlocks)),
	)
	if err != nil {
		return nil, err
	}
	sources := client.Pool().Sources()
	if len(sources) != len(selected) {
		client.Close()
		return nil, fmt.Errorf("%w: %d client sources, %d selected", errBasisChanged, len(sources), len(selected))
	}
	for i, source := range sources {
		if source.Name() != selected[i].SourceID || source.URL() != selected[i].URL {
			client.Close()
			return nil, fmt.Errorf("%w: source %d is %s/%s, want %s/%s",
				errBasisChanged, i, source.Name(), source.URL(), selected[i].SourceID, selected[i].URL)
		}
	}
	return client, nil
}

type runState struct {
	manifest       Manifest
	manifestSHA256 string
	started        time.Time
	now            func() time.Time
	client         *ethguard.Client
	steps          []StepReport
	reasons        []StateReason
	runReference   *rpc.ReferenceEvidence
	sourceHealth   []SourceHealthReport
}

func (s *runState) addPrePlanReason(membership MembershipState) {
	switch membership {
	case MembershipInsufficient:
		s.addReason("insufficient_members", nil, "",
			fmt.Sprintf("selected %d members, need %d", selectedCount(s.manifest), MinMembers))
	default:
		detail := "registry acquisition failed or the target chain is absent"
		if s.manifest.Registry.Status == "unavailable" {
			detail = "registry unavailable"
		}
		s.addReason("registry_unavailable", nil, "", detail)
	}
}

func selectedCount(manifest Manifest) int {
	if manifest.Selection == nil {
		return 0
	}
	return len(manifest.Selection.Selected)
}

func (s *runState) addReason(category string, stepID *string, sourceID, detail string) {
	detail = sanitizeDetail(detail)
	if detail == "" {
		detail = category
	}
	for _, existing := range s.reasons {
		if existing.Category == category && existing.SourceID == sourceID && sameStepID(existing.StepID, stepID) {
			return
		}
	}
	s.reasons = append(s.reasons, StateReason{Category: category, StepID: stepID, SourceID: sourceID, Detail: detail})
}

func sameStepID(a, b *string) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

func (s *runState) checkBasis() error {
	if s.manifest.Selection == nil {
		return fmt.Errorf("%w: manifest has no selection", errBasisChanged)
	}
	if got := VotingBasisSHA256(s.manifest.Selection.Selected); got != s.manifest.Selection.VotingBasisSHA256 {
		return fmt.Errorf("%w: voting basis digest %s, want %s", errBasisChanged, got, s.manifest.Selection.VotingBasisSHA256)
	}
	sources := s.client.Pool().Sources()
	if len(sources) != len(s.manifest.Selection.Selected) {
		return fmt.Errorf("%w: %d client sources, %d selected", errBasisChanged, len(sources), len(s.manifest.Selection.Selected))
	}
	for i, source := range sources {
		if source.Name() != s.manifest.Selection.Selected[i].SourceID {
			return fmt.Errorf("%w: source %d is %q, want %q", errBasisChanged, i, source.Name(), s.manifest.Selection.Selected[i].SourceID)
		}
	}
	return nil
}

func (s *runState) selectedIDs() []string {
	ids := make([]string, 0, len(s.manifest.Selection.Selected))
	for _, member := range s.manifest.Selection.Selected {
		ids = append(ids, member.SourceID)
	}
	return ids
}

func (s *runState) execute(parent context.Context) {
	runCtx, cancel := context.WithTimeout(parent, time.Duration(s.manifest.Timeouts.RunMS)*time.Millisecond)
	defer cancel()
	adapter := ethadapter.Wrap(s.client)
	for _, planStep := range s.manifest.Plan.Steps {
		if runCtx.Err() != nil {
			s.addReason("run_timeout", &planStep.ID, "", sanitizeDetail(runCtx.Err().Error()))
			break
		}
		if requiresRunReference(planStep) && s.runReference == nil {
			stepID := planStep.ID
			s.addReason("evidence_missing", &stepID, "", "run reference was not established")
			break
		}
		logicalParams, err := substitutePlanParams(planStep.Params, s.runReference)
		if err != nil {
			stepID := planStep.ID
			s.addReason("evidence_missing", &stepID, "", err.Error())
			break
		}
		logical := RequestReport{Method: planStep.Method, Params: logicalParams}
		stepCtx, cancelStep := context.WithTimeout(runCtx, time.Duration(s.manifest.Timeouts.StepMS)*time.Millisecond)
		execution := executeStep(stepCtx, s.client, adapter, planStep, logical)
		stepReport, stepReasons := s.evaluateStep(execution, stepCtx, runCtx)
		cancelStep()
		s.steps = append(s.steps, stepReport)
		s.reasons = append(s.reasons, stepReasons...)
		if execution.reference != nil && execution.reference.Status == rpc.Corroborated {
			if planStep.ID == runReferenceStepID {
				ref := execution.reference.Clone()
				s.runReference = &ref
			}
		}
		if runCtx.Err() != nil {
			s.addReason("run_timeout", &planStep.ID, "", sanitizeDetail(runCtx.Err().Error()))
			break
		}
		if planStep.ID == "identity.chain-id" {
			expectedIdentity := execution.identity != nil &&
				execution.identity.Cmp(new(big.Int).SetUint64(s.manifest.ChainID)) == 0
			if stepReport.Decision == nil || stepReport.Decision.Status != "corroborated" || !expectedIdentity {
				break
			}
		}
		if planStep.ID == runReferenceStepID && s.runReference == nil {
			break
		}
	}
}

type stepExecution struct {
	plan        PlanStep
	logical     RequestReport
	decision    *rpc.Decision
	decisionErr error
	prepared    *rpc.Request
	reference   *rpc.ReferenceEvidence
	typed       *TypedReport
	typedErr    error
	typedOK     bool
	identity    *big.Int
}

func executeStep(ctx context.Context, client *ethguard.Client, adapter *ethadapter.Client, planStep PlanStep, logical RequestReport) stepExecution {
	execution := stepExecution{plan: planStep, logical: logical}
	switch planStep.Path {
	case "client_call":
		if planStep.ID == "identity.chain-id" {
			value, decision, err := client.ChainID(ctx)
			execution.decision, execution.decisionErr, execution.identity = &decision, err, value
		} else {
			decision, err := client.Call(ctx, planStep.Method, callParams(planStep, logical.Params)...)
			execution.decision, execution.decisionErr = &decision, err
		}
	case "typed_block":
		hash := common.HexToHash(paramText(logical.Params, 0))
		block, err := adapter.BlockByHash(ctx, hash)
		execution.typed = typedBlockReport(block, err)
		execution.typedErr = err
		execution.typedOK = err == nil && block != nil
	case "typed_receipt":
		hash := common.HexToHash(paramText(logical.Params, 0))
		decision, err := client.Call(ctx, planStep.Method, hash.Hex())
		execution.decision, execution.decisionErr = &decision, err
		if err == nil {
			receipt, decodeErr := ethguard.DecodeReceipt(decision.Value)
			execution.typed = typedReceiptReport(receipt, decodeErr)
			execution.typedErr = decodeErr
		} else {
			execution.typed = typedReceiptReport(nil, err)
			if !isNotFound(err) {
				execution.typed.ErrorClass = classifyFaultError(err)
			}
			execution.typedErr = err
		}
		execution.typedOK = execution.typed != nil && execution.typed.OK
	}
	if execution.decision != nil {
		execution.prepared = execution.decision.Prepared
		execution.reference = execution.decision.Reference
	}
	return execution
}

func typedBlockReport(block *types.Block, err error) *TypedReport {
	if err != nil || block == nil {
		category := classifyTypedError(err)
		if category == "" {
			category = "typed_decode"
		}
		return &TypedReport{Kind: TypedKindBlock, OK: false, ErrorClass: category}
	}
	height := block.NumberU64()
	return &TypedReport{
		Kind:   TypedKindBlock,
		OK:     true,
		Height: &height,
		Hash:   strings.ToLower(block.Hash().Hex()),
	}
}

func typedReceiptReport(receipt *types.Receipt, err error) *TypedReport {
	if err == nil && receipt != nil {
		return &TypedReport{Kind: TypedKindReceiptAbsence, OK: false, ErrorClass: "illegal_state"}
	}
	if err == nil {
		return &TypedReport{Kind: TypedKindReceiptAbsence, OK: false, ErrorClass: "typed_decode"}
	}
	if isNotFound(err) {
		return &TypedReport{Kind: TypedKindReceiptAbsence, OK: true, ErrorClass: ErrorClassNotFound}
	}
	return &TypedReport{Kind: TypedKindReceiptAbsence, OK: false, ErrorClass: classifyTypedError(err)}
}

func (s *runState) collectSourceHealth() {
	if s.client == nil {
		s.sourceHealth = []SourceHealthReport{}
		return
	}
	sources := s.client.Pool().Sources()
	selected := s.selectedIDs()
	if len(sources) != len(selected) {
		s.addReason("denominator_changed", nil, "", "source health basis differs from manifest selection")
	} else {
		for i, source := range sources {
			if source.Name() != selected[i] {
				s.addReason("denominator_changed", nil, "", "source health order differs from manifest selection")
				break
			}
		}
	}
	health := make([]SourceHealthReport, 0, len(sources))
	for _, source := range sources {
		snapshot := source.Health()
		lastError := ""
		if snapshot.LastError != nil {
			lastError = classifyFaultError(snapshot.LastError)
		}
		health = append(health, SourceHealthReport{
			SourceID:             source.Name(),
			URL:                  source.URL(),
			Successes:            snapshot.Successes,
			Failures:             snapshot.Failures,
			RateLimited:          snapshot.RateLimited,
			ProtocolServerErrors: snapshot.ProtocolServerErrors,
			P95MS:                snapshot.LatencyP95.Milliseconds(),
			LastErrorClass:       lastError,
		})
	}
	s.sourceHealth = health
}

func (s *runState) finish() (Report, error) {
	now := s.now()
	if now.Before(s.started) {
		now = s.started
	}
	if s.reasons == nil {
		s.reasons = []StateReason{}
	}
	if s.steps == nil {
		s.steps = []StepReport{}
	}
	if s.sourceHealth == nil {
		s.sourceHealth = []SourceHealthReport{}
	}
	if len(s.steps) != len(s.manifest.Plan.Steps) && len(s.reasons) == 0 {
		s.addReason("evidence_missing", nil, "", "the fixed plan did not complete")
	}
	if s.runReference == nil && len(s.reasons) == 0 {
		s.addReason("evidence_missing", nil, "", "the run reference was not corroborated")
	}
	for _, step := range s.steps {
		if step.Outcome != OutcomeInvariantFailed && step.Outcome != OutcomeProviderInconclusive {
			continue
		}
		if s.hasStepReason(step.ID) {
			continue
		}
		if step.Outcome == OutcomeInvariantFailed {
			s.addReason("code_error", &step.ID, "", "step failed without a classified reason")
		} else {
			s.addReason("provider_error", &step.ID, "", "step was inconclusive without a classified reason")
		}
	}
	report := Report{
		SchemaVersion:  ReportSchemaVersion,
		RunID:          s.manifest.RunID,
		ManifestSHA256: s.manifestSHA256,
		PlanSHA256:     s.manifest.Plan.SHA256,
		StartedAt:      formatReportTime(s.started),
		FinishedAt:     formatReportTime(now),
		DurationMS:     now.Sub(s.started).Milliseconds(),
		State:          stateFromReasons(s.reasons),
		StateReasons:   s.reasons,
		Steps:          s.steps,
		SourceHealth:   s.sourceHealth,
	}
	if s.runReference != nil && s.runReference.Status == rpc.Corroborated && s.runReference.Hash != "" {
		stepID := runReferenceStepID
		report.ReferenceStepID = &stepID
	}
	if err := report.Validate(); err != nil {
		return Report{}, fmt.Errorf("run: report generation: %w", err)
	}
	return report, nil
}

func (s *runState) hasStepReason(stepID string) bool {
	for _, reason := range s.reasons {
		if reason.StepID != nil && *reason.StepID == stepID {
			return true
		}
	}
	return false
}

func stateFromReasons(reasons []StateReason) string {
	state := StatePassed
	for _, reason := range reasons {
		if categoryFailed(reason.Category) {
			return StateFailed
		}
		state = StateInconclusive
	}
	return state
}

func formatReportTime(value time.Time) string {
	return value.UTC().Format("2006-01-02T15:04:05.000Z")
}

func requiresRunReference(planStep PlanStep) bool {
	for _, param := range planStep.Params {
		if containsPlaceholder(param) {
			return true
		}
	}
	return false
}

func containsPlaceholder(value any) bool {
	switch typed := value.(type) {
	case string:
		return typed == "$reference.hash"
	case []any:
		for _, item := range typed {
			if containsPlaceholder(item) {
				return true
			}
		}
	case map[string]any:
		for _, item := range typed {
			if containsPlaceholder(item) {
				return true
			}
		}
	}
	return false
}

func substitutePlanParams(params []any, reference *rpc.ReferenceEvidence) ([]any, error) {
	if params == nil {
		return []any{}, nil
	}
	out := make([]any, len(params))
	for i, param := range params {
		value, err := substituteValue(param, reference)
		if err != nil {
			return nil, err
		}
		out[i] = value
	}
	return out, nil
}

func substituteValue(value any, reference *rpc.ReferenceEvidence) (any, error) {
	switch typed := value.(type) {
	case string:
		if typed == "$reference.hash" {
			if reference == nil || reference.Status != rpc.Corroborated || reference.Hash == "" {
				return nil, fmt.Errorf("run reference hash is unavailable")
			}
			return strings.ToLower(reference.Hash), nil
		}
		return typed, nil
	case []any:
		out := make([]any, len(typed))
		for i, item := range typed {
			replaced, err := substituteValue(item, reference)
			if err != nil {
				return nil, err
			}
			out[i] = replaced
		}
		return out, nil
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, item := range typed {
			replaced, err := substituteValue(item, reference)
			if err != nil {
				return nil, err
			}
			out[key] = replaced
		}
		return out, nil
	default:
		return value, nil
	}
}

// callParams adapts plan literals to the policy's typed request forms without
// changing the logical request recorded in the report.
func callParams(planStep PlanStep, params []any) []any {
	out := append([]any(nil), params...)
	if planStep.Method != methods.MethodFeeHistory || len(out) != 3 {
		return out
	}
	values, ok := out[2].([]any)
	if !ok {
		return out
	}
	percentiles := make([]float64, len(values))
	for i, value := range values {
		switch typed := value.(type) {
		case int:
			percentiles[i] = float64(typed)
		case int64:
			percentiles[i] = float64(typed)
		case float64:
			percentiles[i] = typed
		case json.Number:
			parsed, err := strconv.ParseFloat(string(typed), 64)
			if err != nil {
				return out
			}
			percentiles[i] = parsed
		default:
			return out
		}
	}
	out[2] = percentiles
	return out
}

func paramText(params []any, index int) string {
	if index < 0 || index >= len(params) {
		return ""
	}
	text, _ := params[index].(string)
	return text
}

func (e stepExecution) errorDetail() string {
	if e.typedErr != nil {
		return e.typedErr.Error()
	}
	if e.decisionErr != nil {
		return e.decisionErr.Error()
	}
	return ""
}
