package header

import (
	"bytes"
	"encoding/json"
	"reflect"
	"testing"
)

func TestDecodeWithCanonicalKeepsBothHeaderProducts(t *testing.T) {
	for _, raw := range []json.RawMessage{
		testHeader(t), json.RawMessage(`null`), json.RawMessage(`{}`),
		json.RawMessage(`{"hash":"bad","hash":"bad"}`),
	} {
		before := bytes.Clone(raw)
		canonical, head, err := DecodeWithCanonical(raw)
		wantCanonical, normalizeErr := Normalize(raw)
		wantHead, decodeErr := Decode(raw)
		if !bytes.Equal(canonical, wantCanonical) || !reflect.DeepEqual(head, wantHead) ||
			!reflect.DeepEqual(err, normalizeErr) || !reflect.DeepEqual(err, decodeErr) {
			t.Fatalf("combined header result differs: %v / %v / %v", err, normalizeErr, decodeErr)
		}
		if !bytes.Equal(before, raw) {
			t.Fatal("source mutated")
		}
		if err == nil {
			wantHash := head.Hash()
			canonical[0] = '!'
			if !bytes.Equal(raw, before) || wantCanonical[0] != '{' {
				t.Fatal("canonical header aliases independent bytes")
			}
			raw[0] = '!'
			if head.Hash() != wantHash {
				t.Fatal("header products share mutable storage")
			}
		}
	}
}
