package header

import (
	"bytes"
	"encoding/json"
	"math/big"
	"strings"
	"testing"

	"github.com/codermaybe/ethguard/ethapi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
)

func testHeader(t *testing.T) json.RawMessage {
	t.Helper()
	// A PoW header may commit to uncles even when a Header response has no
	// uncle body. No body commitment check is applicable at this boundary.
	head := &types.Header{UncleHash: common.HexToHash("0x1234"), Difficulty: big.NewInt(1), Number: big.NewInt(7), TxHash: types.EmptyTxsHash, GasLimit: 100, Time: 12}
	raw, err := json.Marshal(head)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestNormalizeOriginalWireHashBeforeReencoding(t *testing.T) {
	raw := testHeader(t)
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(raw, &fields)
	fields["hash"] = json.RawMessage(`"0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"`)
	wrong, _ := json.Marshal(fields)
	// Geth parsing and re-encoding would erase precisely this false claim.
	var decoded types.Header
	if err := json.Unmarshal(wrong, &decoded); err != nil {
		t.Fatal(err)
	}
	repaired, _ := json.Marshal(&decoded)
	if bytes.Equal(wrong, repaired) {
		t.Fatal("negative fixture does not exercise overwritten hash")
	}
	if _, err := Decode(wrong); err == nil {
		t.Fatal("incorrect original wire hash accepted")
	}
	if _, err := Normalize(wrong); err == nil {
		t.Fatal("normalizer accepted overwritten wire hash")
	}
}

func TestNormalizeIndependentHeaderProjectionAndPoWUncles(t *testing.T) {
	raw := testHeader(t)
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(raw, &fields)
	fields["size"] = json.RawMessage(`"0x100"`)
	fields["transactions"] = json.RawMessage(`[]`)
	fields["uncles"] = json.RawMessage(`[]`)
	raw, _ = json.Marshal(fields)
	canonical, err := Normalize(raw)
	if err != nil {
		t.Fatal(err)
	}
	out, err := ethapi.NewHeader(canonical)
	if err != nil {
		t.Fatal(err)
	}
	if out.Sha3uncles != ethapi.Hash(common.HexToHash("0x1234").Hex()) {
		t.Fatal("PoW uncle root lost")
	}
	for _, name := range []string{"size", "transactions", "uncles"} {
		if bytes.Contains(canonical, []byte(`"`+name+`"`)) {
			t.Fatalf("body %s entered Header", name)
		}
	}
	again, err := Normalize(canonical)
	if err != nil || !bytes.Equal(canonical, again) {
		t.Fatalf("Header not canonical: %s, %v", again, err)
	}
}

func TestNormalizeRejectsMinimalReferenceAndMissingDifficulty(t *testing.T) {
	if _, err := Normalize(json.RawMessage(`{"hash":"0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","number":"0x1"}`)); err == nil {
		t.Fatal("minimal reference accepted as Header")
	}
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(testHeader(t), &fields)
	delete(fields, "difficulty")
	raw, _ := json.Marshal(fields)
	if _, err := Normalize(raw); err == nil {
		t.Fatal("missing Header difficulty accepted")
	}
}

func TestNormalizeOptionalNullPolicyDoesNotHideMalformedValues(t *testing.T) {
	raw := testHeader(t) // Actual geth MarshalJSON includes all eight nulls.
	canonical, err := Normalize(raw)
	if err != nil {
		t.Fatalf("native pre-fork geth header rejected: %v", err)
	}
	var canonicalFields map[string]json.RawMessage
	_ = json.Unmarshal(canonical, &canonicalFields)
	for _, field := range []string{"baseFeePerGas", "withdrawalsRoot", "blobGasUsed", "excessBlobGas", "parentBeaconBlockRoot", "requestsHash", "blockAccessListHash", "slotNumber"} {
		if _, exists := canonicalFields[field]; exists {
			t.Fatalf("unset optional %s was retained", field)
		}
		t.Run(field, func(t *testing.T) {
			var fields map[string]json.RawMessage
			_ = json.Unmarshal(raw, &fields)
			fields[field] = json.RawMessage(`"garbage"`)
			malformed, _ := json.Marshal(fields)
			if _, err := Normalize(malformed); err == nil {
				t.Fatal("malformed optional value accepted")
			}
		})
	}
	for _, field := range []string{"difficulty", "hash", "number", "gasLimit", "extraData", "logsBloom"} {
		t.Run("required null "+field, func(t *testing.T) {
			var fields map[string]json.RawMessage
			_ = json.Unmarshal(raw, &fields)
			fields[field] = json.RawMessage(`null`)
			malformed, _ := json.Marshal(fields)
			if _, err := Normalize(malformed); err == nil {
				t.Fatal("required null accepted")
			}
		})
	}
}

func TestNormalizeJSONEscapesAndCanonicalScalarsBeforeGethBridge(t *testing.T) {
	raw := testHeader(t)
	baseline, err := Normalize(raw)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(raw, &fields)
	for name, value := range map[string]string{"number": "0X7", "difficulty": "0X1", "gasLimit": "0X64", "gasUsed": "0X0", "timestamp": "0Xc"} {
		fields[name], _ = json.Marshal(value)
	}
	noncanonical, _ := json.Marshal(fields)
	fields["difficulty"] = json.RawMessage(`"0x01"`)
	leadingZero, _ := json.Marshal(fields)
	if _, err := Normalize(leadingZero); err == nil {
		t.Fatal("generated quantity leading-zero policy was widened")
	}
	variants := []struct {
		name string
		raw  json.RawMessage
	}{
		{"escaped hexadecimal strings", json.RawMessage(strings.ReplaceAll(string(raw), `"0x`, `"\u0030x`))},
		{"source case quantities", noncanonical},
	}
	for _, tc := range variants {
		t.Run(tc.name, func(t *testing.T) {
			canonical, err := Normalize(tc.raw)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(canonical, baseline) {
				t.Fatalf("equivalent wire representation changed Header: %s", canonical)
			}
			decoded, err := Decode(tc.raw)
			if err != nil || decoded.Hash().Hex() != string(mustHeader(t, baseline).Hash) {
				t.Fatalf("private decode rejected equivalent source: %v", err)
			}
			var forged map[string]json.RawMessage
			_ = json.Unmarshal(tc.raw, &forged)
			forged["hash"] = json.RawMessage(`"0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"`)
			wrong, _ := json.Marshal(forged)
			wrong = json.RawMessage(strings.ReplaceAll(string(wrong), `"0x`, `"\u0030x`))
			if _, err := Normalize(wrong); err == nil || !strings.Contains(err.Error(), "wire hash disagrees") {
				t.Fatalf("canonicalization hid incorrect original hash: %v", err)
			}
		})
	}
}

func mustHeader(t *testing.T, raw json.RawMessage) ethapi.Header {
	t.Helper()
	value, err := ethapi.NewHeader(raw)
	if err != nil {
		t.Fatal(err)
	}
	return value
}
