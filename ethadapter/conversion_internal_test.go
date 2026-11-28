package ethadapter

import (
	"encoding/json"
	"math/big"
	"strings"
	"testing"

	"github.com/codermaybe/ethguard/ethapi"
	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/holiman/uint256"
)

func TestSimulateInputPreservesCreationAndOptionalZero(t *testing.T) {
	opts := ethclient.SimulateOptions{BlockStateCalls: []ethclient.SimulateBlock{{Calls: []ethereum.CallMsg{{Value: new(big.Int), Data: []byte{1, 2}}}}}}
	converted, _, err := fromSimulate(opts)
	if err != nil {
		t.Fatal(err)
	}
	call := (*converted.BlockStateCalls[0].Calls)[0]
	if call.To != nil || call.Value == nil || *call.Value != "0x0" || call.Input == nil || *call.Input != "0x0102" {
		t.Fatalf("creation call changed: %+v", call)
	}
	if opts.BlockStateCalls[0].Calls[0].Value.Sign() != 0 || opts.BlockStateCalls[0].Calls[0].To != nil {
		t.Fatal("source mutated")
	}
}

func TestSetCodeAuthorizationHexByteSpelling(t *testing.T) {
	key, err := crypto.HexToECDSA("0000000000000000000000000000000000000000000000000000000000000001")
	if err != nil {
		t.Fatal(err)
	}
	auth, err := types.SignSetCode(key, types.SetCodeAuthorization{ChainID: *uint256.NewInt(1), Address: common.HexToAddress("0x1234"), Nonce: 1})
	if err != nil {
		t.Fatal(err)
	}
	tx, err := types.SignTx(types.NewTx(&types.SetCodeTx{ChainID: uint256.NewInt(1), Nonce: 1, GasTipCap: uint256.NewInt(2), GasFeeCap: uint256.NewInt(3), Gas: 100000, To: common.HexToAddress("0x4567"), Value: uint256.NewInt(0), AuthList: []types.SetCodeAuthorization{auth}}), types.LatestSignerForChainID(big.NewInt(1)), key)
	if err != nil {
		t.Fatal(err)
	}
	input, _, err := fromTransaction(tx)
	if err != nil {
		t.Fatal(err)
	}
	input.Signed7702Transaction.AuthorizationList[0].YParity = ethapi.HexByte("0x0" + string(input.Signed7702Transaction.AuthorizationList[0].YParity)[2:])
	got, _, err := ToGethTransaction(input)
	if err != nil {
		t.Fatal(err)
	}
	if got.Hash() != tx.Hash() {
		t.Fatal("signature-byte normalization changed the transaction")
	}
	input.Signed7702Transaction.Value = ethapi.Quantity("0x1" + strings.Repeat("0", 64))
	if got, _, err := ToGethTransaction(input); err == nil || got != nil {
		t.Fatal("uint256 overflow must return an error before geth's MustFromBig")
	}
}

func TestSimulationOptionsRejectNegativeOverrides(t *testing.T) {
	for _, opts := range []ethclient.SimulateOptions{
		{BlockStateCalls: []ethclient.SimulateBlock{{BlockOverrides: &ethereum.BlockOverrides{Difficulty: big.NewInt(-1)}, Calls: []ethereum.CallMsg{{}}}}},
		{BlockStateCalls: []ethclient.SimulateBlock{{StateOverrides: map[common.Address]ethereum.OverrideAccount{{}: {Balance: big.NewInt(-1)}}, Calls: []ethereum.CallMsg{{}}}}},
	} {
		if _, _, err := fromSimulate(opts); err == nil {
			t.Fatal("negative override accepted")
		}
	}
}

func TestFilterOmitsEmptyAddressAndPreservesTopicWildcard(t *testing.T) {
	value, _, err := FromGethFilterQuery(ethereum.FilterQuery{Addresses: []common.Address{}, Topics: [][]common.Hash{nil, {}}})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	if _, ok := fields["address"]; ok {
		t.Fatal("empty address filter sent")
	}
	if string(fields["topics"]) != "[null,[]]" {
		t.Fatalf("topics=%s", fields["topics"])
	}
}
