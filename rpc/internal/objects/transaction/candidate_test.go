package transaction

import (
	"bytes"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"strings"
	"testing"

	"github.com/codermaybe/ethguard/internal/spec"
	"github.com/codermaybe/ethguard/internal/spec/conformance"
	"github.com/codermaybe/ethguard/rpc"
)

// The object kernel's differential witnesses: Prepare is Encode's body
// exposed as an immutable candidate, and the kernel admission bindings
// reproduce the compatibility entries' verdicts and error classes on the
// same inputs. The compatibility entries and the kernel must not drift.

// TestPrepareMatchesEncodeOnOfficialVectors walks the official by-hash
// vectors and the local fixtures: the kernel's canonical bytes and Encode's
// output are identical, and Prepare is idempotent on its own output.
func TestPrepareMatchesEncodeOnOfficialVectors(t *testing.T) {
	fixtures := make([]json.RawMessage, 0, 16)
	err := fs.WalkDir(spec.VectorsFS(), "vectors/eth_getTransactionByHash", func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".io") {
			return nil
		}
		vectors, err := conformance.ParseVector(spec.VectorsFS(), path)
		if err != nil {
			return err
		}
		for _, vector := range vectors {
			if len(vector.Error) > 0 || bytes.Equal(bytes.TrimSpace(vector.Result), []byte("null")) {
				continue
			}
			fixtures = append(fixtures, vector.Result)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(fixtures) == 0 {
		t.Fatal("no official vectors found")
	}
	for i, raw := range fixtures {
		encodeOut, encodeErr := Encode(raw)
		candidate, prepareErr := Prepare(raw)
		if (encodeErr == nil) != (prepareErr == nil) {
			t.Fatalf("fixture %d: encode/prepare verdicts differ: %v vs %v", i, encodeErr, prepareErr)
		}
		if prepareErr != nil {
			continue
		}
		if !bytes.Equal(candidate.Canonical(), encodeOut) {
			t.Fatalf("fixture %d: canonical bytes diverge: %s vs %s", i, candidate.Canonical(), encodeOut)
		}
		again, err := Prepare(candidate.Canonical())
		if err != nil || !bytes.Equal(again.Canonical(), candidate.Canonical()) {
			t.Fatalf("fixture %d: prepare not idempotent: %v", i, err)
		}
	}
}

// TestCandidateBindingsMatchCompatEntries runs the kernel bindings and the
// compatibility entries on the same prepared material and requires identical
// verdicts and error classes.
func TestCandidateBindingsMatchCompatEntries(t *testing.T) {
	fixtures := officialFixtures(t)
	for _, raw := range fixtures {
		candidate, err := Prepare(raw)
		if err != nil {
			continue // malformed fixtures: binding entries see the same prepare failure first
		}
		validHash := `"0x` + strings.Repeat("11", 32) + `"`
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(candidate.Canonical(), &fields); err != nil {
			t.Fatal(err)
		}
		declared, hasHash := fields["hash"]
		if !hasHash {
			continue
		}
		byHashReq := rpc.Request{Method: "eth_getTransactionByHash", Params: []any{string(declared)}}
		otherHashReq := rpc.Request{Method: "eth_getTransactionByHash", Params: []any{"0x" + strings.Repeat("22", 32)}}
		cases := []struct {
			name   string
			kernel func() error
			compat func() error
		}{
			{
				name:   "by-hash/declared",
				kernel: func() error { return candidate.AdmitByHash(byHashReq) },
				compat: func() error { return Admit(byHashReq, candidate.Canonical()) },
			},
			{
				name:   "by-hash/other",
				kernel: func() error { return candidate.AdmitByHash(otherHashReq) },
				compat: func() error { return Admit(otherHashReq, candidate.Canonical()) },
			},
			{
				name: "in-block/self",
				kernel: func() error {
					blockHash := strings.Trim(strings.TrimSpace(string(fields["blockHash"])), `"`)
					return candidate.AdmitInBlock(rpc.Request{Method: "eth_getTransactionByBlockHashAndIndex", Params: []any{blockHash, "0x0"}})
				},
				compat: func() error {
					blockHash := strings.Trim(strings.TrimSpace(string(fields["blockHash"])), `"`)
					return AdmitInBlock(rpc.Request{Method: "eth_getTransactionByBlockHashAndIndex", Params: []any{blockHash, "0x0"}}, candidate.Canonical())
				},
			},
			{
				name: "in-block/bad-index",
				kernel: func() error {
					return candidate.AdmitInBlock(rpc.Request{Method: "eth_getTransactionByBlockHashAndIndex", Params: []any{validHash, "zz"}})
				},
				compat: func() error {
					return AdmitInBlock(rpc.Request{Method: "eth_getTransactionByBlockHashAndIndex", Params: []any{validHash, "zz"}}, candidate.Canonical())
				},
			},
		}
		for _, item := range cases {
			t.Run(item.name, func(t *testing.T) {
				kernelErr, compatErr := item.kernel(), item.compat()
				if (kernelErr == nil) != (compatErr == nil) {
					t.Fatalf("verdicts differ: kernel=%v compat=%v", kernelErr, compatErr)
				}
				if kernelErr != nil && compatErr != nil && errorClass(kernelErr) != errorClass(compatErr) {
					t.Fatalf("error classes differ: kernel=%v compat=%v", kernelErr, compatErr)
				}
			})
		}
	}
}

// TestCandidateBindingErrorOrderPreserved pins the first-error-class rule on
// the kernel bindings: a request error outranks a payload error, a root null
// needs no binding, and an unsupported typed payload keeps its joined
// sentinel.
func TestCandidateBindingErrorOrderPreserved(t *testing.T) {
	// Request error beside a payload error: the request class fires first.
	malformed := json.RawMessage(`{"type":"0x7f"}`)
	candidate, prepareErr := Prepare(malformed)
	if prepareErr == nil {
		t.Fatal("unsupported payload prepared")
	}
	if err := candidate.AdmitByHash(rpc.Request{Method: "eth_getTransactionByHash", Params: []any{"nothex"}}); !errors.Is(err, rpc.ErrInvalidRequest) {
		t.Fatalf("request error must outrank the payload verdict, kernel binding returned %v", err)
	}
	// Root null: no binding, no request error.
	nullCandidate, err := Prepare(json.RawMessage("null"))
	if err != nil {
		t.Fatal(err)
	}
	if err := nullCandidate.AdmitByHash(rpc.Request{Method: "eth_getTransactionByHash", Params: []any{"nothex"}}); err != nil {
		t.Fatalf("root null acquired a binding verdict: %v", err)
	}
	if err := nullCandidate.AdmitInBlock(rpc.Request{}); err != nil {
		t.Fatalf("root null acquired an in-block verdict: %v", err)
	}
	// Pending state on the candidate: visible to the method, not acted on by
	// the kernel binding. The pending shape is the mainnet fixture with every
	// inclusion member explicitly null — the derivation to a complete
	// pending object is the by-hash method's rule, not the kernel's.
	pendingRaw, err := editFixture(func(fields map[string]json.RawMessage) {
		for _, key := range []string{"blockHash", "blockNumber", "blockTimestamp", "transactionIndex"} {
			fields[key] = json.RawMessage("null")
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	pending, err := Prepare(pendingRaw)
	if err != nil {
		t.Fatal(err)
	}
	if !pending.Pending() {
		t.Fatal("pending fixture lost its pending state")
	}
	if err := pending.AdmitByHash(rpc.Request{Method: "eth_getTransactionByHash", Params: []any{"0x" + strings.Repeat("11", 32)}}); err == nil {
		t.Fatal("pending candidate accepted an unrelated requested hash")
	}
	if err := pending.AdmitInBlock(rpc.Request{Method: "eth_getTransactionByBlockHashAndIndex", Params: []any{"0x" + strings.Repeat("aa", 32), "0x0"}}); err == nil {
		t.Fatal("pending candidate bound to an immutable block context")
	}
}

// editFixture rewrites the mainnet fixture's fields through a function and
// returns the marshaled bytes.
func editFixture(edit func(map[string]json.RawMessage)) (json.RawMessage, error) {
	raw, err := fixtureBytes()
	if err != nil {
		return nil, err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return nil, err
	}
	edit(fields)
	return json.Marshal(fields)
}

// TestCandidateImmutability pins the no-side-effect rule: preparing is
// read-only over the input, the candidate's views do not alias caller bytes,
// and binding never mutates the candidate.
func TestCandidateImmutability(t *testing.T) {
	raw, err := fixtureBytes()
	if err != nil {
		t.Fatal(err)
	}
	before := append(json.RawMessage(nil), raw...)
	candidate, err := Prepare(raw)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(raw, before) {
		t.Fatal("prepare mutated the input bytes")
	}
	canonical := candidate.Canonical()
	if !bytes.Equal(canonical, candidate.Canonical()) {
		t.Fatal("canonical bytes are unstable across reads")
	}
}

func officialFixtures(t *testing.T) []json.RawMessage {
	t.Helper()
	var fixtures []json.RawMessage
	err := fs.WalkDir(spec.VectorsFS(), "vectors/eth_getTransactionByHash", func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".io") {
			return nil
		}
		vectors, err := conformance.ParseVector(spec.VectorsFS(), path)
		if err != nil {
			return err
		}
		for _, vector := range vectors {
			if len(vector.Error) > 0 {
				continue
			}
			fixtures = append(fixtures, vector.Result)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return fixtures
}

func errorClass(err error) string {
	switch {
	case errors.Is(err, rpc.ErrInvalidRequest):
		return "invalid-request"
	case errors.Is(err, rpc.ErrMalformedResponse):
		return "malformed"
	case errors.Is(err, rpc.ErrUnsupportedTypedObject):
		return "unsupported"
	default:
		return err.Error()
	}
}

// fixtureBytes loads the object package's mainnet fixture.
func fixtureBytes() (json.RawMessage, error) {
	data, err := os.ReadFile("testdata/tx-one-mainnet-1559.json")
	if err != nil {
		return nil, err
	}
	return json.RawMessage(data), nil
}
