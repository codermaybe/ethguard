package receipt

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/codermaybe/ethguard/rpc"
)

func blockReceiptArray(t *testing.T, rows ...json.RawMessage) json.RawMessage {
	t.Helper()
	if rows == nil {
		return json.RawMessage(`[]`)
	}
	raw, err := json.Marshal(rows)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// These literal admission expectations are frozen before adding the processing
// entry. In particular, each row binds before the next row is validated.
func TestBlockReceiptAdmissionOrder(t *testing.T) {
	valid := receiptFixture(t)
	badLogs := deleteField(t, valid, "logs")
	foreign := replaceField(t, valid, "blockHash", "0x"+strings.Repeat("22", 32))
	validRequest := rpc.Request{Params: []any{receiptBlkHash}}
	badRequest := rpc.Request{Params: []any{123}}
	cases := []struct {
		name  string
		req   rpc.Request
		raw   json.RawMessage
		class error
		want  string
	}{
		{"null-before-request", badRequest, json.RawMessage(` null `), nil, ""},
		{"empty", validRequest, json.RawMessage(`[]`), nil, ""},
		{"empty-still-checks-request", badRequest, json.RawMessage(`[]`), rpc.ErrInvalidRequest, "rpc: invalid request: block reference must be a string or object"},
		{"shape-before-request", badRequest, json.RawMessage(`{}`), rpc.ErrMalformedResponse, "rpc: malformed response: block receipts must be an array"},
		{"request-before-row", badRequest, json.RawMessage(`[null]`), rpc.ErrInvalidRequest, "rpc: invalid request: block reference must be a string or object"},
		{"null-row", validRequest, json.RawMessage(`[null]`), rpc.ErrMalformedResponse, "rpc: malformed response: receipt 0: receipt: invalid payload"},
		{"row-shape-before-binding", validRequest, blockReceiptArray(t, deleteField(t, foreign, "logs")), rpc.ErrMalformedResponse, "rpc: malformed response: receipt 0: receipt: logs must be an array"},
		{"first-binding-before-second-shape", validRequest, blockReceiptArray(t, foreign, badLogs), rpc.ErrMalformedResponse, "rpc: malformed response: receipt 0 block hash does not match requested hash"},
		{"second-binding", validRequest, blockReceiptArray(t, valid, foreign), rpc.ErrMalformedResponse, "rpc: malformed response: receipt 1 block hash does not match requested hash"},
		{"second-shape", validRequest, blockReceiptArray(t, valid, badLogs), rpc.ErrMalformedResponse, "rpc: malformed response: receipt 1: receipt: logs must be an array"},
		{"height-binding", rpc.Request{Params: []any{"0x1"}}, blockReceiptArray(t, valid), rpc.ErrMalformedResponse, "rpc: malformed response: receipt 0 block number does not match requested number"},
		{"zero-identity", validRequest, blockReceiptArray(t, replaceField(t, valid, "transactionHash", "0x"+strings.Repeat("00", 32))), rpc.ErrMalformedResponse, "rpc: malformed response: receipt 0: receipt: missing transaction or block hash"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := AdmitBlock(tc.req, tc.raw)
			canonical, preparedErr := PrepareBlock(tc.req, tc.raw)
			if tc.class == nil {
				if err != nil || preparedErr != nil {
					t.Fatalf("admit=%v prepare=%v", err, preparedErr)
				}
				want, encodeErr := EncodeBlock(tc.raw)
				if encodeErr != nil || !bytes.Equal(canonical, want) {
					t.Fatalf("prepare=%s, want encoder bytes %s: %v", canonical, want, encodeErr)
				}
				return
			}
			if !errors.Is(err, tc.class) || err.Error() != tc.want {
				t.Fatalf("admit = %v; want %q (%v)", err, tc.want, tc.class)
			}
			if canonical != nil || !errors.Is(preparedErr, tc.class) || preparedErr.Error() != tc.want {
				t.Fatalf("prepare = %s, %v; want nil, %q (%v)", canonical, preparedErr, tc.want, tc.class)
			}
		})
	}
}

func TestPrepareBlockRetainsBytesOrderAndOwnership(t *testing.T) {
	first := receiptFixture(t, logFixture())
	second := replaceField(t, first, "gasUsed", "0x1")
	raw := blockReceiptArray(t, first, second)
	want, err := EncodeBlock(raw)
	if err != nil {
		t.Fatal(err)
	}
	got, err := PrepareBlock(rpc.Request{Params: []any{receiptBlkHash}}, raw)
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("prepared=%s want=%s: %v", got, want, err)
	}
	for i := range raw {
		raw[i] = ' '
	}
	if !bytes.Equal(got, want) {
		t.Fatal("prepared bytes alias the input")
	}
	again, err := PrepareBlock(rpc.Request{Params: []any{receiptBlkHash}}, blockReceiptArray(t, first, second))
	if err != nil {
		t.Fatal(err)
	}
	got[0] = ' '
	if !bytes.Equal(again, want) {
		t.Fatal("independent preparation results alias")
	}
}
