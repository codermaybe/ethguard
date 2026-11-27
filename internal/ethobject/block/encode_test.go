package block

import (
	"bytes"
	"encoding/json"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
)

func TestEncodeKeepsOfficialSizeAndDropsProviderFields(t *testing.T) {
	header := &types.Header{
		ParentHash:  common.HexToHash("0x1"),
		UncleHash:   types.EmptyUncleHash,
		Root:        common.HexToHash("0x2"),
		TxHash:      types.EmptyTxsHash,
		ReceiptHash: common.HexToHash("0x3"),
		Difficulty:  new(big.Int),
		Number:      big.NewInt(1),
		GasLimit:    1,
		Time:        1,
	}
	headerRaw, err := marshalTestHeader(t, header)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(headerRaw, &fields); err != nil {
		t.Fatal(err)
	}
	fields["transactions"] = json.RawMessage(`[]`)
	fields["uncles"] = json.RawMessage(`[]`)
	fields["size"] = json.RawMessage(`"0x100"`)
	fields["totalDifficulty"] = json.RawMessage(`"0x200"`)
	fields["from"] = json.RawMessage(`"0x0000000000000000000000000000000000000001"`)
	fields["vendor"] = json.RawMessage(`{"extra":true}`)
	raw, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := Encode(raw)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"totalDifficulty", "from", "vendor"} {
		if bytes.Contains(encoded, []byte(`"`+field+`"`)) {
			t.Fatalf("uncommitted field %s survived: %s", field, encoded)
		}
	}
	if !bytes.Contains(encoded, []byte(`"size":"0x100"`)) {
		t.Fatalf("official size lost: %s", encoded)
	}
	if !bytes.Contains(encoded, []byte(`"transactions":[]`)) || !bytes.Contains(encoded, []byte(`"uncles":[]`)) {
		t.Fatalf("committed empty bodies missing: %s", encoded)
	}
}

func TestEncodeKeepsCommittedEmptyWithdrawals(t *testing.T) {
	withdrawalsHash := types.EmptyWithdrawalsHash
	header := &types.Header{
		ParentHash:      common.HexToHash("0x1"),
		UncleHash:       types.EmptyUncleHash,
		Root:            common.HexToHash("0x2"),
		TxHash:          types.EmptyTxsHash,
		ReceiptHash:     common.HexToHash("0x3"),
		Difficulty:      new(big.Int),
		Number:          big.NewInt(1),
		GasLimit:        1,
		Time:            1,
		WithdrawalsHash: &withdrawalsHash,
	}
	headerRaw, err := marshalTestHeader(t, header)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(headerRaw, &fields); err != nil {
		t.Fatal(err)
	}
	fields["withdrawals"] = json.RawMessage(`[]`)
	fields["transactions"] = json.RawMessage(`[]`)
	fields["uncles"] = json.RawMessage(`[]`)
	fields["size"] = json.RawMessage(`"0x100"`)
	raw, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := Encode(raw)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(encoded, []byte(`"withdrawals":[]`)) {
		t.Fatalf("committed withdrawals missing: %s", encoded)
	}
}

// TestEncodeOmitsUnsetOptionalHeaderMembers pins the null→omit fold: a
// pre-fork block served with geth's unset-pointer nulls commits those
// members omitted, keeping the value a legal subset of the official schema
// instead of declaring non-nullable optional fields null.
func TestEncodeOmitsUnsetOptionalHeaderMembers(t *testing.T) {
	header := &types.Header{
		ParentHash:  common.HexToHash("0x1"),
		UncleHash:   types.EmptyUncleHash,
		Root:        common.HexToHash("0x2"),
		TxHash:      types.EmptyTxsHash,
		ReceiptHash: common.HexToHash("0x3"),
		Difficulty:  new(big.Int),
		Number:      big.NewInt(1),
		GasLimit:    1,
		Time:        1,
	}
	headerRaw, err := marshalTestHeader(t, header)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := Encode(headerRaw)
	if err != nil {
		t.Fatal(err)
	}
	for _, member := range []string{"baseFeePerGas", "withdrawalsRoot", "blobGasUsed", "excessBlobGas", "parentBeaconBlockRoot", "requestsHash", "blockAccessListHash", "slotNumber"} {
		if bytes.Contains(encoded, []byte(`"`+member+`"`)) {
			t.Fatalf("unset optional member %s committed: %s", member, encoded)
		}
	}
}

func TestEncodeCanonicalizesHeaderOnlyAnchor(t *testing.T) {
	raw := json.RawMessage(`{"number":"0x10","hash":"0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}`)
	encoded, err := Encode(raw)
	if err != nil {
		t.Fatal(err)
	}
	if string(encoded) != `{"hash":"0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","number":"0x10"}` {
		t.Fatalf("encoded = %s, want canonical head", encoded)
	}
}

func TestEncodeSlotNumberPreservesCommittedValueAndOmitsUnsetNull(t *testing.T) {
	slot := uint64(7)
	header := &types.Header{ParentHash: common.HexToHash("0x1"), UncleHash: types.EmptyUncleHash, Root: common.HexToHash("0x2"), TxHash: types.EmptyTxsHash, ReceiptHash: common.HexToHash("0x3"), Difficulty: new(big.Int), Number: big.NewInt(1), GasLimit: 1, Time: 1, SlotNumber: &slot}
	originalHash := header.Hash()
	raw, err := marshalTestHeader(t, header)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := Encode(raw)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &fields); err != nil {
		t.Fatal(err)
	}
	if string(fields["slotNumber"]) != `"0x7"` {
		t.Fatalf("committed slot lost: %s", encoded)
	}
	var roundTrip types.Header
	if err := json.Unmarshal(encoded, &roundTrip); err != nil {
		t.Fatal(err)
	}
	if roundTrip.Hash() != originalHash {
		t.Fatal("canonical slot encoding changed header commitment")
	}
	slot++
	if header.Hash() == originalHash {
		t.Fatal("slot number does not participate in the header commitment")
	}
	header.SlotNumber = nil
	raw, err = marshalTestHeader(t, header)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err = Encode(raw)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(encoded, []byte(`"slotNumber"`)) {
		t.Fatalf("unset slot survived as null: %s", encoded)
	}
}

func marshalTestHeader(t *testing.T, head *types.Header) (json.RawMessage, error) {
	t.Helper()
	return json.Marshal(head)
}
