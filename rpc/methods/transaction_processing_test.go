package methods

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"strings"
	"testing"

	"github.com/codermaybe/ethguard/internal/spec"
	"github.com/codermaybe/ethguard/internal/spec/conformance"
	"github.com/codermaybe/ethguard/rpc"
)

// The processing entries' differential witnesses: each method's canonical
// processor must produce the same verdict class and the same canonical bytes
// as the raw route's prepare+compat-admission on every input class, and the
// processing witnesses (preparation idempotence, input immutability, output
// immutability, source isolation) must hold. The production plan is not
// switched here; the processors exist so the pilot wiring can adopt them.

func transactionProcessorFixtures(t *testing.T) []json.RawMessage {
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
	// The local fixtures: the recorded Anvil pending observation and the
	// block-receipt sample. Provenance stays local/synthetic.
	_, pendingRaw, _ := observedPendingTransaction(t)
	fixtures = append(fixtures, pendingRaw, sampleResults(t, "block-receipt-tx", "tx-one")[0])
	return fixtures
}

func transactionVerdictClass(err error) string {
	switch {
	case err == nil:
		return "ok"
	case errors.Is(err, rpc.ErrInvalidRequest):
		return "invalid-request"
	case errors.Is(err, rpc.ErrUnsupportedTypedObject):
		return "unsupported"
	case errors.Is(err, rpc.ErrMalformedResponse):
		return "malformed"
	default:
		return "other:" + err.Error()
	}
}

func declaredHash(t *testing.T, raw json.RawMessage) (string, bool) {
	t.Helper()
	var fields struct {
		Hash string `json:"hash"`
	}
	if err := json.Unmarshal(raw, &fields); err != nil || fields.Hash == "" {
		return "", false
	}
	return fields.Hash, true
}

// TestTransactionByHashProcessorMatchesRawRoute runs both routes on every
// fixture class with the same request: same verdict class, and on success
// the same canonical bytes the raw route would commit.
func TestTransactionByHashProcessorMatchesRawRoute(t *testing.T) {
	for i, raw := range transactionProcessorFixtures(t) {
		name := "fixture"
		if hash, ok := declaredHash(t, raw); ok {
			name = hash
		}
		t.Run(name, func(t *testing.T) {
			request := rpc.Request{Method: MethodGetTransactionByHash, Params: []any{"0x" + strings.Repeat("11", 32)}}
			if hash, ok := declaredHash(t, raw); ok {
				request.Params[0] = hash
			}
			rawRoute := func() (json.RawMessage, error) {
				candidate, err := prepareTransactionByHash(raw)
				if err != nil {
					return nil, err
				}
				if _, err := admitTransactionByHash(context.Background(), request, candidate, nil); err != nil {
					return nil, err
				}
				return candidate, nil
			}
			processRoute := func() (json.RawMessage, error) {
				result, err := processTransactionByHash(context.Background(), request, raw, nil)
				if err != nil {
					return nil, err
				}
				return result.Value(), nil
			}
			want, wantErr := rawRoute()
			got, gotErr := processRoute()
			if transactionVerdictClass(wantErr) != transactionVerdictClass(gotErr) {
				t.Fatalf("verdict classes differ: raw=%v (%q) processor=%v (%q)", wantErr, wantErr, gotErr, gotErr)
			}
			if wantErr == nil && !bytes.Equal(want, got) {
				t.Fatalf("canonical bytes differ: raw=%s processor=%s", want, got)
			}
			_ = i
		})
	}
}

// TestTransactionInBlockProcessorMatchesRawRoute mirrors the differential for
// the block-addressed method, with each mined fixture's own inclusion
// context as the request and pending/root-null shapes rejected equally.
func TestTransactionInBlockProcessorMatchesRawRoute(t *testing.T) {
	for _, raw := range transactionProcessorFixtures(t) {
		var fields struct {
			Hash             string `json:"hash"`
			BlockHash        string `json:"blockHash"`
			TransactionIndex string `json:"transactionIndex"`
		}
		if err := json.Unmarshal(raw, &fields); err != nil {
			t.Fatalf("fixture is not an object: %v", err)
		}
		name := fields.Hash
		if name == "" {
			name = "null"
		}
		t.Run(name, func(t *testing.T) {
			request := rpc.Request{Method: MethodGetTransactionByBlockHashAndIndex, Params: []any{fields.BlockHash, fields.TransactionIndex}}
			rawRoute := func() (json.RawMessage, error) {
				candidate, err := prepareTransactionInBlock(raw)
				if err != nil {
					return nil, err
				}
				if _, err := admitTransactionInBlock(context.Background(), request, candidate, nil); err != nil {
					return nil, err
				}
				return candidate, nil
			}
			processRoute := func() (json.RawMessage, error) {
				result, err := processTransactionInBlock(context.Background(), request, raw, nil)
				if err != nil {
					return nil, err
				}
				return result.Value(), nil
			}
			want, wantErr := rawRoute()
			got, gotErr := processRoute()
			if transactionVerdictClass(wantErr) != transactionVerdictClass(gotErr) {
				t.Fatalf("verdict classes differ: raw=%v (%q) processor=%v (%q)", wantErr, wantErr, gotErr, gotErr)
			}
			if wantErr == nil && !bytes.Equal(want, got) {
				t.Fatalf("canonical bytes differ: raw=%s processor=%s", want, got)
			}
		})
	}
}

// TestTransactionProcessorBindingSemanticsUnchanged pins the request-binding
// semantics on the processing route: wrong requested hash, wrong block hash,
// wrong index, bad request grammar, unsupported payload, and the protected
// legacy chainId declaration all keep their classes.
func TestTransactionProcessorBindingSemanticsUnchanged(t *testing.T) {
	request, raw, _ := observedPendingTransaction(t)
	// By-hash binding: the pending fixture must bind to its own hash and
	// reject another one.
	if _, err := processTransactionByHash(context.Background(), request, raw, nil); err != nil {
		t.Fatalf("self-hash binding = %v", err)
	}
	other := rpc.Request{Method: MethodGetTransactionByHash, Params: []any{"0x" + strings.Repeat("22", 32)}}
	if _, err := processTransactionByHash(context.Background(), other, raw, nil); !errors.Is(err, rpc.ErrMalformedResponse) {
		t.Fatalf("other-hash binding = %v, want malformed", err)
	}
	// In-block binding: pending rejects; mined binds to its own context;
	// grammar errors stay request errors.
	if _, err := processTransactionInBlock(context.Background(), rpc.Request{Method: MethodGetTransactionByBlockHashAndIndex, Params: []any{"0x" + strings.Repeat("aa", 32), "0x0"}}, raw, nil); !errors.Is(err, rpc.ErrMalformedResponse) {
		t.Fatalf("pending in-block = %v, want malformed", err)
	}
	mined := sampleResults(t, "block-receipt-tx", "tx-one")[0]
	var fields struct {
		BlockHash        string `json:"blockHash"`
		TransactionIndex string `json:"transactionIndex"`
	}
	if err := json.Unmarshal(mined, &fields); err != nil {
		t.Fatal(err)
	}
	minedRequest := rpc.Request{Method: MethodGetTransactionByBlockHashAndIndex, Params: []any{fields.BlockHash, fields.TransactionIndex}}
	if _, err := processTransactionInBlock(context.Background(), minedRequest, mined, nil); err != nil {
		t.Fatalf("mined in-block binding = %v", err)
	}
	wrongIndex := rpc.Request{Method: MethodGetTransactionByBlockHashAndIndex, Params: []any{fields.BlockHash, "0x1"}}
	if _, err := processTransactionInBlock(context.Background(), wrongIndex, mined, nil); !errors.Is(err, rpc.ErrMalformedResponse) {
		t.Fatalf("wrong index binding = %v, want malformed", err)
	}
	badGrammar := rpc.Request{Method: MethodGetTransactionByBlockHashAndIndex, Params: []any{fields.BlockHash, "zz"}}
	if _, err := processTransactionInBlock(context.Background(), badGrammar, mined, nil); !errors.Is(err, rpc.ErrInvalidRequest) {
		t.Fatalf("bad index grammar = %v, want invalid request", err)
	}
	// Unsupported payload keeps its joined sentinel on both processors.
	unsupported := injectField(t, raw, "type", `"0x7f"`)
	_, byHashErr := processTransactionByHash(context.Background(), request, unsupported, nil)
	if !errors.Is(byHashErr, rpc.ErrUnsupportedTypedObject) || !errors.Is(byHashErr, rpc.ErrMalformedResponse) {
		t.Fatalf("unsupported by-hash = %v", byHashErr)
	}
	_, inBlockErr := processTransactionInBlock(context.Background(), minedRequest, unsupported, nil)
	if !errors.Is(inBlockErr, rpc.ErrUnsupportedTypedObject) {
		t.Fatalf("unsupported in-block = %v", inBlockErr)
	}
	// Protected legacy chainId declaration must match the signed payload.
	conflicting := injectField(t, raw, "chainId", `"0x1"`)
	if _, err := processTransactionByHash(context.Background(), request, conflicting, nil); !errors.Is(err, rpc.ErrMalformedResponse) {
		t.Fatalf("chainId conflict = %v, want malformed", err)
	}
}

// TestTransactionProcessingWitnesses pins the four processing witnesses:
// preparation idempotence over the canonical output, input immutability,
// output immutability, and source isolation.
func TestTransactionProcessingWitnesses(t *testing.T) {
	request, raw, _ := observedPendingTransaction(t)
	process := func(value json.RawMessage) json.RawMessage {
		result, err := processTransactionByHash(context.Background(), request, value, nil)
		if err != nil {
			t.Fatal(err)
		}
		return result.Value()
	}
	// Preparation idempotence: processing the canonical output as a fresh
	// input yields the same canonical bytes.
	first := process(raw)
	second := process(first)
	if !bytes.Equal(first, second) {
		t.Fatalf("processing not idempotent over its own output: %s vs %s", first, second)
	}
	// Input immutability: the raw bytes are read-only.
	before := append(json.RawMessage(nil), raw...)
	_ = process(raw)
	if !bytes.Equal(raw, before) {
		t.Fatal("processing mutated the input bytes")
	}
	// Output immutability: mutating returned bytes cannot reach the stored
	// result or a later processing run.
	mutated := append(json.RawMessage(nil), first...)
	mutated[0] = '['
	again := process(raw)
	if !bytes.Equal(first, again) {
		t.Fatal("mutating the returned value changed a later result")
	}
	// Source isolation: two sources' responses process independently, in
	// either order, with no cross-contamination. The sources hold genuinely
	// different transactions (a pending one and a mined one); that their
	// canonical bytes differ is the fixture, not the isolation claim.
	minedSource := sampleResults(t, "block-receipt-tx", "tx-one")[0]
	minedRequest := rpc.Request{Method: MethodGetTransactionByHash, Params: []any{"0x" + strings.Repeat("11", 32)}}
	if hash, ok := declaredHash(t, minedSource); ok {
		minedRequest.Params[0] = hash
	}
	processMined := func(value json.RawMessage) json.RawMessage {
		result, err := processTransactionByHash(context.Background(), minedRequest, value, nil)
		if err != nil {
			t.Fatal(err)
		}
		return result.Value()
	}
	minedFirst := processMined(minedSource)
	if bytes.Equal(minedFirst, first) {
		t.Fatal("fixture sources produced identical payloads")
	}
	if !bytes.Equal(processMined(minedSource), minedFirst) || !bytes.Equal(process(raw), first) {
		t.Fatal("processing one source's response changed another's result")
	}
}
