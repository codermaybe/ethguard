package methods

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"ethguard/rpc"
)

func TestStateReadPoliciesPinMovingTagsAndPreserveExplicitReferences(t *testing.T) {
	anchorCalls := 0
	anchor := func(ctx context.Context) (string, error) {
		anchorCalls++
		if got := rpc.AnchorTag(ctx); got != "latest" {
			t.Fatalf("anchor tag = %q, want latest", got)
		}
		return "0x" + "11" + "22", nil
	}
	cases := []struct {
		name   string
		policy rpc.Policy
		params []any
		pos    int
	}{
		{"balance", GetBalancePolicy(anchor), []any{"0xabc", "latest"}, 1},
		{"nonce", GetTransactionCountPolicy(anchor), []any{"0xabc", "latest"}, 1},
		{"code", GetCodePolicy(anchor), []any{"0xabc", "latest"}, 1},
		{"storage", GetStorageAtPolicy(anchor), []any{"0xabc", "0x01", "latest"}, 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			prepared, err := tc.policy.Prepare(context.Background(), rpc.Request{Method: tc.name, Params: tc.params})
			if err != nil {
				t.Fatal(err)
			}
			if got := prepared.Params[tc.pos]; got != "0x1122" {
				t.Fatalf("moving reference = %v, want pinned hash", got)
			}
		})
	}
	if anchorCalls != len(cases) {
		t.Fatalf("anchor calls = %d, want %d", anchorCalls, len(cases))
	}

	for _, tc := range cases {
		for _, ref := range []string{"0x" + "33" + "44", "pending"} {
			before := anchorCalls
			params := append([]any(nil), tc.params...)
			params[tc.pos] = ref
			prepared, err := tc.policy.Prepare(context.Background(), rpc.Request{Method: tc.name, Params: params})
			if err != nil {
				t.Fatalf("%s %s: %v", tc.name, ref, err)
			}
			if got := prepared.Params[tc.pos]; got != ref {
				t.Fatalf("%s %s rewritten to %v", tc.name, ref, got)
			}
			if anchorCalls != before {
				t.Fatalf("%s %s consulted anchor", tc.name, ref)
			}
		}
	}
}

func TestChainIDPolicyNormalizesQuantities(t *testing.T) {
	p := ChainIDPolicy()
	first, err := p.Normalize(json.RawMessage(`"0x1"`))
	if err != nil {
		t.Fatal(err)
	}
	second, err := p.Normalize(json.RawMessage(`"0x1"`))
	if err != nil {
		t.Fatal(err)
	}
	if first != "1" || second != "1" || first != second {
		t.Fatalf("unexpected normalized IDs: %q, %q", first, second)
	}
	if _, err := p.Normalize(json.RawMessage(`"0x01"`)); err == nil {
		t.Fatal("expected non-canonical chain ID quantity to be rejected")
	}
}

func TestStrictWireScalarHelpers(t *testing.T) {
	for _, tc := range []struct {
		input string
		want  string
	}{
		{`"0x0"`, "0"},
		{`"0x1"`, "1"},
		{`"0xAB"`, "171"},
	} {
		got, err := normalizeWireQuantity(json.RawMessage(tc.input))
		if err != nil || got != tc.want {
			t.Fatalf("normalizeWireQuantity(%s) = %q, %v; want %q", tc.input, got, err, tc.want)
		}
	}
	for _, input := range []string{`"0x00"`, `"0x01"`, `"0x"`, `"0xg"`} {
		if _, err := normalizeWireQuantity(json.RawMessage(input)); err == nil {
			t.Fatalf("normalizeWireQuantity(%s) unexpectedly succeeded", input)
		}
	}
	data, err := normalizeWireData(json.RawMessage(`"0X0012AB"`))
	if err != nil || data != "0x0012ab" {
		t.Fatalf("normalizeWireData() = %q, %v", data, err)
	}
	if _, err := validateWireFixedHex(json.RawMessage(`"0x01"`), 4, "hash"); err == nil {
		t.Fatal("validateWireFixedHex accepted a short hash")
	}
}

func TestQuantityPoliciesRejectNonCanonicalWireValues(t *testing.T) {
	policies := []struct {
		name   string
		policy rpc.Policy
	}{
		{"chain id", ChainIDPolicy()},
		{"block number", BlockNumberPolicy()},
		{"blob base fee", BlobBaseFeePolicy()},
		{"gas price", GasPricePolicy()},
		{"priority fee", MaxPriorityFeePerGasPolicy()},
	}
	for _, tc := range policies {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := tc.policy.Normalize(json.RawMessage(`"0x01"`)); err == nil {
				t.Fatal("accepted non-canonical quantity")
			}
		})
	}
}

func TestTransactionAdmissionClassifiesUnsupportedTypedObject(t *testing.T) {
	policy := TransactionByHashPolicy().(rpc.AdmissionPolicy)
	_, err := policy.Admit(context.Background(), rpc.Request{
		Method: MethodGetTransactionByHash,
		Params: []any{"0x0000000000000000000000000000000000000000000000000000000000000001"},
	}, json.RawMessage(`{"type":"0x7f"}`), nil)
	if !errors.Is(err, rpc.ErrUnsupportedTypedObject) {
		t.Fatalf("error = %v, want unsupported typed object", err)
	}
	if !errors.Is(err, rpc.ErrMalformedResponse) {
		t.Fatalf("error = %v, want malformed compatibility classification", err)
	}
}

func TestBlockByHashPolicyIgnoresObjectOrderAndHexCase(t *testing.T) {
	p := BlockByHashPolicy()
	first, err := p.Normalize(json.RawMessage(`{"hash":"0xAB","number":"0x1","transactions":[]}`))
	if err != nil {
		t.Fatal(err)
	}
	second, err := p.Normalize(json.RawMessage(`{"transactions":[],"number":"0x1","hash":"0xab"}`))
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("expected canonical block values to match: %s != %s", first, second)
	}
}

func TestBlockByNumberPolicyComparesHeightsNumerically(t *testing.T) {
	p := BlockByNumberPolicy()
	first, err := p.Normalize(json.RawMessage(`{"number":"0x010f","hash":"0xAB"}`))
	if err != nil {
		t.Fatal(err)
	}
	second, err := p.Normalize(json.RawMessage(`{"number":"0x10f","hash":"0xab"}`))
	if err != nil {
		t.Fatal(err)
	}
	if first != second || first != "271|0xab" {
		t.Fatalf("expected numerically equal block heads, got %q vs %q", first, second)
	}
}

func TestTransactionReceiptPolicyIgnoresObjectOrderAndHexCase(t *testing.T) {
	p := TransactionReceiptPolicy()
	first, err := p.Normalize(json.RawMessage(`{"transactionHash":"0xAB","status":"0x1","gasUsed":"0x10","logs":[]}`))
	if err != nil {
		t.Fatal(err)
	}
	second, err := p.Normalize(json.RawMessage(`{"logs":[],"gasUsed":"0x10","status":"0x1","transactionHash":"0xab"}`))
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("expected canonical receipt values to match: %s != %s", first, second)
	}
}

func TestTransactionPoliciesRejectIncompleteObjectsBeforeClustering(t *testing.T) {
	if _, err := TransactionByHashPolicy().Normalize(json.RawMessage(`{"hash":"0x01"}`)); err == nil {
		t.Fatal("expected incomplete transaction context to be rejected")
	}
	if _, err := TransactionReceiptPolicy().Normalize(json.RawMessage(`{"transactionHash":"0x01"}`)); err == nil {
		t.Fatal("expected incomplete receipt to be rejected")
	}
}

func TestBlockReceiptsPolicyPreservesEmptyArrayAndRejectsNullElement(t *testing.T) {
	p := BlockReceiptsPolicy(nil)
	if got, err := p.Normalize(json.RawMessage(`[]`)); err != nil || got != `[]` {
		t.Fatalf("expected empty receipt list to remain valid, got %q: %v", got, err)
	}
	if _, err := p.Normalize(json.RawMessage(`[null]`)); err == nil {
		t.Fatal("expected null receipt element to be rejected")
	}
}

func TestNormalizeHexDataFoldsCaseAndAllowsEmpty(t *testing.T) {
	first, err := normalizeHexData(json.RawMessage(`"0x6080FE"`))
	if err != nil {
		t.Fatal(err)
	}
	second, err := normalizeHexData(json.RawMessage(`"0x6080fe"`))
	if err != nil {
		t.Fatal(err)
	}
	empty, err := normalizeHexData(json.RawMessage(`"0x"`))
	if err != nil {
		t.Fatal(err)
	}
	if first != second || first != "0x6080fe" || empty != "0x" {
		t.Fatalf("unexpected canonical data: %q, %q, %q", first, second, empty)
	}
	if _, err := normalizeHexData(json.RawMessage(`"6080"`)); err == nil {
		t.Fatal("expected non-0x data to be rejected")
	}
	for _, malformed := range []string{`"0x1"`, `"0xgg"`, `null`} {
		if _, err := normalizeHexData(json.RawMessage(malformed)); err == nil {
			t.Fatalf("expected malformed data %s to be rejected", malformed)
		}
	}
}

func TestNormalizeStorageWordRequiresExactly32Bytes(t *testing.T) {
	word := `"0x00000000000000000000000000000000000000000000000000000000000000AB"`
	canonical, err := normalizeStorageWord(json.RawMessage(word))
	if err != nil {
		t.Fatal(err)
	}
	if canonical != "0x00000000000000000000000000000000000000000000000000000000000000ab" {
		t.Fatalf("unexpected storage word canonical form: %q", canonical)
	}
	for _, malformed := range []string{`"0x"`, `"0x01"`, `"0x000000000000000000000000000000000000000000000000000000000000000001"`, `"0x00000000000000000000000000000000000000000000000000000000000000GG"`, `null`} {
		if _, err := normalizeStorageWord(json.RawMessage(malformed)); err == nil {
			t.Fatalf("expected malformed storage word %s to be rejected", malformed)
		}
	}
}

func TestNormalizeDecimalStringComparesNumerically(t *testing.T) {
	first, err := normalizeDecimalString(json.RawMessage(`"01"`))
	if err != nil {
		t.Fatal(err)
	}
	second, err := normalizeDecimalString(json.RawMessage(`"1"`))
	if err != nil {
		t.Fatal(err)
	}
	if first != second || first != "1" {
		t.Fatalf("expected numeric equality across spellings, got %q vs %q", first, second)
	}
	if _, err := normalizeDecimalString(json.RawMessage(`"0x1"`)); err == nil {
		t.Fatal("expected hex spelling to be rejected")
	}
}

func TestNormalizeCallOutcomeClustersResultsAndReverts(t *testing.T) {
	cases := []struct {
		name    string
		payload string
		want    string
		wantErr bool
	}{
		{"return data compares case-insensitively", `"0xDEADbeef"`, "ok|0xdeadbeef", false},
		{"empty result is valid data", `"0x"`, "ok|0x", false},
		{"revert wording collapses to one key", `{"code":-32000,"message":"execution reverted: ERC20: insufficient allowance"}`, "reverted", false},
		{"bare revert collapses to the same key", `{"code":-32000,"message":"execution reverted"}`, "reverted", false},
		{"empty revert data is still an execution observation", `{"code":-32000,"message":"","data":"0x"}`, "reverted", false},
		{"legacy parity revert code", `{"code":3,"message":"Reverted"}`, "reverted", false},
		{"geth client timeout is a fault, not an observation", `{"code":-32002,"message":"request timed out"}`, "", true},
		{"internal error is a fault", `{"code":-32603,"message":"panic"}`, "", true},
		{"unrecognized payload is a fault", `{"neither":"result nor error"}`, "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			key, err := normalizeCallOutcome(json.RawMessage(tc.payload))
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected fault, got key %q", key)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if key != tc.want {
				t.Fatalf("expected key %q, got %q", tc.want, key)
			}
		})
	}
}
