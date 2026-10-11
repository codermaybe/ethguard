package block

import (
	"bytes"
	"encoding/json"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
)

func TestEncodeDropsUncommittedBlockFields(t *testing.T) {
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
	headerRaw, err := json.Marshal(header)
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
	for _, field := range []string{"size", "totalDifficulty", "from", "vendor"} {
		if bytes.Contains(encoded, []byte(`"`+field+`"`)) {
			t.Fatalf("uncommitted field %s survived: %s", field, encoded)
		}
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
	headerRaw, err := json.Marshal(header)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(headerRaw, &fields); err != nil {
		t.Fatal(err)
	}
	fields["withdrawals"] = json.RawMessage(`[]`)
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
