package methods

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/codermaybe/ethguard/rpc"
)

// The call-revert resolver's outcome seam. The resolver states its
// per-source verdicts itself, so the aggregator's planned eth_call path never
// re-runs the generic comparison key as a usability guess. The statement
// classifies the execution dimension — winner supported, losing groups
// dissented, admission failures unusable — while the data dimension keeps its
// abstentions and assertions on the decision's own fields.

// planCallRevert assembles the call-revert resolver exactly as the plan
// compiler does: error bodies in, canonical success encoding.
func planCallRevert() rpc.Policy {
	return rpc.NewCallRevertPolicy(normalizeCallOutcome,
		rpc.WithRPCErrorBodies(), rpc.WithCanonicalValue(encodeCallSuccess))
}

func callOutcomeOutcome(t *testing.T, policy rpc.Policy, obs []rpc.Observation) (rpc.Outcome, error) {
	t.Helper()
	outcome, err := policy.(rpc.OutcomeResolver).ResolveOutcome(obs)
	return outcome, err
}

func callOutcomeOutcomeStated(t *testing.T, policy rpc.Policy, obs []rpc.Observation) rpc.Outcome {
	t.Helper()
	outcome, err := callOutcomeOutcome(t, policy, obs)
	if err != nil {
		t.Fatalf("outcome: %v", err)
	}
	return outcome
}

func invocationSnap() rpc.Corroboration {
	return rpc.Corroboration{Mode: rpc.CorroborationMajority, Floor: 2}
}

// TestCallRevertOutcomeMatchesResolve pins that the outcome statement is the
// same adjudication the plain and corroborated resolutions return: stating
// per-source verdicts must not re-decide the value, status, support, or the
// error the resolution failed with.
func TestCallRevertOutcomeMatchesResolve(t *testing.T) {
	policy := planCallRevert()
	success := []rpc.Observation{
		{Source: rpc.SourceRef{Name: "a", ID: "a"}, Value: json.RawMessage(`"0x64"`)},
		{Source: rpc.SourceRef{Name: "b", ID: "b"}, Value: json.RawMessage(`"0x64"`)},
	}
	reverted := []rpc.Observation{
		rpcErrorObs("a", 3, "execution reverted", `"0xdead"`),
		rpcErrorObs("b", -32000, "execution reverted", `"0xdead"`),
	}
	conflict := []rpc.Observation{
		{Source: rpc.SourceRef{Name: "a", ID: "a"}, Value: json.RawMessage(`"0x64"`)},
		{Source: rpc.SourceRef{Name: "b", ID: "b"}, Value: json.RawMessage(`"0x65"`)},
	}
	snap := invocationSnap()
	for name, obs := range map[string][]rpc.Observation{
		"success": success, "reverted": reverted, "conflict": conflict,
	} {
		t.Run(name, func(t *testing.T) {
			plain, plainErr := policy.Resolve(obs)
			outcome, outcomeErr := callOutcomeOutcome(t, policy, obs)
			if (plainErr == nil) != (outcomeErr == nil) || plainErr != nil && plainErr.Error() != outcomeErr.Error() {
				t.Fatalf("%s error mismatch: resolve %v, outcome %v", name, plainErr, outcomeErr)
			}
			assertSameAdjudication(t, "plain", plain, outcome)

			corroborated, corroboratedErr := policy.(rpc.CorroboratingPolicy).ResolveCorroborated(snap, obs)
			stated, statedErr := policy.(rpc.CorroboratingOutcomeResolver).ResolveOutcomeCorroborated(snap, obs)
			if (corroboratedErr == nil) != (statedErr == nil) || corroboratedErr != nil && corroboratedErr.Error() != statedErr.Error() {
				t.Fatalf("corroborated error mismatch: resolve %v, outcome %v", corroboratedErr, statedErr)
			}
			assertSameAdjudication(t, "corroborated", corroborated, stated)
		})
	}
}

func assertSameAdjudication(t *testing.T, entry string, decision rpc.Decision, outcome rpc.Outcome) {
	t.Helper()
	if !reflect.DeepEqual(outcome.Decision, decision) {
		t.Fatalf("%s outcome decision diverged from resolve:\noutcome     %+v\nresolve     %+v", entry, outcome.Decision, decision)
	}
}

// TestCallRevertOutcomeClassifiesExecutionDimension pins the statement's
// dimension: the winning execution group supported, a losing group dissented,
// and an attempted but unusable source unusable with its own error. Verdicts
// cover every observation, ordered by source ID.
func TestCallRevertOutcomeClassifiesExecutionDimension(t *testing.T) {
	policy := planCallRevert()
	obs := []rpc.Observation{
		{Source: rpc.SourceRef{Name: "c", ID: "c"}, Value: json.RawMessage(`"0x64"`)},
		{Source: rpc.SourceRef{Name: "a", ID: "a"}, Value: json.RawMessage(`"0x64"`)},
		{Source: rpc.SourceRef{Name: "b", ID: "b"}, Value: json.RawMessage(`"0x65"`)},
		{Source: rpc.SourceRef{Name: "d", ID: "d"}, Err: errors.New("connection reset")},
	}
	outcome, _ := callOutcomeOutcome(t, policy, obs)
	want := []rpc.SourceVerdict{
		{Source: rpc.SourceRef{Name: "a", ID: "a"}, Kind: rpc.VerdictSupported},
		{Source: rpc.SourceRef{Name: "b", ID: "b"}, Kind: rpc.VerdictDissented},
		{Source: rpc.SourceRef{Name: "c", ID: "c"}, Kind: rpc.VerdictSupported},
		{Source: rpc.SourceRef{Name: "d", ID: "d"}, Kind: rpc.VerdictUnusable, Err: errors.New("connection reset")},
	}
	if !reflect.DeepEqual(outcome.Verdicts, want) {
		t.Fatalf("verdicts = %+v\nwant   %+v", outcome.Verdicts, want)
	}
}

// TestCallRevertOutcomeClassifiesDimensionOnConflict also holds when the
// execution groups disagree: the winner is still supported and the loser
// dissented, even though the resolution failed as a conflict.
func TestCallRevertOutcomeClassifiesDimensionOnConflict(t *testing.T) {
	policy := planCallRevert()
	obs := []rpc.Observation{
		{Source: rpc.SourceRef{Name: "a", ID: "a"}, Value: json.RawMessage(`"0x64"`)},
		{Source: rpc.SourceRef{Name: "b", ID: "b"}, Value: json.RawMessage(`"0x65"`)},
	}
	_, resolveErr := policy.Resolve(obs)
	if resolveErr == nil {
		t.Fatal("expected an execution conflict")
	}
	outcome, _ := callOutcomeOutcome(t, policy, obs)
	want := []rpc.SourceVerdict{
		{Source: rpc.SourceRef{Name: "a", ID: "a"}, Kind: rpc.VerdictSupported},
		{Source: rpc.SourceRef{Name: "b", ID: "b"}, Kind: rpc.VerdictDissented},
	}
	if !reflect.DeepEqual(outcome.Verdicts, want) {
		t.Fatalf("verdicts = %+v\nwant   %+v", outcome.Verdicts, want)
	}
}

// TestCallRevertOutcomeKeepsDataAbstentionStates pins that the per-source
// statement does not erase the data dimension: a missing-data or null-data
// abstainer stays supported on execution and sits on the data dimension's
// abstain column, while concrete data keepers sit on its support column.
// Abstention is a usability fact about data only — it never demotes an
// execution answer — and missing/null stay distinct non-assertions.
func TestCallRevertOutcomeKeepsDataAbstentionStates(t *testing.T) {
	policy := planCallRevert()
	obs := []rpc.Observation{
		rpcErrorObs("a", 3, "execution reverted", `"0xdead"`),
		rpcErrorObs("b", -32000, "execution reverted", `"0xdead"`),
		rpcErrorObs("c", 3, "execution reverted", ``),
		rpcErrorObs("d", 3, "execution reverted", `null`),
	}
	decision, err := policy.Resolve(obs)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	outcome := callOutcomeOutcomeStated(t, policy, obs)
	for _, verdict := range outcome.Verdicts {
		if verdict.Kind != rpc.VerdictSupported {
			t.Fatalf("verdict for %s = %v, want supported: all four answers were execution-admissible", verdict.Source.ID, verdict.Kind)
		}
	}
	data, ok := callDataDimension(decision)
	if !ok {
		t.Fatalf("no data dimension in %+v", decision.Dimensions)
	}
	if !reflect.DeepEqual(dimensionIDs(data.Support), []string{"a", "b"}) {
		t.Fatalf("data asserters = %v, want a/b only", dimensionIDs(data.Support))
	}
	if !reflect.DeepEqual(dimensionIDs(data.Abstain), []string{"c", "d"}) {
		t.Fatalf("data abstainers = %v, want c/d only", dimensionIDs(data.Abstain))
	}
	// The data dimension stays in its returned state: the concrete cluster
	// holds the required floor of the four-voter basis.
	if !data.Returned {
		t.Fatal("data dimension did not return its concrete assertion")
	}
}

// TestCallRevertOutcomeWithheldDataStaysAbsent pins the other half: when the
// concrete data cluster misses quorum, the data stays withheld from Value,
// the abstainers remain on the data dimension's abstain column, and the
// execution verdicts keep every source supported — exactly as the
// corroborated revert with abstainers behaved before the outcome seam
// existed.
func TestCallRevertOutcomeWithheldDataStaysAbsent(t *testing.T) {
	policy := planCallRevert()
	obs := []rpc.Observation{
		rpcErrorObs("a", 3, "execution reverted", `"0xdead"`),
		rpcErrorObs("b", 3, "execution reverted", `"0xbeef"`),
		rpcErrorObs("c", 3, "execution reverted", ``),
	}
	decision, err := policy.Resolve(obs)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	outcome := callOutcomeOutcomeStated(t, policy, obs)
	if decision.Status != rpc.Corroborated {
		t.Fatalf("status = %v, want a corroborated revert with withheld data", decision.Status)
	}
	for _, verdict := range outcome.Verdicts {
		if verdict.Kind != rpc.VerdictSupported {
			t.Fatalf("verdict for %s = %v", verdict.Source.ID, verdict.Kind)
		}
	}
	value := string(decision.Value)
	if strings.Contains(value, "0xdead") || strings.Contains(value, "0xbeef") {
		t.Fatalf("withheld data leaked into Value %s", value)
	}
	data, ok := callDataDimension(decision)
	if !ok {
		t.Fatalf("no data dimension in %+v", decision.Dimensions)
	}
	if data.Returned {
		t.Fatal("withheld data came back through the dimension statement")
	}
	if !reflect.DeepEqual(dimensionIDs(data.Abstain), []string{"c"}) {
		t.Fatalf("data abstainers = %v, want c only", dimensionIDs(data.Abstain))
	}
	if !reflect.DeepEqual(data.Abstain, dimensionIDsAsSources(data.Abstain)) {
		t.Fatal("abstain column lost its source identities")
	}
}

func callDataDimension(decision rpc.Decision) (rpc.DimensionEvidence, bool) {
	for _, dim := range decision.Dimensions {
		if dim.Name == "data" {
			return dim, true
		}
	}
	return rpc.DimensionEvidence{}, false
}

func dimensionIDs(sources []rpc.SourceRef) []string {
	ids := make([]string, 0, len(sources))
	for _, source := range sources {
		ids = append(ids, source.ID)
	}
	return ids
}

func dimensionIDsAsSources(sources []rpc.SourceRef) []rpc.SourceRef {
	return sources
}
