package transaction

import (
	"bytes"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/codermaybe/ethguard/internal/spec"
	"github.com/codermaybe/ethguard/internal/spec/conformance"
	"github.com/codermaybe/ethguard/rpc"
)

// Official success vectors must prepare, and their canonical representations
// must remain valid and byte-stable when prepared again.
func TestPrepareOfficialVectorsIsIdempotent(t *testing.T) {
	fixtures := officialFixtures(t)
	if len(fixtures) == 0 {
		t.Fatal("no official vectors found")
	}
	for i, raw := range fixtures {
		candidate, err := Prepare(raw)
		if err != nil {
			t.Fatalf("official fixture %d rejected: %v", i, err)
		}
		canonical := candidate.Canonical()
		if !json.Valid(canonical) {
			t.Fatalf("fixture %d produced invalid JSON: %s", i, canonical)
		}
		again, err := Prepare(canonical)
		if err != nil || !bytes.Equal(again.Canonical(), canonical) {
			t.Fatalf("fixture %d: prepare not idempotent: %v", i, err)
		}
	}
}

// Each official payload binds to its declared identity and rejects foreign
// identities or invalid requests with independently specified error classes.
func TestCandidateBindingsAcrossOfficialPayloads(t *testing.T) {
	accepts := map[string]int{}
	for _, raw := range officialFixtures(t) {
		if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			continue
		}
		candidate, err := Prepare(raw)
		if err != nil {
			t.Fatalf("official fixture rejected: %v", err)
		}
		var fields struct {
			Hash      string `json:"hash"`
			BlockHash string `json:"blockHash"`
			Index     string `json:"transactionIndex"`
		}
		if err := json.Unmarshal(raw, &fields); err != nil {
			t.Fatal(err)
		}
		if !isHexHashString(fields.Hash) || !isHexHashString(fields.BlockHash) || fields.Index == "" {
			t.Fatal("official fixture has no declared identity")
		}
		cases := []struct {
			name  string
			bind  func() error
			class error
			text  string
		}{
			{"by-hash/declared", func() error { return candidate.AdmitByHash(rpc.Request{Params: []any{fields.Hash}}) }, nil, ""},
			{"by-hash/other", func() error {
				return candidate.AdmitByHash(rpc.Request{Params: []any{"0x" + strings.Repeat("22", 32)}})
			}, rpc.ErrMalformedResponse, "transaction hash does not match requested hash"},
			{"by-hash/bad-request", func() error { return candidate.AdmitByHash(rpc.Request{Params: []any{"zz"}}) }, rpc.ErrInvalidRequest, "transaction hash must be a 32-byte hex string"},
			{"in-block/declared", func() error {
				return candidate.AdmitInBlock(rpc.Request{Params: []any{fields.BlockHash, fields.Index}})
			}, nil, ""},
			{"in-block/other-block", func() error {
				return candidate.AdmitInBlock(rpc.Request{Params: []any{"0x" + strings.Repeat("33", 32), fields.Index}})
			}, rpc.ErrMalformedResponse, "transaction block hash does not match requested block"},
			{"in-block/bad-index", func() error { return candidate.AdmitInBlock(rpc.Request{Params: []any{fields.BlockHash, "zz"}}) }, rpc.ErrInvalidRequest, "transaction index:"},
		}
		for _, item := range cases {
			t.Run(item.name, func(t *testing.T) {
				err := item.bind()
				if item.class == nil {
					if err != nil {
						t.Fatal(err)
					}
					accepts[item.name]++
					return
				}
				if !errors.Is(err, item.class) || !strings.Contains(err.Error(), item.text) {
					t.Fatalf("binding = %v, want %v and %q", err, item.class, item.text)
				}
				otherClass := rpc.ErrInvalidRequest
				if errors.Is(item.class, rpc.ErrInvalidRequest) {
					otherClass = rpc.ErrMalformedResponse
				}
				if errors.Is(err, otherClass) || errors.Is(err, rpc.ErrUnsupportedTypedObject) {
					t.Fatalf("binding carries an unrelated error class: %v", err)
				}
			})
		}
	}
	for _, name := range []string{"by-hash/declared", "in-block/declared"} {
		if accepts[name] == 0 {
			t.Fatalf("%s never exercised a real acceptance", name)
		}
	}
}

// isHexHashString reports whether the unquoted request value is a 32-byte hex
// hash, the grammar the request parsers enforce.
func isHexHashString(s string) bool {
	return len(s) == 66 && strings.HasPrefix(s, "0x")
}

// TestCandidateBindingErrorOrderPreserved pins the first-error-class rule on
// the kernel bindings: a request error outranks a payload error, a root null
// needs no binding, and an unsupported typed payload keeps its joined
// sentinel.
func TestCandidateBindingErrorOrderPreserved(t *testing.T) {
	// Preparation succeeds for a structurally valid declaration with a forged
	// hash. Binding checks request grammar before validating that payload.
	malformed, err := editFixture(func(fields map[string]json.RawMessage) {
		fields["hash"] = json.RawMessage(`"0x` + strings.Repeat("11", 32) + `"`)
	})
	if err != nil {
		t.Fatal(err)
	}
	candidate, err := Prepare(malformed)
	if err != nil {
		t.Fatal(err)
	}
	if err := candidate.AdmitByHash(rpc.Request{Params: []any{"nothex"}}); !errors.Is(err, rpc.ErrInvalidRequest) || errors.Is(err, rpc.ErrMalformedResponse) {
		t.Fatalf("request error must outrank the payload verdict: %v", err)
	}
	if err := candidate.AdmitByHash(rpc.Request{Params: []any{"0x" + strings.Repeat("11", 32)}}); !errors.Is(err, rpc.ErrMalformedResponse) || !strings.Contains(err.Error(), "hash does not match signed payload") {
		t.Fatalf("forged payload was not rejected: %v", err)
	}
	_, err = Prepare(json.RawMessage(`{"type":"0x7f"}`))
	if !errors.Is(err, rpc.ErrMalformedResponse) || !errors.Is(err, rpc.ErrUnsupportedTypedObject) {
		t.Fatalf("unsupported payload lost its joined error classes: %v", err)
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

// TestCandidateCanonicalReturnsIndependentBytes pins the getter-isolation
// rule: writing to the bytes Canonical returns never changes the candidate's
// later output or its binding verdicts, a copied candidate stays independent,
// and concurrent read-only access is safe.
func TestCandidateCanonicalReturnsIndependentBytes(t *testing.T) {
	raw, err := fixtureBytes()
	if err != nil {
		t.Fatal(err)
	}
	candidate, err := Prepare(raw)
	if err != nil {
		t.Fatal(err)
	}
	before := append(json.RawMessage(nil), candidate.Canonical()...)
	// A caller that writes to the returned slice must not change the
	// candidate: re-reads and binding verdicts stay as they were.
	borrowed := candidate.Canonical()
	borrowed[0] = '['
	if !bytes.Equal(candidate.Canonical(), before) {
		t.Fatal("writing to the returned bytes changed the candidate's canonical output")
	}
	var hash string
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(before, &fields); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(fields["hash"], &hash); err != nil {
		t.Fatal(err)
	}
	req := rpc.Request{Method: "eth_getTransactionByHash", Params: []any{hash}}
	if err := candidate.AdmitByHash(req); err != nil {
		t.Fatalf("binding changed after a caller corrupted the returned bytes: %v", err)
	}
	// A copied candidate is an independent value: corrupting one copy's
	// returned bytes leaves the other's output untouched.
	copied := candidate
	copied.Canonical()[0] = '['
	if !bytes.Equal(candidate.Canonical(), before) {
		t.Fatal("a copied candidate mutated the original's canonical output")
	}
	// Concurrent read-only access over the same candidate value.
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if !bytes.Equal(candidate.Canonical(), before) {
				t.Error("concurrent read saw unstable canonical bytes")
			}
		}()
	}
	wg.Wait()
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

// fixtureBytes loads the object package's mainnet fixture.
func fixtureBytes() (json.RawMessage, error) {
	data, err := os.ReadFile("testdata/tx-one-mainnet-1559.json")
	if err != nil {
		return nil, err
	}
	return json.RawMessage(data), nil
}
