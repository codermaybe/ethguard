package ethadapter_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"

	"ethguard"
	"ethguard/ethadapter"
	"ethguard/rpc"
	"ethguard/rpc/methods"
	"ethguard/rpctest"
)

// TestCallRevertRule4eMatrix is the decision-0039 matrix for eth_call.
// Each row runs the method resolver and the real aggregator against the same
// httptest answers, then checks the typed CallContract gate. Missing and
// null abstain; they are not empty bytes. A short data cluster does not
// become ErrConflict and does not return those bytes.
func TestCallRevertRule4eMatrix(t *testing.T) {
	cases := []rule4eCase{
		{
			name: "N=2 withhold",
			answers: []revertAnswer{
				{name: "node-a", kind: "empty"},
				{name: "node-b", kind: "missing"},
			},
			wantStatus:          rpc.Corroborated,
			wantData:            dataAbsent,
			wantAbstain:         []string{"node-b"},
			wantDataRejectEmpty: true,
			wantDeleted:         []string{"data"},
		},
		{
			name: "N=3 empty quorum",
			answers: []revertAnswer{
				{name: "node-a", kind: "empty"},
				{name: "node-b", kind: "empty"},
				{name: "node-c", kind: "missing"},
			},
			wantStatus:          rpc.Corroborated,
			wantData:            dataEmpty,
			wantAbstain:         []string{"node-c"},
			wantDataRejectEmpty: true,
		},
		{
			name: "N=3 concrete conflict",
			answers: []revertAnswer{
				{name: "node-a", kind: "hex", data: `"0xdead"`},
				{name: "node-b", kind: "hex", data: `"0xbeef"`},
				{name: "node-c", kind: "missing"},
			},
			wantStatus:  rpc.Corroborated,
			wantData:    dataAbsent,
			wantAbstain: []string{"node-c"},
			wantDeleted: []string{"data"},
			absentBytes: []string{"0xdead", "0xbeef"},
			// Equal-sized data clusters rank by key, so "hex:0xbeef" wins
			// over "hex:0xdead". The representative is the first source of
			// that cluster, node-b, and only its data is withheld.
			withheldBytes: []string{"0xbeef"},
		},
		{
			name: "N=3 all abstain",
			answers: []revertAnswer{
				{name: "node-a", kind: "missing"},
				{name: "node-b", kind: "missing"},
				{name: "node-c", kind: "missing"},
			},
			wantStatus:           rpc.Corroborated,
			wantData:             dataAbsent,
			wantAbstain:          []string{"node-a", "node-b", "node-c"},
			wantDataSupportEmpty: true,
			wantDataRejectEmpty:  true,
		},
		{
			name: "N=3 one concrete",
			answers: []revertAnswer{
				{name: "node-a", kind: "missing"},
				{name: "node-b", kind: "missing"},
				{name: "node-c", kind: "hex", data: `"0xdead"`},
			},
			wantStatus:  rpc.Corroborated,
			wantData:    dataAbsent,
			wantAbstain: []string{"node-a", "node-b"},
			wantDeleted: []string{"data"},
			absentBytes: []string{"0xdead"},
		},
		{
			name: "N=4 short empty cluster",
			answers: []revertAnswer{
				{name: "node-a", kind: "empty"},
				{name: "node-b", kind: "empty"},
				{name: "node-c", kind: "missing"},
				{name: "node-d", kind: "missing"},
			},
			wantStatus:  rpc.Corroborated,
			wantData:    dataAbsent,
			wantAbstain: []string{"node-c", "node-d"},
			wantDeleted: []string{"data"},
		},
		{
			name: "unanimity concrete quorum",
			mode: rpc.CorroborationUnanimity,
			answers: []revertAnswer{
				{name: "node-a", kind: "hex", data: `"0xdead"`},
				{name: "node-b", kind: "hex", data: `"0xdead"`},
				{name: "node-c", kind: "missing"},
			},
			wantStatus:          rpc.Corroborated,
			wantData:            dataHex,
			wantHex:             "0xdead",
			wantAbstain:         []string{"node-c"},
			wantDataRejectEmpty: true,
		},
		{
			name: "unanimity data split",
			mode: rpc.CorroborationUnanimity,
			answers: []revertAnswer{
				{name: "node-a", kind: "hex", data: `"0xdead"`},
				{name: "node-b", kind: "hex", data: `"0xbeef"`},
				{name: "node-c", kind: "missing"},
			},
			wantStatus:  rpc.Corroborated,
			wantData:    dataAbsent,
			wantAbstain: []string{"node-c"},
			wantDeleted: []string{"data"},
			absentBytes: []string{"0xdead", "0xbeef"},
			// Same ranking as the majority split above: the beef cluster is
			// first, so only node-b's data is withheld.
			withheldBytes: []string{"0xbeef"},
		},
		{
			name: "concrete data quorum",
			answers: []revertAnswer{
				{name: "node-a", kind: "hex", data: `"0xdead"`},
				{name: "node-b", kind: "hex", data: `"0xdead"`},
				{name: "node-c", kind: "hex", data: `"0xbeef"`},
			},
			wantStatus:     rpc.Corroborated,
			wantData:       dataHex,
			wantHex:        "0xdead",
			wantDataReject: []string{"node-c"},
		},
		{
			name: "execution split",
			answers: []revertAnswer{
				{name: "node-a", kind: "ok", data: `"0x01"`},
				{name: "node-b", kind: "missing"},
			},
			wantErr:    rpc.ErrConflict,
			wantStatus: rpc.Unknown,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assertRule4e(t, tc)
		})
	}
}

type dataExpect int

const (
	dataAbsent dataExpect = iota
	dataEmpty
	dataHex
)

type revertAnswer struct {
	name string
	kind string
	data string
}

type rule4eCase struct {
	name                 string
	mode                 rpc.CorroborationMode
	answers              []revertAnswer
	wantStatus           rpc.Status
	wantErr              error
	wantData             dataExpect
	wantHex              string
	wantAbstain          []string
	wantDataReject       []string
	wantDataSupportEmpty bool
	wantDataRejectEmpty  bool
	wantDeleted          []string
	absentBytes          []string
	withheldBytes        []string
}

func assertRule4e(t *testing.T, tc rule4eCase) {
	t.Helper()
	opts := []ethguard.Option{}
	snap := rpc.Corroboration{Mode: rpc.CorroborationMajority, Floor: 2}
	if tc.mode == rpc.CorroborationUnanimity {
		opts = append(opts)
		snap.Mode = rpc.CorroborationUnanimity
	}
	adapter, nodes := fleet(t, len(tc.answers), opts...)
	headFixture(nodes)
	for i, answer := range tc.answers {
		scriptRevertAnswer(nodes[i], answer)
	}
	policy := methods.CallPolicy(func(context.Context) (string, error) {
		t.Fatal("policy resolve must not anchor")
		return "", errors.New("anchor")
	})
	resolver, ok := policy.(rpc.CorroboratingPolicy)
	if !ok {
		t.Fatal("eth_call lost ResolveCorroborated")
	}
	obs := make([]rpc.Observation, len(tc.answers))
	for i, answer := range tc.answers {
		obs[i] = observationFor(answer)
	}
	policyDecision, policyErr := resolver.ResolveCorroborated(snap, obs)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	to := common.HexToAddress("0x1")
	arg := map[string]any{"to": to.Hex()}
	aggDecision, aggErr := adapter.Unwrap().Call(ctx, methods.MethodCall, arg, "latest")
	if policyDecision.Status != aggDecision.Status || !errors.Is(policyErr, tc.wantErr) || !errors.Is(aggErr, tc.wantErr) {
		t.Fatalf("policy %s/%v aggregator %s/%v, want %s/%v", policyDecision.Status, policyErr, aggDecision.Status, aggErr, tc.wantStatus, tc.wantErr)
	}
	if aggDecision.Status != tc.wantStatus {
		t.Fatalf("status = %s, want %s", aggDecision.Status, tc.wantStatus)
	}
	if !sameStringList(policyDecision.DeletedFields, aggDecision.DeletedFields) || !sameStringList(aggDecision.DeletedFields, tc.wantDeleted) {
		t.Fatalf("deleted policy %v aggregator %v want %v", policyDecision.DeletedFields, aggDecision.DeletedFields, tc.wantDeleted)
	}
	if tc.wantErr != nil {
		return
	}
	assertDataDimension(t, policyDecision, tc)
	assertDataDimension(t, aggDecision, tc)
	for _, needle := range tc.absentBytes {
		if bytes.Contains(aggDecision.Value, []byte(needle)) {
			t.Fatalf("value still contains %s: %s", needle, aggDecision.Value)
		}
	}
	kept := tc.withheldBytes
	if kept == nil {
		kept = tc.absentBytes
	}
	for _, needle := range kept {
		if !bytes.Contains(aggDecision.Evidence, []byte(needle)) {
			t.Fatalf("evidence lost %s: %s", needle, aggDecision.Evidence)
		}
	}
	for _, needle := range tc.absentBytes {
		withheld := false
		for _, keep := range kept {
			if keep == needle {
				withheld = true
			}
		}
		if !withheld && bytes.Contains(aggDecision.Evidence, []byte(needle)) {
			t.Fatalf("evidence spliced in %s from another source: %s", needle, aggDecision.Evidence)
		}
	}
	_, err := adapter.CallContract(ctx, ethereum.CallMsg{To: &to}, nil)
	var revert *ethadapter.RevertError
	if !errors.As(err, &revert) {
		t.Fatalf("typed err = %v, want RevertError", err)
	}
	switch tc.wantData {
	case dataAbsent:
		if revert.Data != nil || revert.ErrorData() != nil {
			t.Fatalf("typed data = %x raw %s", revert.Data, revert.DataRaw)
		}
	case dataEmpty:
		if revert.Data == nil || len(revert.Data) != 0 || hexutil.Encode(revert.Data) != "0x" {
			t.Fatalf("typed empty data = %x", revert.Data)
		}
	case dataHex:
		if hexutil.Encode(revert.Data) != tc.wantHex {
			t.Fatalf("typed data = %s, want %s", hexutil.Encode(revert.Data), tc.wantHex)
		}
	}
}

func assertDataDimension(t *testing.T, decision rpc.Decision, tc rule4eCase) {
	t.Helper()
	var data *rpc.DimensionEvidence
	var execution *rpc.DimensionEvidence
	for i := range decision.Dimensions {
		switch decision.Dimensions[i].Name {
		case "data":
			data = &decision.Dimensions[i]
		case "execution":
			execution = &decision.Dimensions[i]
		}
	}
	if execution == nil || !execution.Returned {
		t.Fatalf("execution dimension = %+v", decision.Dimensions)
	}
	if data == nil {
		t.Fatal("missing data dimension")
	}
	if !sameStringList(names(data.Abstain), tc.wantAbstain) {
		t.Fatalf("abstain = %v, want %v", names(data.Abstain), tc.wantAbstain)
	}
	if tc.wantDataRejectEmpty && len(data.Reject) != 0 {
		t.Fatalf("data reject = %v, want empty", names(data.Reject))
	}
	if tc.wantDataSupportEmpty && len(data.Support) != 0 {
		t.Fatalf("data support = %v, want empty", names(data.Support))
	}
	if tc.wantDataReject != nil && !sameStringList(names(data.Reject), tc.wantDataReject) {
		t.Fatalf("data reject = %v, want %v", names(data.Reject), tc.wantDataReject)
	}
	for _, abstainer := range names(data.Abstain) {
		if containsName(data.Reject, abstainer) {
			t.Fatalf("abstainer %s is in data reject", abstainer)
		}
	}
	returned := tc.wantData != dataAbsent
	if data.Returned != returned {
		t.Fatalf("data returned = %v, want %v", data.Returned, returned)
	}
}

func scriptRevertAnswer(node *rpctest.Node, answer revertAnswer) {
	switch answer.kind {
	case "ok":
		node.ReturnRaw(methods.MethodCall, json.RawMessage(answer.data))
	case "missing":
		node.FailWithData(methods.MethodCall, 3, "execution reverted", nil, false)
	case "empty":
		node.FailWithData(methods.MethodCall, 3, "execution reverted", json.RawMessage(`"0x"`), true)
	case "hex":
		node.FailWithData(methods.MethodCall, 3, "execution reverted", json.RawMessage(answer.data), true)
	default:
		panic(answer.kind)
	}
}

func observationFor(answer revertAnswer) rpc.Observation {
	source := rpc.SourceRef{Name: answer.name, ID: answer.name}
	if answer.kind == "ok" {
		return rpc.Observation{Source: source, Value: json.RawMessage(answer.data)}
	}
	body := &rpc.RPCError{Code: 3, Message: "execution reverted"}
	switch answer.kind {
	case "empty":
		body.Data = json.RawMessage(`"0x"`)
	case "hex":
		body.Data = json.RawMessage(answer.data)
	}
	return rpc.Observation{Source: source, Err: errors.New("execution reverted"), RPCError: body}
}

func names(refs []rpc.SourceRef) []string {
	out := make([]string, len(refs))
	for i, ref := range refs {
		out[i] = ref.Name
	}
	return out
}

func containsName(refs []rpc.SourceRef, name string) bool {
	for _, ref := range refs {
		if ref.Name == name {
			return true
		}
	}
	return false
}

func sameStringList(left, right []string) bool {
	if len(left) == 0 && len(right) == 0 {
		return true
	}
	return strings.Join(left, ",") == strings.Join(right, ",")
}
