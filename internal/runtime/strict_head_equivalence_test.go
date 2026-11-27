package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"slices"
	"testing"
	"time"

	"github.com/codermaybe/ethguard/internal/pool"
	"github.com/codermaybe/ethguard/internal/rpcengine"
	nativehttp "github.com/codermaybe/ethguard/internal/transport/http"
	"github.com/codermaybe/ethguard/observe"
	"github.com/codermaybe/ethguard/rpc"
	"github.com/codermaybe/ethguard/rpctest"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
)

const (
	equivTimeout = 2 * time.Second
	equivHeight  = 100
)

// Use the complete, self-consistent Header shape from the block policy
// fixtures. Changing the parent produces a distinct valid head; malformed
// cases start from these bytes and corrupt only the field under test.
func equivHeader(t *testing.T, height uint64, parent string) (json.RawMessage, string) {
	t.Helper()
	header := &types.Header{
		ParentHash: common.HexToHash(parent), UncleHash: types.EmptyUncleHash,
		Root: common.HexToHash("0x2"), TxHash: types.EmptyTxsHash,
		ReceiptHash: common.HexToHash("0x3"), Difficulty: new(big.Int),
		Number: new(big.Int).SetUint64(height), GasLimit: 1, Time: 1,
	}
	raw, err := header.MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	return raw, header.Hash().Hex()
}

type headHandler = rpctest.Handler

func equivFleet(t *testing.T, serve ...headHandler) []*rpctest.Node {
	t.Helper()
	raw, _ := equivHeader(t, equivHeight, "0x1")
	common := rpctest.Value(raw)
	if len(serve) == 0 {
		serve = []headHandler{common, common, common}
	}
	nodes := make([]*rpctest.Node, len(serve))
	for i, handler := range serve {
		node := rpctest.NewNode(1)
		t.Cleanup(node.Close)
		if handler == nil {
			handler = common
		}
		node.Handle("eth_getBlockByNumber", handler)
		nodes[i] = node
	}
	return nodes
}

func equivPool(t *testing.T, nodes []*rpctest.Node) *pool.Pool {
	t.Helper()
	members := make([]pool.Member, 0, len(nodes))
	for i, node := range nodes {
		members = append(members, pool.Member{Name: fmt.Sprintf("node-%d", i), URL: node.URL()})
	}
	p, err := pool.NewWithMembers(0, members, pool.WithDialer(nativehttp.NewDialer()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Close)
	return p
}

func establishService(t *testing.T, nodes []*rpctest.Node, rule rpcengine.Corroboration, tag string) (rpc.ReferenceEvidence, error) {
	t.Helper()
	stage := NewReferenceStage(ReferenceStageConfig{
		Pool: equivPool(t, nodes), Timeout: equivTimeout, Recorder: observe.Nop,
	})
	return NewReferenceService(stage, rule).ReferenceForTag(context.Background(), tag)
}

func referenceSourceNames(refs []rpc.SourceRef) []string {
	names := make([]string, len(refs))
	for i, ref := range refs {
		names[i] = ref.Name
	}
	return names
}

// Pin the service's concrete evidence contract, including the error path.
// Agreement with another call through the same stage would not prove that
// either call actually established the expected reference.
func TestReferenceServiceStrictEvidence(t *testing.T) {
	raw, hash := equivHeader(t, equivHeight, "0x1")
	dissenting, _ := equivHeader(t, equivHeight, "0x4")
	malformed := bytes.Replace(raw, []byte(`"number":"0x64"`), []byte(`"number":"wrong"`), 1)
	if bytes.Equal(raw, malformed) {
		t.Fatal("malformed-number fixture did not change the header")
	}
	all := []string{"node-0", "node-1", "node-2"}
	pair := []string{"node-0", "node-1"}
	for _, tc := range []struct {
		name       string
		serve      []headHandler
		down       bool
		tag        string
		mode       rpc.CorroborationMode
		floor      int
		status     rpc.Status
		err        error
		support    []string
		reject     []string
		faults     int
		candidates int
		reason     rpc.DecisionReason
	}{
		{name: "unanimous-latest", floor: 2, status: rpc.Corroborated, support: all, candidates: 1, reason: rpc.ReasonAccepted},
		{name: "one-provider-down", down: true, floor: 2, status: rpc.Corroborated, support: pair, faults: 1, candidates: 1, reason: rpc.ReasonAccepted},
		{name: "one-dissenting-head", serve: []headHandler{nil, nil, rpctest.Value(dissenting)}, floor: 2, status: rpc.Corroborated, support: pair, reject: []string{"node-2"}, candidates: 2, reason: rpc.ReasonAccepted},
		{name: "encode-failure-stays-in-denominator", serve: []headHandler{nil, nil, rpctest.Value(malformed)}, floor: 2, status: rpc.Corroborated, support: pair, faults: 1, candidates: 1, reason: rpc.ReasonAccepted},
		{name: "unanimity-floor-with-one-failure", down: true, mode: rpc.CorroborationUnanimity, floor: 3, status: rpc.Unknown, err: rpc.ErrInsufficientSupport, support: pair, faults: 1, candidates: 1, reason: rpc.ReasonInsufficientSupport},
		{name: "safe-tag", tag: "safe", floor: 2, status: rpc.Corroborated, support: all, candidates: 1, reason: rpc.ReasonAccepted},
	} {
		t.Run(tc.name, func(t *testing.T) {
			nodes := equivFleet(t, tc.serve...)
			if tc.down {
				nodes[2].Close()
			}
			ref, err := establishService(t, nodes, rpcengine.Corroboration{Mode: tc.mode, Floor: tc.floor}, tc.tag)
			if !errors.Is(err, tc.err) || ref.Status != tc.status {
				t.Fatalf("status/error = %s / %v, want %s / %v", ref.Status, err, tc.status, tc.err)
			}
			wantHash, wantHeight := hash, uint64(equivHeight)
			if tc.err != nil {
				wantHash, wantHeight = "", 0
			}
			if ref.Hash != wantHash || ref.Height != wantHeight {
				t.Fatalf("reference = %s at %d, want %s at %d", ref.Hash, ref.Height, wantHash, wantHeight)
			}
			if ref.Adjudication.Basis != 3 || ref.Adjudication.Required != tc.floor || ref.Adjudication.Reason != tc.reason || len(ref.Adjudication.Candidates) != tc.candidates {
				t.Fatalf("adjudication = %+v, want basis 3, required %d, reason %v, candidates %d", ref.Adjudication, tc.floor, tc.reason, tc.candidates)
			}
			if !slices.Equal(referenceSourceNames(ref.Support), tc.support) || !slices.Equal(referenceSourceNames(ref.Reject), tc.reject) || !slices.Equal(referenceSourceNames(ref.Participants), all) {
				t.Fatalf("support/reject/participants = %v / %v / %v, want %v / %v / %v", ref.Support, ref.Reject, ref.Participants, tc.support, tc.reject, all)
			}
			if !slices.Equal(referenceSourceNames(ref.Adjudication.Candidates[0].Sources), tc.support) || (tc.candidates == 2 && !slices.Equal(referenceSourceNames(ref.Adjudication.Candidates[1].Sources), tc.reject)) {
				t.Fatalf("candidate membership = %+v, want support %v and reject %v", ref.Adjudication.Candidates, tc.support, tc.reject)
			}
			if len(ref.Sources) != 3 || len(ref.SourceFaults) != tc.faults || len(ref.Faults) != tc.faults || len(ref.CollectionFaults) != 0 {
				t.Fatalf("source/fault evidence = %+v", ref)
			}
			if tc.faults != 0 && (ref.SourceFaults[0].Source.Name != "node-2" || ref.SourceFaults[0].Err == nil || ref.Sources[2].Relation != rpc.VerdictUnusable) {
				t.Fatalf("failed source lost its attribution: %+v", ref)
			}
			origin := "strict-head"
			if tc.tag == "safe" {
				origin = "tagged-head"
			}
			if ref.ObservedAt.IsZero() || ref.Tag != tc.tag || ref.Origin != origin {
				t.Fatalf("reference timestamp/tag/origin = %v / %q / %q", ref.ObservedAt, ref.Tag, ref.Origin)
			}
		})
	}
}

func TestReferenceServiceCollectionFailureKeepsPartialEvidence(t *testing.T) {
	nodes := equivFleet(t)
	for _, node := range nodes {
		node.Close()
	}
	ref, err := establishService(t, nodes, rpcengine.Corroboration{Floor: 2}, "latest")
	if !errors.Is(err, pool.ErrAllSourcesDown) {
		t.Fatalf("collection failure = %v, want the pool sentinel", err)
	}
	if ref.Status != rpc.Unknown || ref.Hash != "" || ref.Height != 0 || ref.ObservedAt.IsZero() || ref.Tag != "" || ref.Origin != "strict-head" {
		t.Fatalf("failed reference = %+v", ref)
	}
	if ref.Adjudication.Basis != 3 || ref.Adjudication.Required != 2 || len(ref.Adjudication.Candidates) != 0 || len(ref.Support) != 0 || len(ref.Reject) != 0 {
		t.Fatalf("collection failure lost its basis or acquired votes: %+v", ref)
	}
	if len(ref.Participants) != 3 || len(ref.Sources) != 3 || len(ref.SourceFaults) != 3 || len(ref.CollectionFaults) != 1 || !errors.Is(ref.CollectionFaults[0], pool.ErrAllSourcesDown) {
		t.Fatalf("collection failure lost its source/collection evidence: %+v", ref)
	}
}

func TestReferenceServiceMalformedBodyKeepsPartialEvidence(t *testing.T) {
	// Deeply nested valid JSON rejected by structural admission, before
	// reference identity comparison or Header encoding.
	var bloat bytes.Buffer
	bloat.WriteString(`{"number":"0x64","hash":"0xabc","nest":[`)
	for i := 0; i < 4000; i++ {
		bloat.WriteByte('[')
	}
	for i := 0; i < 4000; i++ {
		bloat.WriteByte(']')
	}
	bloat.WriteString("]}")
	nodes := equivFleet(t)
	for _, node := range nodes {
		node.ReturnRaw("eth_getBlockByNumber", bloat.Bytes())
	}
	ref, err := establishService(t, nodes, rpcengine.Corroboration{Floor: 2}, "safe")
	if !errors.Is(err, rpc.ErrMalformedResponse) {
		t.Fatalf("service error = %v, want malformed response", err)
	}
	if ref.Status != rpc.Unknown || ref.Hash != "" || ref.Height != 0 || ref.ObservedAt.IsZero() || ref.Tag != "safe" || ref.Origin != "tagged-head" {
		t.Fatalf("malformed reference = %+v", ref)
	}
	if ref.Adjudication.Basis != 3 || ref.Adjudication.Required != 2 || len(ref.Adjudication.Candidates) != 0 || len(ref.Support) != 0 || len(ref.Reject) != 0 {
		t.Fatalf("malformed responses lost the basis or acquired votes: %+v", ref)
	}
	if len(ref.Participants) != 3 || len(ref.Sources) != 3 || len(ref.SourceFaults) != 3 || len(ref.CollectionFaults) != 0 {
		t.Fatalf("malformed response lost its source evidence: %+v", ref)
	}
}
