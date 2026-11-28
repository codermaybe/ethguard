package ethguard_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/codermaybe/ethguard"
	"github.com/codermaybe/ethguard/ethapi"
	"github.com/codermaybe/ethguard/internal/methods"
	"github.com/codermaybe/ethguard/rpctest"
)

// TestBlockReceiptsBareHashReferenceKeepsHashBinding pins the quantity/hash
// anyOf overlap: the official quantity pattern also matches a 32-byte word,
// so the generated parser files a bare hash string under its quantity branch
// first. The facade's prep layer must keep the established bare-32-byte-word
// hash interpretation — the wire still carries the bare hash string, the
// answer binds by hash, and a hash-shaped reference never fails as an
// out-of-range height.
func TestBlockReceiptsBareHashReferenceKeepsHashBinding(t *testing.T) {
	client, nodes := fleet(t, 3)
	pin := "0x" + strings.Repeat("11", 32)
	txHash := ethapi.Hash("0x" + strings.Repeat("42", 32))
	raw := fixtureReceipt(t, txHash, 16)
	for _, node := range nodes {
		node.Handle(methods.MethodGetBlockReceipts, func(c rpctest.Call) (any, error) {
			// The bare reference must ride the wire as the bare hash string,
			// not as an EIP-1898 object and not as a rewritten height.
			var ref string
			if err := json.Unmarshal(c.Params[0], &ref); err != nil || ref != pin {
				return nil, errors.New("unexpected receipts reference " + string(c.Params[0]))
			}
			return []json.RawMessage{raw}, nil
		})
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// The bare hash string parses into the quantity branch of the generated
	// union; the prep layer reinterprets the 32-byte word as a hash.
	bare, err := ethapi.NewBlockReference(json.RawMessage(`"` + pin + `"`))
	if err != nil {
		t.Fatalf("bare hash reference: %v", err)
	}
	receipts, _, err := client.BlockReceipts(ctx, bare)
	if err != nil || len(receipts) != 1 || receipts[0].TransactionHash != txHash {
		t.Fatalf("bare hash receipts = %d, %v", len(receipts), err)
	}
}

// TestBlockReceiptsBareHashReferenceRejectsForeignBlock keeps the hash
// binding's failure class: a fleet returning another block's legitimate
// receipts fails as a malformed response at the admission binding —
// agreement never substitutes for the binding, and a hash-shaped reference
// is never misread as a height.
func TestBlockReceiptsBareHashReferenceRejectsForeignBlock(t *testing.T) {
	client, nodes := fleet(t, 3)
	pin := "0x" + strings.Repeat("11", 32)
	txHash := ethapi.Hash("0x" + strings.Repeat("42", 32))
	foreign := fixtureReceipt(t, txHash, 16)
	// Rewrite the whole receipt to a coherent foreign block: the object
	// admission binds every log's blockHash to the receipt's, so the JSON
	// must change everywhere before the facade binding is reached.
	foreignRaw := bytes.ReplaceAll(foreign,
		[]byte("0x"+strings.Repeat("11", 32)),
		[]byte("0x"+strings.Repeat("33", 32)))
	if !bytes.Contains(foreignRaw, []byte("0x"+strings.Repeat("33", 32))) {
		t.Fatal("fixture does not carry the expected block hash spelling")
	}
	for _, node := range nodes {
		node.Return(methods.MethodGetBlockReceipts, []json.RawMessage{json.RawMessage(foreignRaw)})
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	bare, err := ethapi.NewBlockReference(json.RawMessage(`"` + pin + `"`))
	if err != nil {
		t.Fatalf("bare hash reference: %v", err)
	}
	if _, _, err := client.BlockReceipts(ctx, bare); !errors.Is(err, ethguard.ErrMalformedResponse) {
		t.Fatalf("foreign block under bare hash reference = %v, want ErrMalformedResponse", err)
	}
}
