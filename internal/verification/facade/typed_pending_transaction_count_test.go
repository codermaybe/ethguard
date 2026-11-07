package ethguard_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/codermaybe/ethguard"
	"github.com/codermaybe/ethguard/rpc"
	"github.com/codermaybe/ethguard/rpc/methods"
)

func TestPendingTransactionCountIsSelectedAndUnanchored(t *testing.T) {
	client, nodes := fleet(t, 3)
	for _, node := range nodes {
		node.Return(methods.MethodGetBlockTransactionCountByNumber, "0x2")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	count, result, err := client.PendingTransactionCount(ctx)
	if err != nil || count != 2 {
		t.Fatalf("pending transaction count = %d, result=%+v, err=%v", count, result, err)
	}
	if result.Decision == nil || result.Decision.Status != rpc.Selected {
		t.Fatalf("pending transaction count decision = %+v, want Selected", result.Decision)
	}
	if result.Decision.Reference != nil || result.Decision.BlockRef != "" {
		t.Fatalf("pending transaction count established reference: %+v", result.Decision)
	}
	viewCount, viewErr := client.EthClient().PendingTransactionCount(ctx)
	if viewErr != nil || viewCount != 2 {
		t.Fatalf("view pending transaction count = %d, err=%v", viewCount, viewErr)
	}
}

func TestPendingTransactionCountNullIsMalformed(t *testing.T) {
	client, nodes := fleet(t, 1)
	for _, node := range nodes {
		node.Return(methods.MethodGetBlockTransactionCountByNumber, nil)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	count, result, err := client.PendingTransactionCount(ctx)
	if count != 0 || result.Decision == nil || err == nil || !errors.Is(err, ethguard.ErrNoValidResponse) {
		t.Fatalf("pending null = count:%d result:%+v err:%v, want no-valid-response", count, result, err)
	}
}

// Pending counts are node-local observations. A multi-source disagreement is
// therefore a Selected observation from the first usable source, never a
// Corroborated value or an immutable-read conflict.
func TestPendingTransactionCountDissentStaysSelected(t *testing.T) {
	client, nodes := fleet(t, 3)
	for i, node := range nodes {
		node.Return(methods.MethodGetBlockTransactionCountByNumber, []string{"0x2", "0x3", "0x4"}[i])
	}

	count, result, err := client.PendingTransactionCount(context.Background())
	if err != nil || count != 2 {
		t.Fatalf("pending dissent = %d result=%+v err=%v", count, result, err)
	}
	if result.Decision == nil || result.Decision.Status != rpc.Selected {
		t.Fatalf("pending dissent decision = %+v, want Selected", result.Decision)
	}
	if errors.Is(err, ethguard.ErrConflict) || result.Decision.Status == rpc.Corroborated {
		t.Fatalf("pending dissent was treated as corroborated conflict: result=%+v err=%v", result, err)
	}
	if len(result.Decision.Support) != 1 || result.Decision.Reference != nil || result.Decision.BlockRef != "" {
		t.Fatalf("pending dissent evidence = %+v, want one local support and no reference", result.Decision)
	}
}

// A malformed response from one source must not poison a usable pending-local
// observation from another source. The result remains Selected and never
// claims cross-source corroboration.
func TestPendingTransactionCountSkipsMalformedSource(t *testing.T) {
	client, nodes := fleet(t, 3)
	nodes[0].Return(methods.MethodGetBlockTransactionCountByNumber, "not-hex")
	nodes[1].Return(methods.MethodGetBlockTransactionCountByNumber, "0x2")
	nodes[2].Return(methods.MethodGetBlockTransactionCountByNumber, "0x3")

	count, result, err := client.PendingTransactionCount(context.Background())
	if err != nil || count != 2 {
		t.Fatalf("pending malformed source = %d result=%+v err=%v", count, result, err)
	}
	if result.Decision == nil || result.Decision.Status != rpc.Selected || result.Decision.Status == rpc.Corroborated {
		t.Fatalf("pending malformed source decision = %+v, want Selected", result.Decision)
	}
	if len(result.Decision.Support) != 1 || len(result.Decision.Faults) == 0 {
		t.Fatalf("pending malformed source evidence = %+v, want one support and one fault", result.Decision)
	}
}

// If every source is malformed, PendingTransactionCount must fail closed with
// an Unknown decision. It must not turn a malformed cluster into Conflict or
// a guessed Selected value.
func TestPendingTransactionCountAllMalformedIsUnknown(t *testing.T) {
	client, nodes := fleet(t, 3)
	for _, node := range nodes {
		node.Return(methods.MethodGetBlockTransactionCountByNumber, "not-hex")
	}

	count, result, err := client.PendingTransactionCount(context.Background())
	if count != 0 || err == nil || !errors.Is(err, ethguard.ErrNoValidResponse) {
		t.Fatalf("pending all malformed = %d result=%+v err=%v, want no-valid-response", count, result, err)
	}
	if result.Decision == nil || result.Decision.Status != rpc.Unknown {
		t.Fatalf("pending all malformed decision = %+v, want Unknown", result.Decision)
	}
	if errors.Is(err, ethguard.ErrConflict) || errors.Is(err, ethguard.ErrMalformedResponse) {
		t.Fatalf("pending all malformed was misclassified: result=%+v err=%v", result, err)
	}
}
