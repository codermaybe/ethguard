package livecanary

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"math/big"
	"net"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	ethereum "github.com/ethereum/go-ethereum"

	"github.com/codermaybe/ethguard/pool"
	"github.com/codermaybe/ethguard/rpc"
)

var movingTagSet = map[string]bool{
	"latest":    true,
	"safe":      true,
	"finalized": true,
	"pending":   true,
	"earliest":  true,
}

var quantityMethods = map[string]bool{
	"eth_chainId":                          true,
	"eth_getBalance":                       true,
	"eth_getTransactionCount":              true,
	"eth_getBlockTransactionCountByHash":   true,
	"eth_getBlockTransactionCountByNumber": true,
	"eth_getUncleCountByBlockHash":         true,
	"eth_getUncleCountByBlockNumber":       true,
	"eth_gasPrice":                         true,
	"eth_maxPriorityFeePerGas":             true,
	"eth_blobBaseFee":                      true,
	"eth_estimateGas":                      true,
	"eth_blockNumber":                      true,
}

var dataMethods = map[string]bool{
	"eth_getCode":      true,
	"eth_call":         true,
	"eth_getStorageAt": true,
}

func (s *runState) evaluateStep(execution stepExecution, stepCtx, runCtx context.Context) (StepReport, []StateReason) {
	stepID := execution.plan.ID
	var reasons []StateReason
	add := func(category, sourceID, detail string) {
		if category == "" {
			return
		}
		detail = sanitizeDetail(detail)
		if detail == "" {
			detail = category
		}
		for _, existing := range reasons {
			if existing.Category == category && existing.SourceID == sourceID {
				return
			}
		}
		id := stepID
		reasons = append(reasons, StateReason{Category: category, StepID: &id, SourceID: sourceID, Detail: detail})
	}
	callCategory := s.stepCategory(execution, stepCtx, runCtx)
	if callCategory != "" {
		add(callCategory, "", execution.errorDetail())
	}

	// Source-attributed faults are checked independently of the decision
	// status: an availability fault makes the run inconclusive even when the
	// expected value was still reached.
	for _, fault := range sourceFaultsOf(execution) {
		category := classifyFaultForStep(fault.Err, execution.plan)
		if category == "" {
			continue
		}
		if isAvailabilityCategory(category) || categoryFailed(category) || category == "identity_basis_invalid" {
			add(category, fault.Source.Name, fault.Err.Error())
		}
	}
	for _, err := range collectionFaultsOf(execution) {
		category := classifyFaultError(err)
		if category == "" {
			continue
		}
		if isAvailabilityCategory(category) || categoryFailed(category) {
			add(category, "", err.Error())
		}
	}

	invariants := s.computeInvariants(execution, callCategory)
	if invariants.RequestBinding != nil && !*invariants.RequestBinding {
		if execution.prepared == nil && execution.decision != nil && len(execution.decision.Participants) > 0 {
			add("evidence_missing", "", "prepared request was not observed")
		} else {
			add("request_binding", "", "prepared request is not bound to the step reference")
		}
	}
	if invariants.StatusAllowed != nil && !*invariants.StatusAllowed && callCategory == "" {
		add("illegal_state", "", "decision status is not allowed by the plan")
	}
	if invariants.EvidenceConservation != nil && !*invariants.EvidenceConservation {
		add("evidence_missing", "", "support, reject, and source faults do not partition participants")
	}
	if invariants.RepresentativeInSupport != nil && !*invariants.RepresentativeInSupport && callCategory == "" {
		add("illegal_state", "", "representative is not the first supporter")
	}
	if invariants.ReferenceStatus != nil && !*invariants.ReferenceStatus && callCategory == "" {
		add("reference_not_corroborated", "", "step reference is not corroborated")
	}
	if invariants.ErrorStatusExclusive != nil && !*invariants.ErrorStatusExclusive {
		add("illegal_state", "", "decision status contradicts the returned error")
	}
	if invariants.BasisUnchanged != nil && !*invariants.BasisUnchanged {
		add("denominator_changed", "", "participants do not match the manifest voting basis")
	}
	if invariants.TypedDecode != nil && !*invariants.TypedDecode && callCategory == "" {
		add("typed_decode", "", "typed result does not match the plan or run reference")
	}

	decision := decisionReport(execution.decision, execution.plan, callCategory, execution.errorDetail())
	if decision != nil && decision.Status != "unknown" && decision.ValueSHA256 == "" {
		add("evidence_missing", "", "successful decision carried no value")
		decision.Status = "unknown"
		decision.ErrorClass = "evidence_missing"
		decision.ErrorDetail = "successful decision carried no value"
	}
	reference := referenceReport(execution)
	step := StepReport{
		ID:              execution.plan.ID,
		Phase:           execution.plan.Phase,
		Path:            execution.plan.Path,
		Method:          execution.plan.Method,
		LogicalRequest:  execution.logical,
		PreparedRequest: requestReport(execution.prepared),
		Reference:       reference,
		Decision:        decision,
		Typed:           execution.typed,
		Invariants:      invariants,
	}
	step.Outcome = stepOutcome(step, reasons)
	return step, reasons
}

func (s *runState) stepCategory(execution stepExecution, stepCtx, runCtx context.Context) string {
	if execution.decisionErr != nil {
		if runCtx.Err() != nil {
			return "run_timeout"
		}
		if stepCtx.Err() == context.DeadlineExceeded {
			return "provider_timeout"
		}
		return s.classifyCallError(execution)
	}
	if execution.typedErr != nil {
		if execution.plan.Expect.Error == "not_found" && execution.typedOK && isNotFound(execution.typedErr) {
			return ""
		}
		if runCtx.Err() != nil {
			return "run_timeout"
		}
		if stepCtx.Err() == context.DeadlineExceeded {
			return "provider_timeout"
		}
		return classifyTypedError(execution.typedErr)
	}
	if execution.plan.ID == "identity.chain-id" {
		if execution.identity == nil {
			return "identity_basis_invalid"
		}
		if execution.identity.Cmp(new(big.Int).SetUint64(s.manifest.ChainID)) != 0 {
			return "identity_mismatch"
		}
	}
	if execution.decision != nil {
		status := execution.decision.Status.String()
		if !stringIn(execution.plan.Expect.Statuses, status) {
			return "illegal_state"
		}
	}
	if execution.plan.Expect.Reference == "corroborated" {
		if execution.reference == nil || execution.reference.Status != rpc.Corroborated {
			if category := availabilityFromEvidence(execution.reference); category != "" {
				return category
			}
			return "reference_not_corroborated"
		}
	}
	if execution.plan.Path == "typed_block" && execution.typedOK {
		if s.runReference == nil || execution.typed == nil || execution.typed.Height == nil ||
			*execution.typed.Height != s.runReference.Height ||
			!strings.EqualFold(execution.typed.Hash, s.runReference.Hash) {
			return "typed_decode"
		}
	}
	if execution.plan.Path == "typed_receipt" && execution.typed != nil &&
		!execution.typed.OK && execution.typed.ErrorClass == "illegal_state" {
		return "illegal_state"
	}
	return ""
}

func (s *runState) classifyCallError(execution stepExecution) string {
	err := execution.decisionErr
	if err == nil {
		return ""
	}
	if errors.Is(err, pool.ErrChainIDMismatch) && execution.plan.ID == "identity.chain-id" {
		return "identity_basis_invalid"
	}
	category := classifyFaultError(err)
	if execution.plan.ID == "identity.chain-id" &&
		!isAvailabilityCategory(category) && category != "code_error" && category != "provider_malformed" {
		return "identity_basis_invalid"
	}
	if execution.plan.Expect.Reference == "corroborated" {
		if execution.reference == nil || execution.reference.Status != rpc.Corroborated {
			if availability := availabilityFromEvidence(execution.reference); availability != "" {
				return availability
			}
			return "reference_not_corroborated"
		}
	}
	return category
}

func (s *runState) computeInvariants(execution stepExecution, callCategory string) InvariantReport {
	invariants := InvariantReport{
		RequestBinding:          s.requestBinding(execution),
		StatusAllowed:           statusAllowed(execution),
		EvidenceConservation:    s.evidenceConservation(execution),
		RepresentativeInSupport: representativeInSupport(execution.decision),
		ReferenceStatus:         s.referenceStatus(execution),
		ErrorStatusExclusive:    errorStatusExclusive(execution),
		BasisUnchanged:          s.basisUnchanged(execution),
		TypedDecode:             s.typedDecode(execution),
		ShapeMatchesPlan:        shapeMatchesPlan(execution),
	}
	if invariants.RequestBinding != nil && !*invariants.RequestBinding && callCategory != "" &&
		callCategory != "request_binding" && execution.prepared == nil {
		// A prepare failure never observed a request; binding is not
		// applicable rather than violated.
		invariants.RequestBinding = nil
	}
	return invariants
}

func (s *runState) requestBinding(execution stepExecution) *bool {
	if execution.plan.Path != "client_call" {
		return nil
	}
	prepared := execution.prepared
	if prepared == nil {
		if execution.decision != nil && len(execution.decision.Participants) > 0 {
			return boolPointer(false)
		}
		return nil
	}
	if prepared.Method != execution.logical.Method {
		return boolPointer(false)
	}
	switch execution.plan.Expect.Binding {
	case "none":
		return boolPointer(paramsEqual(prepared.Params, execution.logical.Params))
	case "reference_hash":
		ref := execution.reference
		if ref == nil || ref.Status != rpc.Corroborated || ref.Hash == "" {
			return boolPointer(false)
		}
		return boolPointer(boundParams(prepared.Params, execution.logical.Params, strings.ToLower(ref.Hash), nil))
	case "reference_height":
		ref := execution.reference
		if ref == nil || ref.Status != rpc.Corroborated || ref.Height == 0 {
			return boolPointer(false)
		}
		height := "0x" + strconv.FormatUint(ref.Height, 16)
		return boolPointer(boundParams(prepared.Params, execution.logical.Params, "", &height))
	default:
		return boolPointer(false)
	}
}

func boundParams(prepared, logical []any, hash string, height *string) bool {
	if len(prepared) != len(logical) {
		return false
	}
	replaced := 0
	for i := range logical {
		tag, ok := logical[i].(string)
		if ok && movingTagSet[strings.ToLower(tag)] {
			expected := hash
			if height != nil {
				expected = *height
			}
			value, ok := prepared[i].(string)
			if !ok || value != expected {
				return false
			}
			replaced++
			continue
		}
		if !jsonValuesEqual(prepared[i], logical[i]) {
			return false
		}
	}
	return replaced > 0 && !containsMovingTag(prepared)
}

// containsMovingTag scans nested params so a moving tag hidden in an object or
// array cannot pass the prepared-request binding check.
func containsMovingTag(value any) bool {
	switch typed := value.(type) {
	case string:
		return movingTagSet[strings.ToLower(typed)]
	case []any:
		for _, item := range typed {
			if containsMovingTag(item) {
				return true
			}
		}
	case map[string]any:
		for _, item := range typed {
			if containsMovingTag(item) {
				return true
			}
		}
	}
	return false
}

func paramsEqual(a, b []any) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !jsonValuesEqual(a[i], b[i]) {
			return false
		}
	}
	return true
}

func jsonValuesEqual(a, b any) bool {
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

func statusAllowed(execution stepExecution) *bool {
	if execution.plan.Expect.Decision != "required" {
		return nil
	}
	if execution.decision == nil {
		return nil
	}
	if execution.decisionErr != nil && !(execution.plan.Expect.Error == "not_found" && execution.typedOK) {
		return boolPointer(false)
	}
	status := execution.decision.Status.String()
	if status == "unknown" {
		return boolPointer(false)
	}
	allowed := stringIn(execution.plan.Expect.Statuses, status)
	if allowed && execution.decision.Status == rpc.Corroborated &&
		len(execution.decision.Support) < requiredSupport(len(execution.decision.Participants)) {
		// A corroborated status below the fixed quorum means the production
		// support threshold changed or the decision is corrupt.
		return boolPointer(false)
	}
	return boolPointer(allowed)
}

// requiredSupport mirrors the production acceptance threshold for the fixed
// canary basis: max(floor=2, strict majority).
func requiredSupport(basis int) int {
	if basis <= 0 {
		return 1
	}
	required := basis/2 + 1
	if required < 2 {
		required = 2
	}
	return required
}

func (s *runState) evidenceConservation(execution stepExecution) *bool {
	if execution.plan.Path == "typed_block" {
		return nil
	}
	decision := execution.decision
	if decision == nil {
		return nil
	}
	if len(decision.Participants) == 0 {
		return nil
	}
	if !s.participantOrderMatches(decision.Participants) {
		return boolPointer(false)
	}
	return boolPointer(dispositionsPartition(decision.Participants, decision.Support, decision.Reject, decision.SourceFaults))
}

func (s *runState) referenceStatus(execution stepExecution) *bool {
	if execution.plan.Expect.Reference != "corroborated" {
		return nil
	}
	ref := execution.reference
	if ref == nil {
		return boolPointer(false)
	}
	if ref.Status != rpc.Corroborated || ref.Hash == "" || ref.Height == 0 {
		return boolPointer(false)
	}
	if len(ref.Support) < requiredSupport(len(ref.Participants)) {
		return boolPointer(false)
	}
	if strings.ToLower(ref.Tag) != requestedTag(execution.logical.Params) {
		return boolPointer(false)
	}
	if !s.participantOrderMatches(ref.Participants) {
		return boolPointer(false)
	}
	if !dispositionsPartition(ref.Participants, ref.Support, ref.Reject, ref.SourceFaults) {
		return boolPointer(false)
	}
	return boolPointer(true)
}

func representativeInSupport(decision *rpc.Decision) *bool {
	if decision == nil {
		return nil
	}
	if len(decision.Support) == 0 {
		if decision.Status == rpc.Unknown {
			return nil
		}
		return boolPointer(false)
	}
	return boolPointer(decision.Representative.Name != "" && decision.Representative.Name == decision.Support[0].Name)
}

func errorStatusExclusive(execution stepExecution) *bool {
	if execution.plan.Path == "typed_block" || execution.decision == nil {
		return nil
	}
	if execution.plan.Expect.Error == "not_found" && execution.typedOK {
		return boolPointer(true)
	}
	errored := execution.decisionErr != nil
	return boolPointer(errored == (execution.decision.Status == rpc.Unknown))
}

func (s *runState) basisUnchanged(execution stepExecution) *bool {
	if execution.decision != nil && len(execution.decision.Participants) > 0 &&
		!s.participantOrderMatches(execution.decision.Participants) {
		return boolPointer(false)
	}
	if execution.reference != nil && len(execution.reference.Participants) > 0 &&
		!s.participantOrderMatches(execution.reference.Participants) {
		return boolPointer(false)
	}
	if len(s.sourceHealth) > 0 {
		selected := s.selectedIDs()
		if len(s.sourceHealth) != len(selected) {
			return boolPointer(false)
		}
		for i := range selected {
			if s.sourceHealth[i].SourceID != selected[i] {
				return boolPointer(false)
			}
		}
	}
	return boolPointer(true)
}

func (s *runState) participantOrderMatches(participants []rpc.SourceRef) bool {
	selected := s.selectedIDs()
	if len(participants) != len(selected) {
		return false
	}
	for i, source := range participants {
		if source.Name != selected[i] {
			return false
		}
	}
	return true
}

func (s *runState) typedDecode(execution stepExecution) *bool {
	switch execution.plan.Path {
	case "typed_block":
		if execution.typed == nil || !execution.typed.OK || execution.typed.Height == nil {
			return boolPointer(false)
		}
		if s.runReference == nil {
			return boolPointer(false)
		}
		return boolPointer(*execution.typed.Height == s.runReference.Height &&
			strings.EqualFold(execution.typed.Hash, s.runReference.Hash))
	case "typed_receipt":
		if execution.typed == nil {
			return boolPointer(false)
		}
		return boolPointer(execution.typed.OK && execution.typed.ErrorClass == ErrorClassNotFound)
	default:
		return nil
	}
}

func shapeMatchesPlan(execution stepExecution) *bool {
	if execution.plan.Expect.Decision != "required" {
		return nil
	}
	if execution.decision == nil || execution.decision.Status == rpc.Unknown {
		return nil
	}
	if execution.decisionErr != nil && !(execution.plan.Expect.Error == "not_found" && execution.typedOK) {
		return nil
	}
	kind := classifyValueKind(execution.plan.Method, execution.decision.Value)
	return boolPointer(kind == execution.plan.Expect.ValueKind)
}

func stepOutcome(step StepReport, reasons []StateReason) string {
	failed, inconclusive := false, false
	for _, reason := range reasons {
		if categoryFailed(reason.Category) {
			failed = true
		} else {
			inconclusive = true
		}
	}
	if failed {
		return OutcomeInvariantFailed
	}
	if inconclusive {
		return OutcomeProviderInconclusive
	}
	falseInvariant := false
	nonShapeFalse := false
	for _, value := range []*bool{
		step.Invariants.RequestBinding,
		step.Invariants.StatusAllowed,
		step.Invariants.EvidenceConservation,
		step.Invariants.RepresentativeInSupport,
		step.Invariants.ReferenceStatus,
		step.Invariants.ErrorStatusExclusive,
		step.Invariants.BasisUnchanged,
		step.Invariants.TypedDecode,
	} {
		if value != nil && !*value {
			falseInvariant = true
			nonShapeFalse = true
		}
	}
	if step.Invariants.ShapeMatchesPlan != nil && !*step.Invariants.ShapeMatchesPlan {
		falseInvariant = true
	}
	if nonShapeFalse {
		return OutcomeInvariantFailed
	}
	if falseInvariant || hasDegradingEvidence(step) {
		return OutcomeDegraded
	}
	return OutcomeOK
}

func hasDegradingEvidence(step StepReport) bool {
	if step.Decision != nil && (len(step.Decision.Reject) > 0 || len(step.Decision.Faults) > 0 || len(step.Decision.CollectionFaults) > 0) {
		return true
	}
	if step.Reference != nil && (len(step.Reference.Reject) > 0 || len(step.Reference.Faults) > 0 || len(step.Reference.CollectionFaults) > 0) {
		return true
	}
	return false
}

func decisionReport(decision *rpc.Decision, planStep PlanStep, callCategory, detail string) *DecisionReport {
	if decision == nil {
		return nil
	}
	if len(decision.Participants) == 0 && decision.Status == rpc.Unknown {
		return nil
	}
	participants, support, reject, faults := dispositions(decision.Participants, decision.Support, decision.Reject, decision.SourceFaults)
	report := &DecisionReport{
		Status:           decision.Status.String(),
		N:                len(participants),
		Participants:     participants,
		Support:          support,
		Reject:           reject,
		Faults:           faults,
		CollectionFaults: collectionFaultReports(decision.CollectionFaults),
		Representative:   decision.Representative.Name,
	}
	if len(decision.Value) > 0 {
		report.ValueSHA256 = valueSHA256(decision.Value)
		report.ValueKind = classifyValueKind(planStep.Method, decision.Value)
	}
	if report.Status == "unknown" {
		report.ErrorClass = callCategory
		if report.ErrorClass == "" {
			if len(decision.Participants) == 0 {
				report.ErrorClass = "evidence_missing"
			} else {
				report.ErrorClass = "provider_error"
			}
		}
		report.ErrorDetail = sanitizeDetail(detail)
	}
	return report
}

func referenceReport(execution stepExecution) *ReferenceReport {
	if execution.plan.Expect.Reference != "corroborated" || execution.reference == nil {
		return nil
	}
	ref := execution.reference
	participants, support, reject, faults := dispositions(ref.Participants, ref.Support, ref.Reject, ref.SourceFaults)
	report := &ReferenceReport{
		Status:           ref.Status.String(),
		Tag:              ref.Tag,
		Height:           ref.Height,
		Hash:             strings.ToLower(ref.Hash),
		Origin:           ref.Origin,
		ObservedAt:       formatReportTime(ref.ObservedAt),
		N:                len(participants),
		Participants:     participants,
		Support:          support,
		Reject:           reject,
		Faults:           faults,
		CollectionFaults: collectionFaultReports(ref.CollectionFaults),
	}
	return report
}

func dispositions(participants, support, reject []rpc.SourceRef, sourceFaults []rpc.SourceFault) ([]string, []string, []string, []SourceFaultReport) {
	ids := make([]string, len(participants))
	inBasis := make(map[string]bool, len(participants))
	for i, source := range participants {
		ids[i] = source.Name
		inBasis[source.Name] = true
	}
	claimed := make(map[string]bool, len(participants))
	supportIDs := make([]string, 0, len(support))
	for _, source := range support {
		if !inBasis[source.Name] || claimed[source.Name] {
			continue
		}
		claimed[source.Name] = true
		supportIDs = append(supportIDs, source.Name)
	}
	faultReports := make([]SourceFaultReport, 0, len(sourceFaults))
	for _, fault := range sourceFaults {
		if !inBasis[fault.Source.Name] || claimed[fault.Source.Name] {
			continue
		}
		claimed[fault.Source.Name] = true
		category := classifyFaultError(fault.Err)
		if category == "" {
			category = "provider_error"
		}
		detail := sanitizeDetail(fault.Err.Error())
		if detail == "" {
			detail = category
		}
		faultReports = append(faultReports, SourceFaultReport{
			SourceID: fault.Source.Name,
			Category: category,
			Detail:   detail,
		})
	}
	rejectIDs := make([]string, 0, len(reject))
	for _, source := range reject {
		if !inBasis[source.Name] || claimed[source.Name] {
			continue
		}
		claimed[source.Name] = true
		rejectIDs = append(rejectIDs, source.Name)
	}
	for _, id := range ids {
		if !claimed[id] {
			claimed[id] = true
			rejectIDs = append(rejectIDs, id)
		}
	}
	return ids, supportIDs, rejectIDs, faultReports
}

func dispositionsPartition(participants, support, reject []rpc.SourceRef, sourceFaults []rpc.SourceFault) bool {
	inBasis := make(map[string]bool, len(participants))
	for _, source := range participants {
		if source.Name == "" || inBasis[source.Name] {
			return false
		}
		inBasis[source.Name] = true
	}
	supported := make(map[string]bool, len(support))
	supportIndex := make(map[string]int, len(participants))
	for i, source := range participants {
		supportIndex[source.Name] = i
	}
	lastSupport := -1
	for _, source := range support {
		if !inBasis[source.Name] || supported[source.Name] {
			return false
		}
		if supportIndex[source.Name] <= lastSupport {
			return false
		}
		lastSupport = supportIndex[source.Name]
		supported[source.Name] = true
	}
	faulted := make(map[string]bool, len(sourceFaults))
	for _, fault := range sourceFaults {
		if !inBasis[fault.Source.Name] || supported[fault.Source.Name] || faulted[fault.Source.Name] {
			return false
		}
		faulted[fault.Source.Name] = true
	}
	rejected := make(map[string]bool, len(reject))
	for _, source := range reject {
		if !inBasis[source.Name] || supported[source.Name] || rejected[source.Name] {
			return false
		}
		rejected[source.Name] = true
	}
	for id := range supported {
		if !faulted[id] && !rejected[id] {
			continue
		}
		return false
	}
	claimed := 0
	for id := range inBasis {
		if supported[id] {
			claimed++
			continue
		}
		if faulted[id] {
			claimed++
			continue
		}
		if rejected[id] {
			claimed++
			continue
		}
		return false
	}
	return claimed == len(participants)
}

func collectionFaultReports(faults []error) []CollectionFaultReport {
	reports := make([]CollectionFaultReport, 0, len(faults))
	for _, err := range faults {
		category := classifyFaultError(err)
		if category == "" {
			category = "provider_error"
		}
		detail := sanitizeDetail(err.Error())
		if detail == "" {
			detail = category
		}
		reports = append(reports, CollectionFaultReport{Category: category, Detail: detail})
	}
	return reports
}

func sourceFaultsOf(execution stepExecution) []rpc.SourceFault {
	var faults []rpc.SourceFault
	if execution.decision != nil {
		faults = append(faults, execution.decision.SourceFaults...)
	}
	if execution.reference != nil {
		faults = append(faults, execution.reference.SourceFaults...)
	}
	return faults
}

func collectionFaultsOf(execution stepExecution) []error {
	var faults []error
	if execution.decision != nil {
		faults = append(faults, execution.decision.CollectionFaults...)
	}
	if execution.reference != nil {
		faults = append(faults, execution.reference.CollectionFaults...)
	}
	return faults
}

func requestReport(request *rpc.Request) *RequestReport {
	if request == nil {
		return nil
	}
	params := request.Params
	if params == nil {
		params = []any{}
	}
	return &RequestReport{Method: request.Method, Params: params}
}

func classifyValueKind(method string, raw json.RawMessage) string {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return ""
	}
	switch trimmed[0] {
	case '{':
		return ValueKindObject
	case '[':
		return ValueKindArray
	case 't', 'f':
		return ValueKindBool
	case 'n':
		if bytes.Equal(trimmed, []byte("null")) {
			return ValueKindNull
		}
	case '"':
		if quantityMethods[method] {
			return ValueKindQuantity
		}
		if dataMethods[method] {
			return ValueKindData
		}
		return ValueKindString
	}
	return ValueKindString
}

func valueSHA256(raw json.RawMessage) string {
	digest, err := canonicalSHA256(raw)
	if err != nil {
		return ""
	}
	return digest
}

func requestedTag(params []any) string {
	for _, param := range params {
		if tag, ok := param.(string); ok && movingTagSet[strings.ToLower(tag)] {
			return strings.ToLower(tag)
		}
	}
	return ""
}

func boolPointer(value bool) *bool { return &value }

func stringIn(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}

func isNotFound(err error) bool {
	return err != nil && errors.Is(err, ethereum.NotFound)
}

func isAvailabilityCategory(category string) bool {
	switch category {
	case "provider_unavailable", "provider_rate_limited", "provider_timeout":
		return true
	default:
		return false
	}
}

func availabilityFromEvidence(ref *rpc.ReferenceEvidence) string {
	if ref == nil {
		return ""
	}
	priority := []string{"provider_rate_limited", "provider_timeout", "provider_unavailable"}
	for _, want := range priority {
		for _, fault := range ref.SourceFaults {
			if classifyFaultError(fault.Err) == want {
				return want
			}
		}
		for _, err := range ref.CollectionFaults {
			if classifyFaultError(err) == want {
				return want
			}
		}
	}
	return ""
}

func classifyFaultForStep(err error, planStep PlanStep) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, pool.ErrChainIDMismatch) {
		if planStep.ID == "identity.chain-id" {
			return "identity_basis_invalid"
		}
		return "provider_unavailable"
	}
	return classifyFaultError(err)
}

func classifyFaultError(err error) string {
	if err == nil {
		return ""
	}
	switch {
	case errors.Is(err, pool.ErrSourceGated),
		errors.Is(err, pool.ErrClosed),
		errors.Is(err, pool.ErrAllSourcesDown),
		errors.Is(err, pool.ErrNoEligibleSource),
		errors.Is(err, pool.ErrChainIDMismatch):
		return "provider_unavailable"
	case isRateLimit(err):
		return "provider_rate_limited"
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled), isTimeout(err):
		return "provider_timeout"
	case errors.Is(err, rpc.ErrMalformedResponse), errors.Is(err, rpc.ErrUnsupportedTypedObject):
		return "provider_malformed"
	case errors.Is(err, rpc.ErrReorgDetected), errors.Is(err, rpc.ErrAnchorMismatch):
		return "provider_divergence"
	case errors.Is(err, rpc.ErrConflict):
		return "provider_divergence"
	case errors.Is(err, rpc.ErrNoValidResponse):
		return "quorum_unmet"
	case errors.Is(err, rpc.ErrInvalidRequest):
		return "code_error"
	}
	var statusErr rpc.HTTPStatusError
	if errors.As(err, &statusErr) {
		// Access-denied and missing-endpoint responses mean the provider is
		// not serving this client; other 4xx responses are protocol errors.
		status := statusErr.HTTPStatusCode()
		if status >= 500 || status == 401 ||
			status == 403 || status == 404 {
			return "provider_unavailable"
		}
		return "provider_error"
	}
	var protocolErr rpc.ProtocolError
	if errors.As(err, &protocolErr) {
		return "provider_error"
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		return "provider_unavailable"
	}
	return "provider_error"
}

func classifyTypedError(err error) string {
	if err == nil {
		return ""
	}
	if isNotFound(err) {
		return "typed_decode"
	}
	category := classifyFaultError(err)
	switch category {
	case "provider_unavailable", "provider_rate_limited", "provider_timeout":
		return category
	case "provider_error":
		var protocolErr rpc.ProtocolError
		if errors.As(err, &protocolErr) {
			return "provider_error"
		}
		return "typed_decode"
	default:
		return "typed_decode"
	}
}

func isRateLimit(err error) bool {
	if err == nil {
		return false
	}
	var statusErr rpc.HTTPStatusError
	if errors.As(err, &statusErr) && statusErr.HTTPStatusCode() == 429 {
		return true
	}
	var rpcErr rpc.ProtocolError
	if errors.As(err, &rpcErr) {
		switch rpcErr.ErrorCode() {
		case -32005, -32016, 429:
			return true
		}
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "rate limit") ||
		strings.Contains(message, "too many requests") ||
		strings.Contains(message, "throttl")
}

func isTimeout(err error) bool {
	var timeout interface{ Timeout() bool }
	if errors.As(err, &timeout) {
		return timeout.Timeout()
	}
	return false
}

var detailURLRE = regexp.MustCompile(`https?://[^\s"']+`)

func sanitizeDetail(text string) string {
	text = detailURLRE.ReplaceAllStringFunc(text, func(raw string) string {
		parsed, err := url.Parse(raw)
		if err != nil {
			return "[url]"
		}
		out := parsed.Scheme + "://" + parsed.Host + parsed.EscapedPath()
		return strings.TrimSuffix(out, "/")
	})
	text = strings.TrimSpace(text)
	const maxDetail = 512
	if len(text) > maxDetail {
		text = strings.ToValidUTF8(text[:maxDetail], "")
	}
	return text
}
