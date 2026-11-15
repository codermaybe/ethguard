package ethguard

import (
	"bytes"
	"fmt"
	"reflect"

	"github.com/codermaybe/ethguard/rpc"
)

// BinaryTransaction supplies an encoded transaction for the raw write path.
// Encoding does not establish validity: the private write implementation
// validates the frozen payload and derives the hash before broadcasting.
// Implementations must not mutate the returned bytes concurrently with the call.
type BinaryTransaction interface {
	MarshalBinary() ([]byte, error)
}

// snapshotBinaryTransaction invokes caller code once and takes ownership of
// its output. Validation, hashing and broadcast must all consume this snapshot.
func snapshotBinaryTransaction(tx BinaryTransaction) ([]byte, error) {
	if tx == nil {
		return nil, fmt.Errorf("%w: nil transaction", rpc.ErrInvalidRequest)
	}
	v := reflect.ValueOf(tx)
	switch v.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		if v.IsNil() {
			return nil, fmt.Errorf("%w: nil transaction", rpc.ErrInvalidRequest)
		}
	}
	raw, err := tx.MarshalBinary()
	if err != nil {
		return nil, fmt.Errorf("%w: encode transaction: %v", rpc.ErrInvalidRequest, err)
	}
	if len(raw) == 0 {
		return nil, fmt.Errorf("%w: empty raw transaction", rpc.ErrInvalidRequest)
	}
	return bytes.Clone(raw), nil
}
