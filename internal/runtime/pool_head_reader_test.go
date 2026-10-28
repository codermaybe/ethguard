package runtime

import (
	nativehttp "github.com/codermaybe/ethguard/internal/transport/http"

	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/codermaybe/ethguard/internal/reference"
	"github.com/codermaybe/ethguard/pool"
	"github.com/codermaybe/ethguard/rpc"
	"github.com/codermaybe/ethguard/rpctest"
)

// The basis-driven reader addresses exactly the invocation's frozen voting
// basis: a basis naming two of three registered members reads those two, and
// the confirmation round resolves its members against the same snapshot even
// if pool membership changed in between.
func TestPoolHeadReaderAddressesBasisMembers(t *testing.T) {
	nodes := []*rpctest.Node{rpctest.NewNode(1), rpctest.NewNode(1), rpctest.NewNode(1)}
	defer func() {
		for _, node := range nodes {
			node.Close()
		}
	}()
	raw := json.RawMessage(`{"number":"0x10","hash":"0xabc","parentHash":"0x1111","txRoot":"0x2222"}`)
	for _, node := range nodes {
		node.ReturnRaw("eth_getBlockByNumber", raw)
	}
	p := pool.NewPoolWithOptions(0, pool.WithDialer(nativehttp.NewDialer()))
	joined := make([]*pool.Source, 0, len(nodes))
	for _, node := range nodes {
		source, err := p.Join(node.URL(), node.URL())
		if err != nil {
			t.Fatal(err)
		}
		joined = append(joined, source)
	}
	basis := rpc.FreezeBasis([]rpc.SourceRef{
		{ID: joined[0].ID()},
		{ID: joined[2].ID()},
	}, rpc.Corroboration{Mode: rpc.CorroborationMajority, Floor: 2})
	reader, err := PoolHeadReaderForBasis(p, basis)
	if err != nil {
		t.Fatalf("PoolHeadReaderForBasis: %v", err)
	}
	observations, err := reader.Read(context.Background(), "finalized", 2*time.Second)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(observations) != 2 {
		t.Fatalf("read %d observations, want exactly the two basis members", len(observations))
	}
	seen := map[string]bool{}
	for _, observation := range observations {
		seen[observation.Source.ID] = true
	}
	if !seen[joined[0].ID()] || !seen[joined[2].ID()] {
		t.Fatalf("observations = %v, want basis members only", seen)
	}

	// Confirmation resolves its members against the frozen snapshot.
	confirmations, err := reader.Confirm(context.Background(), 16, []string{joined[0].ID()}, 2*time.Second)
	if err != nil {
		t.Fatalf("Confirm: %v", err)
	}
	if len(confirmations) != 1 || confirmations[0].Source.ID != joined[0].ID() {
		t.Fatalf("confirmations = %+v, want the one named basis member", confirmations)
	}
}

// A basis naming an unregistered member fails before any network round.
func TestPoolHeadReaderRejectsUnknownBasisMember(t *testing.T) {
	p := pool.NewPoolWithOptions(0, pool.WithDialer(nativehttp.NewDialer()))
	_, err := PoolHeadReaderForBasis(p, rpc.FreezeBasis([]rpc.SourceRef{{ID: "missing"}}, rpc.Corroboration{}))
	if err == nil {
		t.Fatal("unknown basis member must fail construction")
	}
}

// The production codec applies the block method's own head contract: a
// well-formed head decodes to its height and lowercased hash; a body without
// an identity fails.
func TestMethodHeadCodecDecodeContract(t *testing.T) {
	view, err := MethodHeadCodec{}.Decode(json.RawMessage(`{"number":"0x10F","hash":"0xABCDEF"}`))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if view.Height != 0x10f || view.Hash != "0xabcdef" {
		t.Fatalf("view = %+v", view)
	}
	codec := MethodHeadCodec{}
	if _, err := codec.Decode(json.RawMessage(`{"hash":"0xabcdef"}`)); err == nil {
		t.Fatal("a head without a height must fail to decode")
	}
	// The comparison key matches the block method's identity key.
	key, err := codec.Normalize(json.RawMessage(`{"number":"0x10","hash":"0xabc"}`))
	if err != nil {
		t.Fatalf("Normalize: %v", err)
	}
	if key != "16|0xabc" {
		t.Fatalf("key = %q, want 16|0xabc", key)
	}
}

// The codec satisfies the reference seam, keeping the dependency direction
// reference ← runtime.
var _ reference.HeadCodec = MethodHeadCodec{}
