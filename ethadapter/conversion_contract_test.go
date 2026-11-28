package ethadapter_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"math/big"
	"reflect"
	"strings"
	"testing"

	"github.com/codermaybe/ethguard/ethadapter"
	"github.com/codermaybe/ethguard/ethapi"
	ethereum "github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/trie"
	"github.com/holiman/uint256"
)

func contractJSON(t *testing.T, value any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func contractFields(t *testing.T, value any) map[string]json.RawMessage {
	t.Helper()
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(contractJSON(t, value), &fields); err != nil {
		t.Fatal(err)
	}
	return fields
}

func contractBaseHeader() *types.Header {
	return &types.Header{
		ParentHash: common.HexToHash("0x11"), UncleHash: types.EmptyUncleHash,
		Coinbase: common.HexToAddress("0x22"), Root: common.HexToHash("0x33"),
		TxHash: types.EmptyTxsHash, ReceiptHash: types.EmptyReceiptsHash,
		Difficulty: big.NewInt(1), Number: big.NewInt(17), GasLimit: 30_000_000,
		Time: 123, Extra: []byte{1, 2, 3}, MixDigest: common.HexToHash("0x44"),
	}
}

func contractHeaderFields(t *testing.T, h *types.Header) map[string]json.RawMessage {
	t.Helper()
	fields := contractFields(t, h)
	for key, value := range fields {
		if string(value) == "null" {
			delete(fields, key)
		}
	}
	return fields
}

func contractHeader(t *testing.T, h *types.Header) *ethapi.Header {
	t.Helper()
	out, err := ethapi.NewHeader(contractJSON(t, contractHeaderFields(t, h)))
	if err != nil {
		t.Fatal(err)
	}
	return &out
}

func contractSignedTransactions(t *testing.T) []*types.Transaction {
	t.Helper()
	key, err := crypto.HexToECDSA(strings.Repeat("01", 32))
	if err != nil {
		t.Fatal(err)
	}
	to := common.HexToAddress("0x99")
	large := new(big.Int).Lsh(big.NewInt(1), 80)
	access := types.AccessList{{Address: to, StorageKeys: []common.Hash{common.HexToHash("0x77")}}}
	auth, err := types.SignSetCode(key, types.SetCodeAuthorization{ChainID: *uint256.NewInt(1), Address: to, Nonce: 2})
	if err != nil {
		t.Fatal(err)
	}
	blobHash := common.HexToHash("0x88")
	blobHash[0] = 1
	inners := []types.TxData{
		&types.LegacyTx{Nonce: 1, Gas: 21000, GasPrice: large, To: &to, Value: large, Data: []byte{1}},
		&types.AccessListTx{ChainID: big.NewInt(1), Nonce: 2, Gas: 30000, GasPrice: large, To: &to, Value: large, Data: []byte{2}, AccessList: access},
		&types.DynamicFeeTx{ChainID: big.NewInt(1), Nonce: 3, Gas: 30000, GasFeeCap: large, GasTipCap: big.NewInt(2), To: &to, Value: large, Data: []byte{3}, AccessList: access},
		&types.BlobTx{ChainID: uint256.NewInt(1), Nonce: 4, Gas: 30000, GasFeeCap: uint256.MustFromBig(large), GasTipCap: uint256.NewInt(2), To: to, Value: uint256.MustFromBig(large), Data: []byte{4}, AccessList: access, BlobFeeCap: uint256.NewInt(3), BlobHashes: []common.Hash{blobHash}},
		&types.SetCodeTx{ChainID: uint256.NewInt(1), Nonce: 5, Gas: 50000, GasFeeCap: uint256.MustFromBig(large), GasTipCap: uint256.NewInt(2), To: to, Value: uint256.MustFromBig(large), Data: []byte{5}, AccessList: access, AuthList: []types.SetCodeAuthorization{auth}},
	}
	out := make([]*types.Transaction, len(inners))
	for i, inner := range inners {
		out[i], err = types.SignTx(types.NewTx(inner), types.LatestSignerForChainID(big.NewInt(1)), key)
		if err != nil {
			t.Fatal(err)
		}
	}
	return out
}

func contractTransactionFields(t *testing.T, tx *types.Transaction) map[string]json.RawMessage {
	t.Helper()
	fields := contractFields(t, tx)
	// A mined RPC transaction supplies gasPrice even when the signed payload
	// carries fee caps. Transaction.MarshalJSON alone has no mined context.
	fields["gasPrice"] = contractJSON(t, hexutil.EncodeBig(tx.GasPrice()))
	fields["from"] = contractJSON(t, common.HexToAddress("0x1a642f0E3c3aF545E7AcBD38b07251B3990914F1"))
	fields["blockHash"] = contractJSON(t, common.HexToHash("0xab"))
	fields["blockNumber"] = contractJSON(t, "0x11")
	fields["transactionIndex"] = contractJSON(t, "0x0")
	fields["blockTimestamp"] = contractJSON(t, "0x7b")
	return fields
}

func contractTransaction(t *testing.T, tx *types.Transaction) *ethapi.Transaction {
	t.Helper()
	out, err := ethapi.NewTransaction(contractJSON(t, contractTransactionFields(t, tx)))
	if err != nil {
		t.Fatal(err)
	}
	return &out
}

func requireConversionError(t *testing.T, err error) {
	t.Helper()
	var conversion *ethadapter.ConversionError
	if !errors.As(err, &conversion) || conversion.Type == "" || conversion.Field == "" {
		t.Fatalf("wanted a located ConversionError, got %T %v", err, err)
	}
}

func TestIndependentHeaderConversionPreservesEveryLockedField(t *testing.T) {
	full := contractBaseHeader()
	full.BaseFee = new(big.Int).Lsh(big.NewInt(1), 90)
	withdrawals, beacon, requests, access := common.HexToHash("0x55"), common.HexToHash("0x66"), common.HexToHash("0x77"), common.HexToHash("0x88")
	used, excess, slot := uint64(131072), uint64(262144), uint64(123456789)
	full.WithdrawalsHash, full.ParentBeaconRoot, full.RequestsHash, full.BlockAccessListHash = &withdrawals, &beacon, &requests, &access
	full.BlobGasUsed, full.ExcessBlobGas, full.SlotNumber = &used, &excess, &slot
	for _, test := range []struct {
		name string
		head *types.Header
	}{{"all_optional_absent", contractBaseHeader()}, {"all_optional_present", full}} {
		t.Run(test.name, func(t *testing.T) {
			input := contractHeader(t, test.head)
			before := contractJSON(t, input)
			got, _, err := ethadapter.ToGethHeader(input)
			if err != nil {
				t.Fatal(err)
			}
			if got.Hash() != test.head.Hash() || !reflect.DeepEqual(got, test.head) {
				t.Fatalf("header field or hash changed: got %#v, want %#v", got, test.head)
			}
			got.Extra[0] = 0xff
			got.Number.SetUint64(99)
			if got.BaseFee != nil {
				got.BaseFee.SetInt64(0)
			}
			if !bytes.Equal(before, contractJSON(t, input)) {
				t.Fatal("output mutation changed the source header")
			}
		})
	}
}

func TestIndependentHeaderConversionRejectsMalformedAndNarrowOverflow(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*ethapi.Header)
	}{
		{"hash_mismatch", func(h *ethapi.Header) { h.Hash = ethapi.Hash(common.HexToHash("0xdead").Hex()) }},
		{"short_hash", func(h *ethapi.Header) { h.ParentHash = "0x01" }},
		{"short_address", func(h *ethapi.Header) { h.Miner = "0x01" }},
		{"short_bloom", func(h *ethapi.Header) { h.LogsBloom = "0x00" }},
		{"gas_overflow", func(h *ethapi.Header) { h.GasLimit = "0x10000000000000000" }},
		{"negative_number", func(h *ethapi.Header) { h.Number = "-0x1" }},
		{"slot_overflow", func(h *ethapi.Header) { q := ethapi.Quantity64("0x10000000000000000"); h.SlotNumber = &q }},
	} {
		t.Run(test.name, func(t *testing.T) {
			input := contractHeader(t, contractBaseHeader())
			test.mutate(input)
			got, _, err := ethadapter.ToGethHeader(input)
			requireConversionError(t, err)
			if got != nil {
				t.Fatal("malformed header produced a partial result")
			}
		})
	}
}

func TestIndependentFiveSignedTransactionConversions(t *testing.T) {
	for _, tx := range contractSignedTransactions(t) {
		t.Run(hexutil.EncodeUint64(uint64(tx.Type())), func(t *testing.T) {
			input := contractTransaction(t, tx)
			before := contractJSON(t, input)
			got, report, err := ethadapter.ToGethTransaction(input)
			if err != nil {
				t.Fatal(err)
			}
			wantBytes, err := tx.MarshalBinary()
			if err != nil {
				t.Fatal(err)
			}
			gotBytes, err := got.MarshalBinary()
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(gotBytes, wantBytes) || got.Hash() != tx.Hash() || got.Value().Cmp(tx.Value()) != 0 {
				t.Fatal("signed payload, arbitrary precision value, or transaction hash changed")
			}
			if len(report.Omissions) == 0 {
				t.Fatal("mined transaction metadata was silently omitted")
			}
			if tx.Type() == types.BlobTxType && (got.BlobTxSidecar() != nil || !reflect.DeepEqual(got.BlobHashes(), tx.BlobHashes())) {
				t.Fatal("RPC transaction must retain versioned hashes without inventing a sidecar")
			}
			if tx.Type() == types.SetCodeTxType && !reflect.DeepEqual(got.SetCodeAuthorizations(), tx.SetCodeAuthorizations()) {
				t.Fatal("authorization signatures changed")
			}
			if err := got.UnmarshalBinary(wantBytes); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(before, contractJSON(t, input)) {
				t.Fatal("output mutation changed transaction input")
			}
		})
	}
}

func TestIndependentTransactionConversionRejectsInvalidSelfConsistentSignature(t *testing.T) {
	to := common.HexToAddress("0x99")
	unsigned := types.NewTx(&types.LegacyTx{Gas: 21000, GasPrice: big.NewInt(1), To: &to, Value: big.NewInt(1), V: big.NewInt(27), R: new(big.Int), S: new(big.Int)})
	input := contractTransaction(t, unsigned)
	if string(input.SignedLegacyTransaction.Hash) != unsigned.Hash().Hex() {
		t.Fatal("fixture hash must match the invalid signed payload")
	}
	_, _, err := ethadapter.ToGethTransaction(input)
	requireConversionError(t, err)
	valid := contractTransaction(t, contractSignedTransactions(t)[0])
	valid.SignedLegacyTransaction.Type = "0x7f"
	_, _, err = ethadapter.ToGethTransaction(valid)
	requireConversionError(t, err)
	valid = contractTransaction(t, contractSignedTransactions(t)[0])
	valid.SignedLegacyTransaction.Hash = ethapi.Hash(common.HexToHash("0xdead").Hex())
	_, _, err = ethadapter.ToGethTransaction(valid)
	requireConversionError(t, err)
}

func TestIndependentLegacyTransactionAcceptsArbitraryPrecisionChainID(t *testing.T) {
	key, err := crypto.HexToECDSA(strings.Repeat("01", 32))
	if err != nil {
		t.Fatal(err)
	}
	chainID := new(big.Int).Lsh(big.NewInt(1), 90)
	to := common.HexToAddress("0x99")
	tx, err := types.SignTx(types.NewTx(&types.LegacyTx{Gas: 21000, GasPrice: big.NewInt(1), To: &to, Value: big.NewInt(1)}), types.NewEIP155Signer(chainID), key)
	if err != nil {
		t.Fatal(err)
	}
	got, _, err := ethadapter.ToGethTransaction(contractTransaction(t, tx))
	if err != nil || got.Hash() != tx.Hash() || got.ChainId().Cmp(chainID) != 0 {
		t.Fatalf("valid EIP-155 chain ID was narrowed: tx=%v err=%v", got, err)
	}
}

func contractBlock(t *testing.T, head *types.Header, txs []*types.Transaction, uncles []*types.Header, withdrawals types.Withdrawals) *ethapi.Block {
	t.Helper()
	fields := contractHeaderFields(t, head)
	rows := make([]map[string]json.RawMessage, len(txs))
	for i, tx := range txs {
		rows[i] = contractTransactionFields(t, tx)
		rows[i]["blockHash"] = contractJSON(t, head.Hash())
		rows[i]["transactionIndex"] = contractJSON(t, hexutil.EncodeUint64(uint64(i)))
	}
	fields["transactions"] = contractJSON(t, rows)
	uncleHashes := make([]common.Hash, len(uncles))
	for i, uncle := range uncles {
		uncleHashes[i] = uncle.Hash()
	}
	fields["uncles"] = contractJSON(t, uncleHashes)
	fields["size"] = contractJSON(t, "0x100")
	if withdrawals != nil {
		fields["withdrawals"] = contractJSON(t, withdrawals)
	}
	out, err := ethapi.NewBlock(contractJSON(t, fields))
	if err != nil {
		t.Fatal(err)
	}
	return &out
}

func TestIndependentBlockConversionVerifiesAllContentCommitments(t *testing.T) {
	txs := contractSignedTransactions(t)
	uncle := contractBaseHeader()
	uncle.Number, uncle.Time = big.NewInt(16), 122
	head := contractBaseHeader()
	head.TxHash = types.DeriveSha(types.Transactions{txs[0]}, trie.NewStackTrie(nil))
	head.UncleHash = types.CalcUncleHash([]*types.Header{uncle})
	withdrawals := types.Withdrawals{{Index: 1, Validator: 2, Address: common.HexToAddress("0x33"), Amount: 3}}
	root := types.DeriveSha(withdrawals, trie.NewStackTrie(nil))
	head.BaseFee, head.WithdrawalsHash = big.NewInt(1), &root
	valid := contractBlock(t, head, txs[:1], []*types.Header{uncle}, withdrawals)
	before := contractJSON(t, valid)
	got, _, err := ethadapter.ToGethBlock(valid, []*ethapi.Header{contractHeader(t, uncle)})
	if err != nil {
		t.Fatal(err)
	}
	if got.Hash() != head.Hash() || len(got.Uncles()) != 1 || got.Uncles()[0].Hash() != uncle.Hash() || len(got.Withdrawals()) != 1 || got.Transactions()[0].Hash() != txs[0].Hash() {
		t.Fatal("complete body was not retained")
	}
	got.Withdrawals()[0].Amount = 999
	if !bytes.Equal(before, contractJSON(t, valid)) {
		t.Fatal("converted body aliases input")
	}
	for _, test := range []struct {
		name   string
		block  *ethapi.Block
		uncles []*ethapi.Header
	}{
		{"missing_uncles", valid, nil},
		{"wrong_uncle", valid, []*ethapi.Header{contractHeader(t, contractBaseHeader())}},
		{"transaction_root", contractBlock(t, head, txs[1:2], []*types.Header{uncle}, withdrawals), []*ethapi.Header{contractHeader(t, uncle)}},
		{"withdrawals_root", contractBlock(t, head, txs[:1], []*types.Header{uncle}, types.Withdrawals{{Index: 1, Validator: 2, Address: withdrawals[0].Address, Amount: 999}}), []*ethapi.Header{contractHeader(t, uncle)}},
	} {
		t.Run(test.name, func(t *testing.T) {
			out, _, err := ethadapter.ToGethBlock(test.block, test.uncles)
			requireConversionError(t, err)
			if out != nil {
				t.Fatal("invalid content commitment produced a block")
			}
		})
	}
	hashOnly := contractBlock(t, head, txs[:1], []*types.Header{uncle}, withdrawals)
	hashes := []ethapi.Hash{ethapi.Hash(txs[0].Hash().Hex())}
	hashOnly.Transactions = ethapi.BlockTransactions{TransactionHashes: &hashes}
	_, _, err = ethadapter.ToGethBlock(hashOnly, []*ethapi.Header{contractHeader(t, uncle)})
	requireConversionError(t, err)
}

func TestIndependentCallMessagePresenceAndOwnership(t *testing.T) {
	zero, report, err := ethadapter.FromGethCallMsg(ethereum.CallMsg{})
	if err != nil {
		t.Fatal(err)
	}
	if zero.Gas != nil || zero.Value != nil || zero.GasPrice != nil || zero.Input != nil || zero.AccessList != nil || zero.BlobVersionedHashes != nil || zero.AuthorizationList != nil || len(report.Omissions) == 0 {
		t.Fatalf("zero/nil presence changed or gas zero ambiguity unreported: %#v, %#v", zero, report)
	}
	tx := contractSignedTransactions(t)[4]
	to := common.HexToAddress("0x99")
	msg := ethereum.CallMsg{From: common.HexToAddress("0x11"), To: &to, Gas: 100, GasPrice: big.NewInt(0), Value: big.NewInt(0), GasFeeCap: new(big.Int).Lsh(big.NewInt(1), 80), GasTipCap: big.NewInt(2), Data: []byte{1, 2}, AccessList: tx.AccessList(), BlobGasFeeCap: big.NewInt(3), BlobHashes: []common.Hash{common.HexToHash("0x1234")}, AuthorizationList: tx.SetCodeAuthorizations()}
	got, _, err := ethadapter.FromGethCallMsg(msg)
	if err != nil {
		t.Fatal(err)
	}
	if got.Input == nil || string(*got.Input) != "0x0102" || got.Value == nil || string(*got.Value) != "0x0" || got.GasPrice == nil || string(*got.GasPrice) != "0x0" || got.MaxFeePerGas == nil || string(*got.MaxFeePerGas) != "0x100000000000000000000" || got.AccessList == nil || got.BlobVersionedHashes == nil || got.AuthorizationList == nil {
		t.Fatalf("call message lost data or field presence: %#v", got)
	}
	before := contractJSON(t, got)
	msg.Data[0] = 9
	msg.Value.SetInt64(9)
	msg.AccessList[0].StorageKeys[0][0] = 9
	msg.AuthorizationList[0].Nonce = 9
	if !bytes.Equal(before, contractJSON(t, got)) {
		t.Fatal("converted call borrows mutable geth input")
	}
	for _, test := range []struct {
		name string
		msg  ethereum.CallMsg
	}{
		{"value", ethereum.CallMsg{Value: big.NewInt(-1)}},
		{"gas_price", ethereum.CallMsg{GasPrice: big.NewInt(-1)}},
		{"fee_cap", ethereum.CallMsg{GasFeeCap: big.NewInt(-1)}},
		{"tip_cap", ethereum.CallMsg{GasTipCap: big.NewInt(-1)}},
		{"blob_fee_cap", ethereum.CallMsg{BlobGasFeeCap: big.NewInt(-1)}},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, _, err := ethadapter.FromGethCallMsg(test.msg)
			requireConversionError(t, err)
		})
	}
}

func TestIndependentFilterQueryPreservesWildcardsAndRejectsConflictingReference(t *testing.T) {
	for _, addresses := range [][]common.Address{nil, {}} {
		empty, _, err := ethadapter.FromGethFilterQuery(ethereum.FilterQuery{Addresses: addresses})
		if err != nil {
			t.Fatal(err)
		}
		fields := contractFields(t, empty)
		if _, exists := fields["address"]; exists {
			t.Fatalf("empty addresses should be absent on the wire: %s", contractJSON(t, empty))
		}
		if _, exists := fields["topics"]; exists {
			t.Fatalf("nil topics should be absent on the wire: %s", contractJSON(t, empty))
		}
		if string(fields["fromBlock"]) != `"0x0"` || string(fields["toBlock"]) != `"latest"` {
			t.Fatalf("default geth filter range changed: %s", contractJSON(t, empty))
		}
	}
	address, hash := common.HexToAddress("0x11"), common.HexToHash("0x22")
	input := ethereum.FilterQuery{FromBlock: big.NewInt(0), ToBlock: big.NewInt(17), Addresses: []common.Address{address}, Topics: [][]common.Hash{nil, {hash}, {}}}
	got, _, err := ethadapter.FromGethFilterQuery(input)
	if err != nil {
		t.Fatal(err)
	}
	fields := contractFields(t, got)
	if string(fields["fromBlock"]) != `"0x0"` || string(fields["toBlock"]) != `"0x11"` || !bytes.Contains(fields["topics"], []byte("null")) {
		t.Fatalf("filter range/wildcards changed: %s", contractJSON(t, got))
	}
	input.BlockHash = &hash
	_, _, err = ethadapter.FromGethFilterQuery(input)
	requireConversionError(t, err)
}

func contractReceipt(t *testing.T, receipt *types.Receipt) *ethapi.Receipt {
	t.Helper()
	fields := contractFields(t, receipt)
	fields["from"] = contractJSON(t, common.HexToAddress("0x11"))
	fields["to"] = json.RawMessage("null")
	if len(receipt.PostState) == 0 {
		delete(fields, "root")
	} else {
		delete(fields, "status")
	}
	out, err := ethapi.NewReceipt(contractJSON(t, fields))
	if err != nil {
		t.Fatal(err)
	}
	return &out
}

func TestIndependentReceiptAndLogConversionPreservesMetadataAndPresence(t *testing.T) {
	blockHash, txHash := common.HexToHash("0x12"), common.HexToHash("0x34")
	log := &types.Log{Address: common.HexToAddress("0x56"), Topics: []common.Hash{common.HexToHash("0x78")}, Data: []byte{1, 2}, BlockNumber: 17, TxHash: txHash, TxIndex: 3, BlockHash: blockHash, Index: 4, Removed: true, BlockTimestamp: 123}
	receipt := &types.Receipt{Type: types.BlobTxType, Status: 1, CumulativeGasUsed: 42000, Logs: []*types.Log{log}, TxHash: txHash, ContractAddress: common.HexToAddress("0x90"), GasUsed: 21000, EffectiveGasPrice: new(big.Int).Lsh(big.NewInt(1), 80), BlobGasUsed: 131072, BlobGasPrice: new(big.Int).Lsh(big.NewInt(1), 90), BlockHash: blockHash, BlockNumber: big.NewInt(17), TransactionIndex: 3}
	receipt.Bloom = types.CreateBloom(receipt)
	input := contractReceipt(t, receipt)
	before := contractJSON(t, input)
	got, _, err := ethadapter.ToGethReceipt(input)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, receipt) {
		t.Fatalf("receipt metadata changed: got %#v want %#v", got, receipt)
	}
	got.Logs[0].Topics[0][0] = 9
	got.Logs[0].Data[0] = 9
	got.EffectiveGasPrice.SetUint64(0)
	if !bytes.Equal(before, contractJSON(t, input)) {
		t.Fatal("receipt result aliases source")
	}
	for _, present := range []bool{false, true} {
		input := contractReceipt(t, receipt)
		input.BlobGasPrice = nil
		if present {
			zero := ethapi.Quantity("0x0")
			input.BlobGasPrice = &zero
		}
		got, _, err := ethadapter.ToGethReceipt(input)
		if err != nil || (got.BlobGasPrice != nil) != present {
			t.Fatalf("blobGasPrice presence changed: present=%v result=%#v err=%v", present, got, err)
		}
	}
	postState := *receipt
	postState.PostState = common.HexToHash("0xab").Bytes()
	postState.Status = 0
	got, _, err = ethadapter.ToGethReceipt(contractReceipt(t, &postState))
	if err != nil || !bytes.Equal(got.PostState, postState.PostState) {
		t.Fatalf("pre-Byzantium post-state lost: %#v %v", got, err)
	}
	// The native model can carry both provider status and a historical root.
	// The adapter has storage for both and must not narrow the admitted domain.
	withBoth := contractReceipt(t, &postState)
	status := ethapi.Quantity("0x1")
	withBoth.Status = &status
	got, _, err = ethadapter.ToGethReceipt(withBoth)
	if err != nil || got.Status != 1 || !bytes.Equal(got.PostState, postState.PostState) {
		t.Fatalf("root plus status was rejected or lost: %#v %v", got, err)
	}
	withBoth.Status, withBoth.Root = nil, nil
	_, _, err = ethadapter.ToGethReceipt(withBoth)
	requireConversionError(t, err)
	input = contractReceipt(t, receipt)
	input.TransactionIndex = "0x10000000000000000"
	_, _, err = ethadapter.ToGethReceipt(input)
	requireConversionError(t, err)
	input = contractReceipt(t, receipt)
	input.EffectiveGasPrice = ""
	_, _, err = ethadapter.ToGethReceipt(input)
	requireConversionError(t, err)

	minimal := ethapi.Log{TransactionHash: ethapi.Hash(txHash.Hex())}
	zero, report, err := ethadapter.ToGethLog(minimal)
	if err != nil || zero.TxHash != txHash || len(report.Omissions) == 0 {
		t.Fatalf("missing log metadata must be explicit in the conversion report: %#v %#v %v", zero, report, err)
	}
	badIndex := ethapi.Quantity("0x10000000000000000")
	minimal.LogIndex = &badIndex
	_, _, err = ethadapter.ToGethLog(minimal)
	requireConversionError(t, err)
}
