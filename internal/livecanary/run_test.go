package livecanary

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	gethrpc "github.com/ethereum/go-ethereum/rpc"

	"github.com/codermaybe/ethguard"
	"github.com/codermaybe/ethguard/config"
	"github.com/codermaybe/ethguard/pool"
	"github.com/codermaybe/ethguard/rpc"
	"github.com/codermaybe/ethguard/rpctest"
)

type executorFleet struct {
	nodes []*rpctest.Node
	block map[string]any
}

func newExecutorFleet(t *testing.T, count int) *executorFleet {
	t.Helper()
	fleet := &executorFleet{}
	for range count {
		node := rpctest.NewNode(1)
		t.Cleanup(node.Close)
		fleet.nodes = append(fleet.nodes, node)
	}
	return fleet
}

// scriptAgreeingFleet installs one finalized block and a matching full block
// that survives both the header-only pinned read and the typed full-body read.
func (f *executorFleet) scriptAgreeingFleet(t *testing.T, height uint64) {
	t.Helper()
	if len(f.nodes) == 0 {
		t.Fatal("fleet has no nodes")
	}
	blockJSON, hash := fullBlockJSON(t, int64(height))
	f.block = blockJSON
	for _, node := range f.nodes {
		node.Handle("eth_getBlockByNumber", func(rpctest.Call) (any, error) {
			return map[string]any{"number": fmt.Sprintf("0x%x", height), "hash": hash}, nil
		})
		node.Return("eth_getBalance", "0x1")
		node.Handle("eth_getBlockByHash", func(rpctest.Call) (any, error) {
			return blockJSON, nil
		})
		node.Handle("eth_feeHistory", func(rpctest.Call) (any, error) {
			return map[string]any{
				"oldestBlock":   fmt.Sprintf("0x%x", height-1),
				"baseFeePerGas": []string{"0x1", "0x2", "0x3"},
				"gasUsedRatio":  []float64{0.5, 0.6},
			}, nil
		})
		node.Return("eth_getTransactionReceipt", nil)
	}
}

func fullBlockJSON(t *testing.T, number int64) (map[string]any, string) {
	t.Helper()
	head := &types.Header{
		ParentHash:  common.HexToHash("0x0000000000000000000000000000000000000000000000000000000000000001"),
		UncleHash:   types.EmptyUncleHash,
		Root:        common.HexToHash("0x0000000000000000000000000000000000000000000000000000000000000002"),
		TxHash:      types.EmptyTxsHash,
		ReceiptHash: common.HexToHash("0x0000000000000000000000000000000000000000000000000000000000000003"),
		Difficulty:  new(big.Int),
		Number:      big.NewInt(number),
		GasLimit:    30_000_000,
		Time:        5,
	}
	block := types.NewBlockWithHeader(head)
	raw, err := head.MarshalJSON()
	if err != nil {
		t.Fatalf("marshal header: %v", err)
	}
	var object map[string]any
	if err := json.Unmarshal(raw, &object); err != nil {
		t.Fatalf("decode header: %v", err)
	}
	hash := block.Hash().Hex()
	object["hash"] = hash
	object["transactions"] = []any{}
	object["uncles"] = []any{}
	return object, hash
}

func testManifest(t *testing.T) Manifest {
	t.Helper()
	selected := []SelectedMember{
		{SourceID: "node-01", URL: "https://rpc-a.example"},
		{SourceID: "node-02", URL: "https://rpc-b.example"},
		{SourceID: "node-03", URL: "https://rpc-c.example"},
	}
	digest := "1111111111111111111111111111111111111111111111111111111111111111"
	plan := FixedPlan()
	return Manifest{
		SchemaVersion: SchemaVersion,
		RunID:         "01JAZ3K4M5N6P7Q8R9S0T1V2WX",
		GeneratedAt:   "2026-10-23T12:00:00Z",
		SourceCommit:  "0123456789abcdef0123456789abcdef01234567",
		ToolVersion:   ToolVersion,
		GoVersion:     "go1.25.3",
		Environment: Environment{
			Kind:        "local",
			RunnerClass: "local",
		},
		ChainID: ChainID,
		Registry: Registry{
			Status:         "ok",
			URL:            ChainlistURL,
			FetchedAt:      "2026-10-23T12:00:00Z",
			RawSHA256:      stringPointer(digest),
			Artifact:       stringPointer(ArtifactName),
			ArtifactSHA256: stringPointer(digest),
			Redaction:      RedactionNone,
		},
		Selection: &Selection{
			Algorithm:         SelectionAlgorithm,
			MinMembers:        MinMembers,
			MaxMembers:        MaxMembers,
			CandidateCount:    len(selected),
			Selected:          selected,
			Filtered:          []FilteredEntry{},
			VotingBasisSHA256: VotingBasisSHA256(selected),
		},
		Reference: Reference{Policy: ReferencePolicy, MaxLagBlocks: MaxLagBlocks},
		Timeouts: Timeouts{
			RunMS:       RunTimeoutMS,
			RegistryMS:  RegistryTimeoutMS,
			ReferenceMS: ReferenceTimeoutMS,
			StepMS:      StepTimeoutMS,
		},
		Limits: Limits{RegistryMaxBytes: RegistryMaxBytes},
		Plan:   plan,
	}
}

func testExecutor(t *testing.T, nodes []*rpctest.Node, chainID uint64) Executor {
	t.Helper()
	at := time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)
	return Executor{
		Now: func() time.Time { return at },
		NewClient: func(manifest Manifest) (*ethguard.Client, error) {
			upstreams := make([]config.Upstream, 0, len(nodes))
			for i, node := range nodes {
				upstreams = append(upstreams, config.Upstream{
					ID:       fmt.Sprintf("node-%02d", i+1),
					Endpoint: node.URL(),
				})
			}
			return ethguard.New(
				config.Config{
					ChainID:   chainID,
					Upstreams: upstreams,
					Anchoring: &config.AnchoringConfig{Preset: "finalized", MaxLag: manifest.Reference.MaxLagBlocks},
				},
				ethguard.WithCollectTimeout(time.Duration(manifest.Timeouts.StepMS)*time.Millisecond),
				ethguard.WithFinalizedAnchoring(ethguard.WithMaxFinalizedLag(manifest.Reference.MaxLagBlocks)),
			)
		},
	}
}

func runFixture(t *testing.T, executor Executor, manifest Manifest, membership MembershipState) Report {
	t.Helper()
	manifestBytes, err := manifest.Marshal()
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	report, err := executor.Run(context.Background(), RunInput{
		Manifest:       manifest,
		ManifestBytes:  manifestBytes,
		ManifestSHA256: ManifestSHA256(manifestBytes),
		Membership:     membership,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	return report
}

func TestExecutorPassedRunBuildsEvidenceReport(t *testing.T) {
	fleet := newExecutorFleet(t, 3)
	fleet.scriptAgreeingFleet(t, 100)
	manifest := testManifest(t)
	report := runFixture(t, testExecutor(t, fleet.nodes, 1), manifest, MembershipFixed)

	if report.State != StatePassed || len(report.StateReasons) != 0 {
		for _, reason := range report.StateReasons {
			if reason.StepID != nil {
				t.Logf("reason=%s step=%s detail=%s", reason.Category, *reason.StepID, reason.Detail)
			}
		}
		for _, step := range report.Steps {
			t.Logf("step=%s outcome=%s invariants=%+v", step.ID, step.Outcome, step.Invariants)
		}
		t.Fatalf("state=%s reasons=%+v, want passed", report.State, report.StateReasons)
	}
	if len(report.Steps) != len(FixedPlan().Steps) {
		t.Fatalf("steps=%d, want %d", len(report.Steps), len(FixedPlan().Steps))
	}
	if report.ReferenceStepID == nil || *report.ReferenceStepID != runReferenceStepID {
		t.Fatalf("reference_step_id=%v", report.ReferenceStepID)
	}
	referenceStep := report.Steps[1]
	if referenceStep.Reference == nil || referenceStep.Reference.Status != "corroborated" {
		t.Fatalf("run reference missing: %+v", referenceStep.Reference)
	}
	runHash := referenceStep.Reference.Hash
	runHeight := referenceStep.Reference.Height
	if referenceStep.PreparedRequest == nil || paramText(referenceStep.PreparedRequest.Params, 1) != runHash {
		t.Fatalf("balance was not pinned to the run reference: %+v", referenceStep.PreparedRequest)
	}
	if paramText(referenceStep.LogicalRequest.Params, 1) != "finalized" {
		t.Fatalf("logical balance request lost its moving tag: %+v", referenceStep.LogicalRequest)
	}

	pinned := report.Steps[2]
	if pinned.Decision == nil || pinned.Decision.Status != "corroborated" || pinned.Decision.ValueKind != ValueKindObject {
		t.Fatalf("pinned block decision: %+v", pinned.Decision)
	}
	if pinned.PreparedRequest == nil || paramText(pinned.PreparedRequest.Params, 0) != runHash {
		t.Fatalf("pinned block used %v, want %s", pinned.PreparedRequest.Params, runHash)
	}

	fee := report.Steps[3]
	if fee.Reference == nil || fee.Reference.Status != "corroborated" {
		t.Fatalf("feeHistory step reference missing: %+v", fee.Reference)
	}
	wantNewest := fmt.Sprintf("0x%x", fee.Reference.Height)
	if fee.PreparedRequest == nil {
		t.Fatalf("feeHistory prepared request missing")
	}
	if got := paramText(fee.PreparedRequest.Params, 1); got != wantNewest {
		t.Fatalf("feeHistory newest=%q, want %q", got, wantNewest)
	}
	if paramText(fee.LogicalRequest.Params, 1) != "finalized" {
		t.Fatalf("feeHistory logical request lost its moving tag: %+v", fee.LogicalRequest)
	}

	typedBlock := report.Steps[4]
	if typedBlock.Decision != nil || typedBlock.Typed == nil || !typedBlock.Typed.OK {
		t.Fatalf("typed block evidence: %+v", typedBlock)
	}
	if typedBlock.Typed.Height == nil || *typedBlock.Typed.Height != runHeight ||
		typedBlock.Typed.Hash != runHash {
		t.Fatalf("typed block %+v does not match run reference %d/%s", typedBlock.Typed, runHeight, runHash)
	}

	negative := report.Steps[5]
	if negative.Decision == nil || negative.Decision.ValueKind != ValueKindNull {
		t.Fatalf("negative decision: %+v", negative.Decision)
	}
	if negative.Typed == nil || !negative.Typed.OK || negative.Typed.ErrorClass != ErrorClassNotFound {
		t.Fatalf("negative typed result: %+v", negative.Typed)
	}
	if negative.Invariants.ErrorStatusExclusive == nil || !*negative.Invariants.ErrorStatusExclusive {
		t.Fatalf("expected not_found must not violate error_status_exclusive")
	}
	if negative.Invariants.ShapeMatchesPlan == nil || !*negative.Invariants.ShapeMatchesPlan {
		t.Fatalf("expected null shape was not checked")
	}

	for _, step := range report.Steps {
		if step.Outcome != OutcomeOK {
			t.Fatalf("step %s outcome=%s, want ok", step.ID, step.Outcome)
		}
	}
	if len(report.SourceHealth) != 3 {
		t.Fatalf("source_health=%d, want 3", len(report.SourceHealth))
	}
	for i, health := range report.SourceHealth {
		if health.SourceID != fmt.Sprintf("node-%02d", i+1) {
			t.Fatalf("source_health[%d].source_id=%q", i, health.SourceID)
		}
	}
	encoded, err := report.Marshal()
	if err != nil {
		t.Fatalf("marshal report: %v", err)
	}
	if _, err := ParseReport(encoded); err != nil {
		t.Fatalf("generated report does not parse: %v", err)
	}
	schema := loadReportSchema(t)
	decoded, err := decodeJSON(encoded)
	if err != nil {
		t.Fatalf("decode generated report: %v", err)
	}
	if err := schema.validate(decoded); err != nil {
		t.Fatalf("schema rejected generated report: %v", err)
	}
}

func TestExecutorMinorityRejectDegradesButPasses(t *testing.T) {
	fleet := newExecutorFleet(t, 3)
	fleet.scriptAgreeingFleet(t, 100)
	fleet.nodes[2].Return("eth_getBalance", "0x2")
	report := runFixture(t, testExecutor(t, fleet.nodes, 1), testManifest(t), MembershipFixed)

	if report.State != StatePassed || len(report.StateReasons) != 0 {
		for _, reason := range report.StateReasons {
			if reason.StepID != nil {
				t.Logf("reason=%s step=%s detail=%s", reason.Category, *reason.StepID, reason.Detail)
			}
		}
		for _, step := range report.Steps {
			t.Logf("step=%s outcome=%s invariants=%+v", step.ID, step.Outcome, step.Invariants)
		}
		t.Fatalf("state=%s reasons=%+v, want passed", report.State, report.StateReasons)
	}
	balance := report.Steps[1]
	if balance.Outcome != OutcomeDegraded {
		t.Fatalf("balance outcome=%s, want degraded", balance.Outcome)
	}
	if balance.Decision == nil || len(balance.Decision.Reject) != 1 || balance.Decision.Reject[0] != "node-03" {
		t.Fatalf("minority reject not preserved: %+v", balance.Decision)
	}
	if balance.Decision.Representative != balance.Decision.Support[0] {
		t.Fatalf("representative is not the first supporter")
	}
}

func TestExecutorAvailabilityFaultMakesRunInconclusive(t *testing.T) {
	fleet := newExecutorFleet(t, 3)
	fleet.scriptAgreeingFleet(t, 100)
	fleet.nodes[0].FailHTTP("eth_getBalance", 429)
	report := runFixture(t, testExecutor(t, fleet.nodes, 1), testManifest(t), MembershipFixed)

	if report.State != StateInconclusive {
		t.Fatalf("state=%s, want inconclusive", report.State)
	}
	if !hasReason(report.StateReasons, "provider_rate_limited", "node-01") {
		t.Fatalf("missing source-attributed rate-limit reason: %+v", report.StateReasons)
	}
	balance := report.Steps[1]
	if balance.Outcome != OutcomeProviderInconclusive {
		t.Fatalf("balance outcome=%s, want provider_inconclusive", balance.Outcome)
	}
	if balance.Decision == nil || len(balance.Decision.Faults) != 1 ||
		balance.Decision.Faults[0].SourceID != "node-01" ||
		balance.Decision.Faults[0].Category != "provider_rate_limited" {
		t.Fatalf("structured source fault not recorded: %+v", balance.Decision)
	}
	if report.SourceHealth[0].RateLimited == 0 {
		t.Fatalf("source health did not record the 429: %+v", report.SourceHealth[0])
	}
	assertReportSchema(t, report)
}

func TestExecutorReferenceFailureStopsBeforeDependentSteps(t *testing.T) {
	fleet := newExecutorFleet(t, 3)
	for i, node := range fleet.nodes {
		height := uint64(100 + i)
		hash := fmt.Sprintf("0x%064x", height)
		node.Handle("eth_getBlockByNumber", func(rpctest.Call) (any, error) {
			return map[string]any{"number": fmt.Sprintf("0x%x", height), "hash": hash}, nil
		})
	}
	report := runFixture(t, testExecutor(t, fleet.nodes, 1), testManifest(t), MembershipFixed)

	if report.State != StateInconclusive {
		t.Fatalf("state=%s, want inconclusive", report.State)
	}
	if report.ReferenceStepID != nil {
		t.Fatalf("run reference must stay null, got %v", *report.ReferenceStepID)
	}
	if len(report.Steps) != 2 {
		t.Fatalf("steps=%d, want identity and reference only", len(report.Steps))
	}
	if !hasReason(report.StateReasons, "reference_not_corroborated", "") {
		t.Fatalf("missing reference_not_corroborated: %+v", report.StateReasons)
	}
	referenceStep := report.Steps[1]
	if referenceStep.Reference == nil || referenceStep.Reference.Status == "corroborated" {
		t.Fatalf("reference evidence: %+v", referenceStep.Reference)
	}
	if referenceStep.Decision != nil {
		t.Fatalf("prepare failure must not present a decision: %+v", referenceStep.Decision)
	}
	if referenceStep.PreparedRequest != nil {
		t.Fatalf("prepare failure observed no prepared request")
	}
	assertReportSchema(t, report)
}

func TestExecutorIdentityMismatchFailsRun(t *testing.T) {
	fleet := newExecutorFleet(t, 3)
	for _, node := range fleet.nodes {
		node.Return("eth_chainId", "0x5")
	}
	manifest := testManifest(t)
	fleet.scriptAgreeingFleet(t, 100)
	report := runFixture(t, testExecutor(t, fleet.nodes, 0), manifest, MembershipFixed)

	if report.State != StateFailed {
		t.Fatalf("state=%s, want failed", report.State)
	}
	if len(report.Steps) != 1 {
		t.Fatalf("steps=%d, identity failure must stop the plan", len(report.Steps))
	}
	if !hasReason(report.StateReasons, "identity_mismatch", "") {
		t.Fatalf("missing identity_mismatch: %+v", report.StateReasons)
	}
	if report.Steps[0].Outcome != OutcomeInvariantFailed {
		t.Fatalf("identity outcome=%s", report.Steps[0].Outcome)
	}
}

func TestExecutorPrePlanInconclusiveWritesNoClientSteps(t *testing.T) {
	for _, test := range []struct {
		name       string
		membership MembershipState
		category   string
	}{
		{"registry unavailable", MembershipRegistryUnavailable, "registry_unavailable"},
		{"insufficient members", MembershipInsufficient, "insufficient_members"},
	} {
		t.Run(test.name, func(t *testing.T) {
			executor := Executor{
				Now: func() time.Time { return time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC) },
				NewClient: func(Manifest) (*ethguard.Client, error) {
					t.Fatal("pre-plan inconclusive must not build a client")
					return nil, nil
				},
			}
			report := runFixture(t, executor, testManifest(t), test.membership)
			if report.State != StateInconclusive {
				t.Fatalf("state=%s", report.State)
			}
			if len(report.Steps) != 0 || len(report.SourceHealth) != 0 {
				t.Fatalf("pre-plan report carried execution evidence: %+v", report)
			}
			if !hasReason(report.StateReasons, test.category, "") {
				t.Fatalf("missing %s: %+v", test.category, report.StateReasons)
			}
		})
	}
}

func TestExecutorCanceledParentBecomesRunTimeout(t *testing.T) {
	manifest := testManifest(t)
	manifestBytes, err := manifest.Marshal()
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	executor := Executor{
		Now: func() time.Time { return time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC) },
		NewClient: func(Manifest) (*ethguard.Client, error) {
			t.Fatal("canceled run must not build a client")
			return nil, nil
		},
	}
	report, err := executor.Run(ctx, RunInput{
		Manifest:       manifest,
		ManifestBytes:  manifestBytes,
		ManifestSHA256: ManifestSHA256(manifestBytes),
		Membership:     MembershipFixed,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if report.State != StateInconclusive || !hasReason(report.StateReasons, "run_timeout", "") {
		t.Fatalf("state=%s reasons=%+v", report.State, report.StateReasons)
	}
}

func TestExecutorPanicBecomesFailedReport(t *testing.T) {
	manifest := testManifest(t)
	manifestBytes, err := manifest.Marshal()
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	executor := Executor{NewClient: func(Manifest) (*ethguard.Client, error) {
		panic("synthetic client panic")
	}}
	report, runErr := executor.Run(context.Background(), RunInput{
		Manifest:       manifest,
		ManifestBytes:  manifestBytes,
		ManifestSHA256: ManifestSHA256(manifestBytes),
		Membership:     MembershipFixed,
	})
	if runErr != nil {
		t.Fatalf("Run: %v", runErr)
	}
	if report.State != StateFailed || !hasReason(report.StateReasons, "code_error", "") {
		t.Fatalf("state=%s reasons=%+v", report.State, report.StateReasons)
	}
	if len(report.Steps) != 0 {
		t.Fatalf("panic before client construction must not report steps")
	}
}

func TestExecutorManifestDigestMismatchIsFailedReport(t *testing.T) {
	manifest := testManifest(t)
	manifestBytes, err := manifest.Marshal()
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	inputBytes := append(append([]byte(nil), manifestBytes...), '\n')
	claimed := ManifestSHA256(manifestBytes)
	actual := ManifestSHA256(inputBytes)
	if claimed == actual {
		t.Fatal("test setup: claimed and actual digests must differ")
	}
	executor := Executor{NewClient: func(Manifest) (*ethguard.Client, error) {
		t.Fatal("digest mismatch must not build a client")
		return nil, nil
	}}
	report, err := executor.Run(context.Background(), RunInput{
		Manifest:       manifest,
		ManifestBytes:  inputBytes,
		ManifestSHA256: claimed,
		Membership:     MembershipFixed,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if report.State != StateFailed || !hasReason(report.StateReasons, "schema_error", "") {
		t.Fatalf("state=%s reasons=%+v", report.State, report.StateReasons)
	}
	if report.ManifestSHA256 != actual {
		t.Fatalf("report manifest_sha256 = %s, want the actual manifest bytes digest %s",
			report.ManifestSHA256, actual)
	}
	if len(report.Steps) != 0 {
		t.Fatalf("digest failure must not execute the plan")
	}
}

func TestExecutorManifestArgumentMismatchIsFailedReport(t *testing.T) {
	manifest := testManifest(t)
	manifestBytes, err := manifest.Marshal()
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	digest := ManifestSHA256(manifestBytes)
	disagreeing := testManifest(t)
	disagreeing.RunID = "01JAZ3K4M5N6P7Q8R9S0T1V2WY"
	executor := Executor{NewClient: func(Manifest) (*ethguard.Client, error) {
		t.Fatal("manifest mismatch must not build a client")
		return nil, nil
	}}
	report, err := executor.Run(context.Background(), RunInput{
		Manifest:       disagreeing,
		ManifestBytes:  manifestBytes,
		ManifestSHA256: digest,
		Membership:     MembershipFixed,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if report.State != StateFailed || !hasReason(report.StateReasons, "schema_error", "") {
		t.Fatalf("state=%s reasons=%+v", report.State, report.StateReasons)
	}
	if report.ManifestSHA256 != digest {
		t.Fatalf("report manifest_sha256 = %s, want %s", report.ManifestSHA256, digest)
	}
	if report.RunID != manifest.RunID {
		t.Fatalf("report run_id = %s, want the manifest bytes run_id %s", report.RunID, manifest.RunID)
	}
	if len(report.Steps) != 0 {
		t.Fatalf("manifest mismatch must not execute the plan")
	}
}

func assertReportSchema(t *testing.T, report Report) {
	t.Helper()
	encoded, err := report.Marshal()
	if err != nil {
		t.Fatalf("marshal report: %v", err)
	}
	decoded, err := decodeJSON(encoded)
	if err != nil {
		t.Fatalf("decode report: %v", err)
	}
	if err := loadReportSchema(t).validate(decoded); err != nil {
		t.Fatalf("schema rejected report: %v", err)
	}
}

func hasReason(reasons []StateReason, category, sourceID string) bool {
	for _, reason := range reasons {
		if reason.Category == category && reason.SourceID == sourceID {
			return true
		}
	}
	return false
}

func TestExecutorTypedBlockDriftFailsButKeepsLaterSteps(t *testing.T) {
	fleet := newExecutorFleet(t, 3)
	fleet.scriptAgreeingFleet(t, 100)
	alternate, _ := fullBlockJSON(t, 101)
	for _, node := range fleet.nodes {
		node.Handle("eth_getBlockByHash", func(call rpctest.Call) (any, error) {
			var fullTx bool
			if len(call.Params) > 1 {
				_ = json.Unmarshal(call.Params[1], &fullTx)
			}
			if fullTx {
				return alternate, nil
			}
			// The pinned read still returns the run-reference block.
			return fleetBlock(fleet, t), nil
		})
	}
	report := runFixture(t, testExecutor(t, fleet.nodes, 1), testManifest(t), MembershipFixed)

	if report.State != StateFailed {
		t.Fatalf("state=%s, want failed", report.State)
	}
	if !hasReason(report.StateReasons, "typed_decode", "") {
		t.Fatalf("missing typed_decode: %+v", report.StateReasons)
	}
	if len(report.Steps) != len(FixedPlan().Steps) {
		t.Fatalf("typed failure must not skip later steps: steps=%d", len(report.Steps))
	}
	typed := report.Steps[4]
	if typed.Outcome != OutcomeInvariantFailed || typed.Typed == nil || typed.Typed.OK {
		t.Fatalf("typed step: %+v", typed)
	}
	if typed.Typed.ErrorClass != "typed_decode" {
		t.Fatalf("typed error_class=%s", typed.Typed.ErrorClass)
	}
	if typed.Invariants.TypedDecode == nil || *typed.Invariants.TypedDecode {
		t.Fatalf("typed drift was not recorded")
	}
	negative := report.Steps[5]
	if negative.Decision == nil || negative.Typed == nil || !negative.Typed.OK {
		t.Fatalf("negative step did not run after typed drift: %+v", negative)
	}
}

// fleetBlock recomputes the agreeing fleet's pinned block fixture.
func fleetBlock(fleet *executorFleet, t *testing.T) map[string]any {
	t.Helper()
	if fleet.block == nil {
		t.Fatal("fleet has no agreeing block fixture")
	}
	return fleet.block
}

func TestStatePrecedence(t *testing.T) {
	if got := stateFromReasons(nil); got != StatePassed {
		t.Fatalf("empty reasons state=%s", got)
	}
	if got := stateFromReasons([]StateReason{{Category: "provider_timeout"}}); got != StateInconclusive {
		t.Fatalf("inconclusive state=%s", got)
	}
	if got := stateFromReasons([]StateReason{{Category: "provider_timeout"}, {Category: "code_error"}}); got != StateFailed {
		t.Fatalf("mixed reasons state=%s", got)
	}
}

func TestGatedMemberMakesStepInconclusive(t *testing.T) {
	manifest := testManifest(t)
	state := &runState{manifest: manifest}
	source := func(i int) rpc.SourceRef {
		return rpc.SourceRef{
			Name: fmt.Sprintf("node-%02d", i),
			URL:  manifest.Selection.Selected[i-1].URL,
			ID:   manifest.Selection.Selected[i-1].URL,
		}
	}
	value := json.RawMessage(`"0x1"`)
	decision := rpc.Decision{
		Status:         rpc.Corroborated,
		Value:          value,
		Participants:   []rpc.SourceRef{source(1), source(2), source(3)},
		Support:        []rpc.SourceRef{source(2), source(3)},
		Reject:         []rpc.SourceRef{source(1)},
		Representative: source(2),
		SourceFaults: []rpc.SourceFault{{
			Source: source(1),
			Err:    pool.ErrSourceGated,
		}},
	}
	execution := stepExecution{
		plan:     FixedPlan().Steps[0],
		logical:  RequestReport{Method: "eth_chainId", Params: []any{}},
		decision: &decision,
		prepared: &rpc.Request{Method: "eth_chainId", Params: []any{}},
		identity: big.NewInt(1),
	}
	step, reasons := state.evaluateStep(execution, context.Background(), context.Background())
	if step.Outcome != OutcomeProviderInconclusive {
		t.Fatalf("outcome=%s, want provider_inconclusive", step.Outcome)
	}
	if !hasReason(reasons, "provider_unavailable", "node-01") {
		t.Fatalf("gated member was not classified: %+v", reasons)
	}
	if step.Decision == nil || len(step.Decision.Faults) != 1 ||
		step.Decision.Faults[0].SourceID != "node-01" ||
		step.Decision.Faults[0].Category != "provider_unavailable" {
		t.Fatalf("gated member is missing from report faults: %+v", step.Decision)
	}
}

func TestFaultClassificationVocabulary(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{"gated member", pool.ErrSourceGated, "provider_unavailable"},
		{"rate limit", gethrpc.HTTPError{StatusCode: 429}, "provider_rate_limited"},
		{"server error", gethrpc.HTTPError{StatusCode: 503}, "provider_unavailable"},
		{"access denied", gethrpc.HTTPError{StatusCode: 403}, "provider_unavailable"},
		{"bad request", gethrpc.HTTPError{StatusCode: 400}, "provider_error"},
		{"conflict", rpc.ErrConflict, "provider_divergence"},
		{"no usable response", rpc.ErrNoValidResponse, "quorum_unmet"},
		{"malformed", rpc.ErrMalformedResponse, "provider_malformed"},
		{"deadline", context.DeadlineExceeded, "provider_timeout"},
		{"invalid request", rpc.ErrInvalidRequest, "code_error"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := classifyFaultError(test.err); got != test.want {
				t.Fatalf("category=%s, want %s", got, test.want)
			}
		})
	}
}
