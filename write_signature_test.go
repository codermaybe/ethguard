package ethguard

import (
	"context"
	"encoding/json"
	"errors"
	"math/big"
	"testing"

	"github.com/codermaybe/ethguard/internal/methods"
	"github.com/codermaybe/ethguard/rpc"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
)

func writeSignatureTransactions() map[string]*types.Transaction {
	to := common.HexToAddress("0x1111111111111111111111111111111111111111")
	return map[string]*types.Transaction{
		"legacy": types.NewTx(&types.LegacyTx{Nonce: 7, To: &to, Gas: 21000, GasPrice: big.NewInt(1), Value: big.NewInt(1)}),
		// A different chain ID is deliberate: local signing checks make no claim
		// about the configured provider chain or transaction executability.
		"typed":      types.NewTx(&types.DynamicFeeTx{ChainID: big.NewInt(777), Nonce: 7, To: &to, Gas: 21000, GasTipCap: big.NewInt(1), GasFeeCap: big.NewInt(2), Value: big.NewInt(1)}),
		"typed zero": types.NewTx(&types.DynamicFeeTx{ChainID: new(big.Int), Nonce: 7, To: &to, Gas: 21000, GasTipCap: big.NewInt(1), GasFeeCap: big.NewInt(2), Value: big.NewInt(1)}),
	}
}

func TestWriteSignatureRejectsEncodableUnsignedBeforeBusinessNetwork(t *testing.T) {
	for name, tx := range writeSignatureTransactions() {
		t.Run(name, func(t *testing.T) {
			raw, err := tx.MarshalBinary()
			if err != nil {
				t.Fatal(err)
			}
			var decoded types.Transaction
			if err := decoded.UnmarshalBinary(raw); err != nil {
				t.Fatalf("fixture is not structurally valid: %v", err)
			}
			_, r, s := decoded.RawSignatureValues()
			if r.Sign() != 0 || s.Sign() != 0 {
				t.Fatal("fixture unexpectedly signed")
			}
			for _, entry := range []string{"raw", "raw sync", "typed", "typed sync", "view typed", "view typed sync"} {
				t.Run(entry, func(t *testing.T) {
					client, nodes := dialFleet(t, 2)
					var result CallResult
					var err error
					switch entry {
					case "raw":
						result, err = client.SendRawTransaction(context.Background(), raw)
					case "raw sync":
						_, result, err = client.SendRawTransactionSync(context.Background(), raw, nil)
					case "typed":
						result, err = client.SendTransaction(context.Background(), tx)
					case "typed sync":
						_, result, err = client.SendTransactionSync(context.Background(), tx, nil)
					case "view typed":
						err = client.EthClient().SendTransaction(context.Background(), tx)
					case "view typed sync":
						_, err = client.EthClient().SendTransactionSync(context.Background(), tx, nil)
					}
					if !errors.Is(err, rpc.ErrInvalidRequest) {
						t.Fatalf("error=%v, want local ErrInvalidRequest", err)
					}
					if result.Decision != nil || result.WriteDecision != nil {
						t.Fatalf("local rejection carried decision: %+v", result)
					}
					for i, node := range nodes {
						for _, call := range node.Calls() {
							if call.Method == methods.MethodSendRawTransaction || call.Method == methods.MethodGetTransactionReceipt {
								t.Fatalf("source %d received business call %s", i, call.Method)
							}
						}
					}
				})
			}
		})
	}
}

func TestWriteSignatureAcceptsSignedLegacyAndTypedPayloads(t *testing.T) {
	key, err := crypto.HexToECDSA("deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef")
	if err != nil {
		t.Fatal(err)
	}
	txs := writeSignatureTransactions()
	txs["protected legacy"] = txs["legacy"]
	txs["protected legacy zero"] = txs["legacy"]

	for name, tx := range txs {
		t.Run(name, func(t *testing.T) {
			var signer types.Signer = types.HomesteadSigner{}
			if name == "protected legacy" {
				signer = types.LatestSignerForChainID(big.NewInt(1))
			} else if name == "typed" {
				signer = types.LatestSignerForChainID(tx.ChainId())
			}
			if name == "protected legacy zero" {
				signer = types.NewEIP155Signer(new(big.Int))
			}
			var signed *types.Transaction
			if name == "typed zero" {
				signingHash := types.NewEIP155Signer(new(big.Int)).Hash(tx)
				signature, signErr := crypto.Sign(signingHash[:], key)
				if signErr != nil {
					t.Fatal(signErr)
				}
				signed = types.NewTx(&types.DynamicFeeTx{ChainID: new(big.Int), Nonce: tx.Nonce(), To: tx.To(), Gas: tx.Gas(), GasTipCap: tx.GasTipCap(), GasFeeCap: tx.GasFeeCap(), Value: tx.Value(), V: new(big.Int).SetUint64(uint64(signature[64])), R: new(big.Int).SetBytes(signature[:32]), S: new(big.Int).SetBytes(signature[32:64])})
			} else {
				var signErr error
				signed, signErr = types.SignTx(tx, signer, key)
				if signErr != nil {
					t.Fatal(signErr)
				}
			}
			raw, err := signed.MarshalBinary()
			if err != nil {
				t.Fatal(err)
			}
			client, nodes := dialFleet(t, 2)
			for _, node := range nodes {
				node.Return(methods.MethodSendRawTransaction, signed.Hash().Hex())
			}
			result, err := client.SendRawTransaction(context.Background(), raw)
			if err != nil {
				t.Fatalf("valid signed transaction rejected: %v", err)
			}
			if result.WriteDecision == nil || result.WriteDecision.Status != rpc.WriteAccepted || result.WriteDecision.Hash != signed.Hash().Hex() {
				t.Fatalf("valid write was not accepted: %+v", result)
			}
			for i, node := range nodes {
				sends := 0
				for _, call := range node.Calls() {
					if call.Method != methods.MethodSendRawTransaction {
						continue
					}
					sends++
					var wire string
					if len(call.Params) != 1 {
						t.Fatalf("source %d params=%v", i, call.Params)
					}
					if err := json.Unmarshal(call.Params[0], &wire); err != nil {
						t.Fatal(err)
					}
					if wire != hexutil.Encode(raw) {
						t.Fatalf("source %d received different payload %s", i, wire)
					}
				}
				if sends != 1 {
					t.Fatalf("source %d writes=%d, want 1", i, sends)
				}
			}
		})
	}
}
