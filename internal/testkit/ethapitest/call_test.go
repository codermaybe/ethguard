package ethapitest

import (
	"encoding/json"
	ethereum "github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"testing"
)

func TestHistoricalCallFixtureRetainsDefaultAndExplicitFields(t *testing.T) {
	to := common.HexToAddress("0x1")
	value := Call(ethereum.CallMsg{To: &to, Data: []byte{0, 1}, Gas: 21000})
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got["to"] != to.Hex() || got["from"] != (common.Address{}).Hex() || got["input"] != "0x0001" || got["gas"] != "0x5208" {
		t.Fatalf("fixture changed: %s", raw)
	}
}
