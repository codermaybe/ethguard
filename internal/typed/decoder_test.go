package typed

import (
	"errors"
	"testing"

	ethereum "github.com/ethereum/go-ethereum"
)

func TestDecodeBlockHeaderOnlyPreservesNotFound(t *testing.T) {
	_, err := DecodeBlockHeaderOnly([]byte("null"))
	if !errors.Is(err, ethereum.NotFound) {
		t.Fatalf("error = %v, want ethereum.NotFound", err)
	}
}

func TestDecodeReceiptRejectsIncompleteEnvelope(t *testing.T) {
	_, err := DecodeReceipt([]byte(`{"transactionHash":"0x0"}`))
	if err == nil {
		t.Fatal("incomplete receipt unexpectedly decoded")
	}
}
