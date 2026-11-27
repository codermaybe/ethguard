package ethguard

import (
	"errors"
	"testing"

	"github.com/codermaybe/ethguard/rpc"
)

type countingBinaryTransaction struct {
	calls int
	raw   []byte
	err   error
}

func (tx *countingBinaryTransaction) MarshalBinary() ([]byte, error) {
	tx.calls++
	return tx.raw, tx.err
}

func TestBinaryTransactionSnapshotOwnsOneEncoding(t *testing.T) {
	tx := &countingBinaryTransaction{raw: []byte{1, 2, 3}}
	raw, err := snapshotBinaryTransaction(tx)
	if err != nil || tx.calls != 1 {
		t.Fatalf("snapshot = %x, %v; calls = %d", raw, err, tx.calls)
	}
	tx.raw[0] = 9
	if raw[0] != 1 {
		t.Fatal("snapshot aliases caller-owned bytes")
	}
}

func TestBinaryTransactionSnapshotRejectsNilAndEncodingFailures(t *testing.T) {
	var typedNil *countingBinaryTransaction
	for _, tx := range []BinaryTransaction{nil, typedNil, &countingBinaryTransaction{}, &countingBinaryTransaction{err: errors.New("cannot encode")}} {
		if raw, err := snapshotBinaryTransaction(tx); !errors.Is(err, rpc.ErrInvalidRequest) || raw != nil {
			t.Fatalf("snapshot = %x, %v; want local invalid request", raw, err)
		}
	}
}
