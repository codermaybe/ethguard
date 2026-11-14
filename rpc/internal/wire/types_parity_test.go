package wire_test

// These tests protect project comparison bytes, not the complete official wire
// contract. types_official_test.go separately checks schema shape, preservation,
// and canonical idempotence without applying a protected-field projection.

import (
	"bytes"
	"encoding/json"
	"io/fs"
	"strings"
	"testing"

	"github.com/codermaybe/ethguard/ethapi"
	"github.com/codermaybe/ethguard/internal/ethcodec"
	"github.com/codermaybe/ethguard/internal/spec"
	"github.com/codermaybe/ethguard/internal/spec/conformance"
	"github.com/codermaybe/ethguard/rpc/internal/objects/accesslist"
	"github.com/codermaybe/ethguard/rpc/internal/objects/feehistory"
	"github.com/codermaybe/ethguard/rpc/internal/objects/log"
	receiptobjects "github.com/codermaybe/ethguard/rpc/internal/objects/receipt"
)

func officialVectors(t *testing.T) []conformance.Vector {
	t.Helper()
	var vectors []conformance.Vector
	err := fs.WalkDir(spec.VectorsFS(), ".", func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".io") {
			return nil
		}
		pairs, err := conformance.ParseVector(spec.VectorsFS(), path)
		if err != nil {
			return err
		}
		vectors = append(vectors, pairs...)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(vectors) == 0 {
		t.Fatal("no official vectors")
	}
	return vectors
}

// resultVectors selects exact methods and keeps null results distinct from
// error responses. Full path plus pair index is retained in every test name.
func resultVectors(t *testing.T, method string) []conformance.Vector {
	t.Helper()
	var results []conformance.Vector
	for _, vector := range officialVectors(t) {
		if vector.Method == method && vector.Error == nil {
			results = append(results, vector)
		}
	}
	if len(results) == 0 {
		t.Fatalf("no successful official vectors for %s", method)
	}
	return results
}

// An official success must independently succeed on each side. Two errors do
// not establish parity: they can hide a shared admission regression.
func compareBytes(t *testing.T, got json.RawMessage, gotErr error, want json.RawMessage, wantErr error) {
	t.Helper()
	if gotErr != nil {
		t.Errorf("generated layer rejects official success: %v", gotErr)
	}
	if wantErr != nil {
		t.Errorf("protected encoder rejects official success: %v", wantErr)
	}
	if gotErr != nil || wantErr != nil {
		return
	}
	if !bytes.Equal(got, want) {
		t.Errorf("canonical bytes diverge:\n generated: %s\n protected: %s", got, want)
	}
}

func TestGeneratedLogsMatchProtectedLogEncoder(t *testing.T) {
	for _, vector := range resultVectors(t, "eth_getLogs") {
		t.Run(vector.Identity(), func(t *testing.T) {
			value, err := ethapi.NewGetLogsResult(vector.Result)
			var out json.RawMessage
			if err == nil {
				out, err = ethcodec.MarshalNoEscape(value)
			}
			protected, protectedErr := log.Encode(vector.Result)
			compareBytes(t, out, err, protected, protectedErr)
		})
	}
}

func TestGeneratedReceiptMatchesProtectedReceiptEncoder(t *testing.T) {
	nulls, receipts := 0, 0
	for _, vector := range resultVectors(t, "eth_getTransactionReceipt") {
		t.Run(vector.Identity(), func(t *testing.T) {
			value, err := ethapi.NewGetTransactionReceiptResult(vector.Result)
			if ethcodec.IsJSONNull(vector.Result) {
				nulls++
				if err != nil || value != nil {
					t.Fatalf("null receipt must parse as nil: %v, %v", value, err)
				}
			} else {
				receipts++
				if err == nil && value == nil {
					t.Fatal("non-null receipt became nil")
				}
			}
			var out json.RawMessage
			if err == nil {
				out, err = ethcodec.MarshalNoEscape(value)
			}
			protected, protectedErr := receiptobjects.Encode(vector.Result)
			compareBytes(t, out, err, protected, protectedErr)
		})
	}
	if nulls == 0 || receipts == 0 {
		t.Fatalf("need both nullable and object receipt vectors: null=%d, object=%d", nulls, receipts)
	}
}

func TestGeneratedAccessListMatchesProtectedAccessListEncoder(t *testing.T) {
	for _, vector := range resultVectors(t, "eth_createAccessList") {
		t.Run(vector.Identity(), func(t *testing.T) {
			value, err := ethapi.NewCreateAccessListResult(vector.Result)
			var out json.RawMessage
			if err == nil {
				out, err = ethcodec.MarshalNoEscape(value)
			}
			protected, protectedErr := accesslist.Encode(vector.Result)
			compareBytes(t, out, err, protected, protectedErr)
		})
	}
}

func TestGeneratedFeeHistoryMatchesProtectedFeeHistoryEncoder(t *testing.T) {
	for _, vector := range resultVectors(t, "eth_feeHistory") {
		t.Run(vector.Identity(), func(t *testing.T) {
			value, err := ethapi.NewFeeHistoryResult(vector.Result)
			var out json.RawMessage
			if err == nil {
				out, err = ethcodec.MarshalNoEscape(value)
			}
			protected, protectedErr := feehistory.Encode(vector.Result)
			compareBytes(t, out, err, protected, protectedErr)
		})
	}
}
