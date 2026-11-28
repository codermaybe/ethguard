package ethguard_test

import (
	"context"
	"testing"

	"github.com/codermaybe/ethguard/internal/methods"
)

func TestTypedScalarStandardAndFiniteCompatibility(t *testing.T) {
	client, nodes := fleet(t, 3)
	ctx := context.Background()
	for _, node := range nodes {
		node.Return(methods.MethodNetVersion, "+0001")
		node.Return(methods.MethodPeerCount, "0x000A")
	}
	network, _, err := client.NetworkID(ctx)
	if err != nil || network.String() != "1" {
		t.Fatalf("network finite compatibility: %v %v", network, err)
	}
	peers, _, err := client.PeerCount(ctx)
	if err != nil || peers != 10 {
		t.Fatalf("peer finite compatibility: %v %v", peers, err)
	}
	for _, node := range nodes {
		node.Return(methods.MethodNetVersion, "-1")
		node.Return(methods.MethodPeerCount, "0XA")
	}
	if _, _, err := client.NetworkID(ctx); err == nil {
		t.Fatal("typed network admitted negative value")
	}
	if _, _, err := client.PeerCount(ctx); err == nil {
		t.Fatal("typed peer count widened prefix compatibility")
	}
	for _, node := range nodes {
		node.Return(methods.MethodNetVersion, "-0")
	}
	network, _, err = client.NetworkID(ctx)
	if err != nil || network.Sign() != 0 {
		t.Fatalf("negative zero compatibility lost: %v %v", network, err)
	}
}
