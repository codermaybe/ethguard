package ethguard_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/codermaybe/ethguard/rpc"
	"github.com/codermaybe/ethguard/rpc/methods"
	"github.com/codermaybe/ethguard/rpc/projection"
)

// T09.2 locks the lifecycle of every protected field: representation
// variation, protected-field mutation, round-trip through the canonical
// encoder, and field conservation. All assertions run through the real
// aggregator and method policies; no testdata, golden, or live provider is
// involved.

type t09Resolution int

const (
	t09Strict t09Resolution = iota
	t09Median
	t09Local
)

type t09MutationExpect int

const (
	t09MutationStrict t09MutationExpect = iota
	t09MutationMedian
	t09MutationLocal
)

type t09FieldRow struct {
	method string
	params []any

	// variants are equivalent spellings of one protected answer. At least
	// three are required so each one can occupy every pool position.
	variants []json.RawMessage
	// other is a legal answer with a different protected value.
	other json.RawMessage
	// mutation is an answer that must fail admission or encoding. When
	// mutationFault is set, the source instead returns a provider error; that
	// is the only available gate for whole-answer local methods.
	mutation      json.RawMessage
	mutationFault bool
	fieldHint     string

	decode func(json.RawMessage) (string, error)
	// typed is the production Client/adapter probe. Round trips compare its
	// result for the raw answer with its result for the canonical Value; a
	// test-side decoder cannot stand in for that boundary.
	typed      func(*testing.T, *t09Fleet, []any) (string, error)
	resolution t09Resolution

	protected []string
	outside   []string
	// omitted lists fields the method's declared result domain deliberately
	// omits: standard schema metadata that is derived from the core result or
	// from inclusion context, and vendor fields where no semantic projector
	// exists. The omission is asserted, not inferred.
	omitted []string
	// schema is the exact official result-path set for this method. It is
	// compared for equality, so a newly added nested transaction, log, or
	// receipt field fails until the row declares it.
	schema []string
	// schemaOmitted lists nested official paths the encoder deliberately drops
	// even though their top-level object is protected.
	schemaOmitted []string
	// noSchema marks the two local methods with no pinned execution-apis
	// result schema; the coverage test rejects any other missing schema.
	noSchema  bool
	projector bool
	// nullPreserved declares that the canonical encoder materializes optional
	// JSON null fields (the typed block header does); otherwise null means
	// absence and must not appear in Value.
	nullPreserved bool
	// whole marks upstream-local methods whose entire answer is the selected
	// value. They have no cross-source protected-field admission; the typed
	// boundary is asserted separately.
	whole bool

	mutationExpect t09MutationExpect
	mutationReject int
	notes          string
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

var t09BlockSchemaPaths = []string{
	"/baseFeePerGas",
	"/blobGasUsed",
	"/blockAccessListHash",
	"/difficulty",
	"/excessBlobGas",
	"/extraData",
	"/gasLimit",
	"/gasUsed",
	"/hash",
	"/logsBloom",
	"/miner",
	"/mixHash",
	"/nonce",
	"/number",
	"/parentBeaconBlockRoot",
	"/parentHash",
	"/receiptsRoot",
	"/requestsHash",
	"/sha3Uncles",
	"/size",
	"/stateRoot",
	"/timestamp",
	"/transactions",
	"/transactions/*",
	"/transactions/*/accessList",
	"/transactions/*/accessList/*",
	"/transactions/*/accessList/*/address",
	"/transactions/*/accessList/*/storageKeys",
	"/transactions/*/accessList/*/storageKeys/*",
	"/transactions/*/authorizationList",
	"/transactions/*/authorizationList/*",
	"/transactions/*/authorizationList/*/address",
	"/transactions/*/authorizationList/*/chainId",
	"/transactions/*/authorizationList/*/nonce",
	"/transactions/*/authorizationList/*/r",
	"/transactions/*/authorizationList/*/s",
	"/transactions/*/authorizationList/*/yParity",
	"/transactions/*/blobVersionedHashes",
	"/transactions/*/blobVersionedHashes/*",
	"/transactions/*/blockHash",
	"/transactions/*/blockNumber",
	"/transactions/*/blockTimestamp",
	"/transactions/*/chainId",
	"/transactions/*/from",
	"/transactions/*/gas",
	"/transactions/*/gasPrice",
	"/transactions/*/hash",
	"/transactions/*/input",
	"/transactions/*/maxFeePerBlobGas",
	"/transactions/*/maxFeePerGas",
	"/transactions/*/maxPriorityFeePerGas",
	"/transactions/*/nonce",
	"/transactions/*/r",
	"/transactions/*/s",
	"/transactions/*/to",
	"/transactions/*/transactionIndex",
	"/transactions/*/type",
	"/transactions/*/v",
	"/transactions/*/value",
	"/transactions/*/yParity",
	"/transactionsRoot",
	"/uncles",
	"/uncles/*",
	"/withdrawals",
	"/withdrawals/*",
	"/withdrawals/*/address",
	"/withdrawals/*/amount",
	"/withdrawals/*/index",
	"/withdrawals/*/validatorIndex",
	"/withdrawalsRoot",
}
var t09ReceiptSchemaPaths = []string{
	"/blobGasPrice",
	"/blobGasUsed",
	"/blockHash",
	"/blockNumber",
	"/contractAddress",
	"/cumulativeGasUsed",
	"/effectiveGasPrice",
	"/from",
	"/gasUsed",
	"/logs",
	"/logs/*",
	"/logs/*/address",
	"/logs/*/blockHash",
	"/logs/*/blockNumber",
	"/logs/*/blockTimestamp",
	"/logs/*/data",
	"/logs/*/logIndex",
	"/logs/*/removed",
	"/logs/*/topics",
	"/logs/*/topics/*",
	"/logs/*/transactionHash",
	"/logs/*/transactionIndex",
	"/logsBloom",
	"/root",
	"/status",
	"/to",
	"/transactionHash",
	"/transactionIndex",
	"/type",
}
var t09BlockReceiptsSchemaPaths = []string{
	"/*",
	"/*/blobGasPrice",
	"/*/blobGasUsed",
	"/*/blockHash",
	"/*/blockNumber",
	"/*/contractAddress",
	"/*/cumulativeGasUsed",
	"/*/effectiveGasPrice",
	"/*/from",
	"/*/gasUsed",
	"/*/logs",
	"/*/logs/*",
	"/*/logs/*/address",
	"/*/logs/*/blockHash",
	"/*/logs/*/blockNumber",
	"/*/logs/*/blockTimestamp",
	"/*/logs/*/data",
	"/*/logs/*/logIndex",
	"/*/logs/*/removed",
	"/*/logs/*/topics",
	"/*/logs/*/topics/*",
	"/*/logs/*/transactionHash",
	"/*/logs/*/transactionIndex",
	"/*/logsBloom",
	"/*/root",
	"/*/status",
	"/*/to",
	"/*/transactionHash",
	"/*/transactionIndex",
	"/*/type",
}
var t09TransactionSchemaPaths = []string{
	"/accessList",
	"/accessList/*",
	"/accessList/*/address",
	"/accessList/*/storageKeys",
	"/accessList/*/storageKeys/*",
	"/authorizationList",
	"/authorizationList/*",
	"/authorizationList/*/address",
	"/authorizationList/*/chainId",
	"/authorizationList/*/nonce",
	"/authorizationList/*/r",
	"/authorizationList/*/s",
	"/authorizationList/*/yParity",
	"/blobVersionedHashes",
	"/blobVersionedHashes/*",
	"/blockHash",
	"/blockNumber",
	"/blockTimestamp",
	"/chainId",
	"/from",
	"/gas",
	"/gasPrice",
	"/hash",
	"/input",
	"/maxFeePerBlobGas",
	"/maxFeePerGas",
	"/maxPriorityFeePerGas",
	"/nonce",
	"/r",
	"/s",
	"/to",
	"/transactionIndex",
	"/type",
	"/v",
	"/value",
	"/yParity",
}
var t09LogSchemaPaths = []string{
	"/*",
	"/*/address",
	"/*/blockHash",
	"/*/blockNumber",
	"/*/blockTimestamp",
	"/*/data",
	"/*/logIndex",
	"/*/removed",
	"/*/topics",
	"/*/topics/*",
	"/*/transactionHash",
	"/*/transactionIndex",
}
var t09FeeSchemaPaths = []string{
	"/baseFeePerBlobGas",
	"/baseFeePerBlobGas/*",
	"/baseFeePerGas",
	"/baseFeePerGas/*",
	"/blobGasUsedRatio",
	"/blobGasUsedRatio/*",
	"/gasUsedRatio",
	"/gasUsedRatio/*",
	"/oldestBlock",
	"/reward",
	"/reward/*",
	"/reward/*/*",
}
var t09AccessSchemaPaths = []string{
	"/accessList",
	"/accessList/*",
	"/accessList/*/address",
	"/accessList/*/storageKeys",
	"/accessList/*/storageKeys/*",
	"/error",
	"/gasUsed",
}
var t09SyncingSchemaPaths = []string{
	"",
	"/currentBlock",
	"/highestBlock",
	"/startingBlock",
}

func t09FieldRows(t *testing.T) []t09FieldRow {
	t.Helper()
	chain := t09NewChain(t)
	block, blockHash := chain.block(t09AnchorHeight, 1_700_000_000, 21_000)
	// A second block with the same height but a different header: a legal fork
	// answer for eth_getBlockByNumber, and a binding failure for byHash.
	fork, _ := chain.block(t09AnchorHeight, 1_700_000_100, 42_000)
	otherHeight, _ := chain.block(t09AnchorHeight+1, 1_700_000_200, 21_000)
	receipt := chain.receipt(blockHash, t09AnchorHeight, "0x5208")
	receipt2 := chain.receipt2(blockHash, t09AnchorHeight, "0x5208")
	otherReceipt := chain.receipt(blockHash, t09AnchorHeight, "0x1")
	otherReceipt2 := chain.receipt2(blockHash, t09AnchorHeight, "0x1")
	minedTx := chain.txObject(true, blockHash, t09AnchorHeight, 0)
	otherTx := chain.txObject(true, blockHash, t09AnchorHeight+1, 1)
	logs := t09Logs(blockHash, chain.txHash)
	otherLogs := t09JSON([]json.RawMessage{t09Log(blockHash, chain.txHash, t09AnchorHeight, 0, "0x00fe")})
	receipts := t09JSON([]json.RawMessage{receipt, receipt2})
	otherReceipts := t09JSON([]json.RawMessage{otherReceipt, otherReceipt2})
	fee := t09FeeHistory()
	otherFee := json.RawMessage(`{"oldestBlock":"0xf","baseFeePerGas":["0x1","0x2","0x3"],"gasUsedRatio":[0.25,0.5],"reward":[["0x1"],["0x2"]],"vendor":"t09"}`)
	access := t09AccessList()
	otherAccess := json.RawMessage(`{"accessList":[{"address":"0x00000000000000000000000000000000000000cc","storageKeys":["0x0000000000000000000000000000000000000000000000000000000000000001","0x0000000000000000000000000000000000000000000000000000000000000002"]}],"gasUsed":"0x5209","vendor":"t09"}`)

	wordA := "0x" + strings.Repeat("00", 31) + "aa"
	wordB := "0x" + strings.Repeat("00", 31) + "bb"
	wrongHash := "0x" + strings.Repeat("77", 32)

	blockProtected := []string{
		"parentHash", "sha3Uncles", "miner", "stateRoot", "transactionsRoot",
		"receiptsRoot", "logsBloom", "difficulty", "number", "gasLimit",
		"gasUsed", "timestamp", "extraData", "mixHash", "nonce",
		"baseFeePerGas", "withdrawalsRoot", "blobGasUsed", "excessBlobGas",
		"parentBeaconBlockRoot", "requestsHash", "blockAccessListHash",
		"slotNumber", "hash", "transactions", "uncles", "withdrawals",
	}
	receiptProtected := []string{
		"transactionHash", "transactionIndex", "blockHash", "blockNumber",
		"from", "to", "contractAddress", "cumulativeGasUsed", "gasUsed",
		"effectiveGasPrice", "blobGasUsed", "blobGasPrice", "root",
		"logsBloom", "logs", "status", "type",
	}
	txProtected := []string{
		"type", "chainId", "nonce", "to", "gas", "gasPrice",
		"maxPriorityFeePerGas", "maxFeePerGas", "maxFeePerBlobGas",
		"value", "input", "accessList", "authorizationList",
		"blobVersionedHashes", "blockTimestamp",
		"v", "r", "s", "yParity", "hash", "from", "blockHash", "blockNumber",
		"transactionIndex",
	}
	logProtected := []string{
		"address", "topics", "data", "blockNumber", "transactionHash",
		"transactionIndex", "blockHash", "logIndex", "removed", "blockTimestamp",
	}
	feeProtected := []string{"oldestBlock", "baseFeePerGas", "gasUsedRatio", "reward", "baseFeePerBlobGas", "blobGasUsedRatio"}
	accessProtected := []string{"accessList", "gasUsed", "error"}

	return []t09FieldRow{
		{
			method:         methods.MethodChainID,
			variants:       t09Variants(t, json.RawMessage(`"0x1"`)),
			other:          json.RawMessage(`"0x2"`),
			mutation:       json.RawMessage(`true`),
			fieldHint:      "hex quantity",
			decode:         t09DecodeBig,
			typed:          t09TypedChainID,
			resolution:     t09Strict,
			protected:      []string{"$"},
			mutationExpect: t09MutationStrict,
			mutationReject: 1,
		},
		{
			method:         methods.MethodGetBlockByHash,
			params:         []any{blockHash, true},
			variants:       t09VariantsNoEscape(t, block),
			other:          otherHeight,
			mutation:       t09DeleteField(t, block, "hash"),
			fieldHint:      "block",
			decode:         t09DecodeBlock,
			typed:          t09TypedBlockByHash,
			schema:         t09BlockSchemaPaths,
			schemaOmitted:  []string{"/transactions/*/blockHash", "/transactions/*/blockNumber", "/transactions/*/transactionIndex", "/transactions/*/from"},
			resolution:     t09Strict,
			protected:      blockProtected,
			outside:        []string{"totalDifficulty", "vendor"},
			omitted:        []string{"size", "blockNumber", "transactionIndex", "blockHash", "from"},
			projector:      true,
			nullPreserved:  true,
			mutationExpect: t09MutationStrict,
			mutationReject: 0,
			notes:          "a different block cannot satisfy the requested hash; the second dissent is an admission fault, not a legal Reject. JSON-escaped hex is covered by TestT092EscapedHexAdmissionBoundary",
		},
		{
			method:         methods.MethodGetBlockByNumber,
			params:         []any{fmt.Sprintf("0x%x", t09AnchorHeight), true},
			variants:       t09VariantsNoEscape(t, block),
			other:          fork,
			mutation:       t09DeleteField(t, block, "hash"),
			fieldHint:      "block",
			decode:         t09DecodeBlock,
			typed:          t09TypedBlockByNumber,
			schema:         t09BlockSchemaPaths,
			schemaOmitted:  []string{"/transactions/*/blockHash", "/transactions/*/blockNumber", "/transactions/*/transactionIndex", "/transactions/*/from"},
			resolution:     t09Strict,
			protected:      blockProtected,
			outside:        []string{"totalDifficulty", "vendor"},
			omitted:        []string{"size", "blockNumber", "transactionIndex", "blockHash", "from"},
			projector:      true,
			nullPreserved:  true,
			mutationExpect: t09MutationStrict,
			mutationReject: 1,
			notes:          "JSON-escaped hex is covered by TestT092EscapedHexAdmissionBoundary",
		},
		{
			method:         methods.MethodGetBalance,
			params:         []any{t09Address, fmt.Sprintf("0x%x", t09AnchorHeight)},
			variants:       t09Variants(t, json.RawMessage(`"0x64"`)),
			other:          json.RawMessage(`"0x3e8"`),
			mutation:       json.RawMessage(`"0x01"`),
			fieldHint:      "hex quantity",
			decode:         t09DecodeBig,
			typed:          t09TypedBalance,
			resolution:     t09Strict,
			protected:      []string{"$"},
			mutationExpect: t09MutationStrict,
			mutationReject: 1,
		},
		{
			method:         methods.MethodGetTransactionReceipt,
			params:         []any{chain.txHash},
			variants:       t09Variants(t, receipt),
			other:          otherReceipt,
			mutation:       t09DeleteField(t, receipt, "logs"),
			fieldHint:      "receipt",
			decode:         t09DecodeReceipt,
			typed:          t09TypedReceipt,
			schema:         t09ReceiptSchemaPaths,
			resolution:     t09Strict,
			protected:      receiptProtected,
			outside:        []string{"l1Fee", "vendor"},
			projector:      true,
			mutationExpect: t09MutationStrict,
			mutationReject: 1,
		},
		{
			method:         methods.MethodGetTransactionByHash,
			params:         []any{chain.txHash},
			variants:       t09VariantsNoEscape(t, minedTx),
			other:          otherTx,
			mutation:       t09SetField(t, minedTx, "hash", wrongHash),
			fieldHint:      "transaction",
			decode:         t09DecodeTransaction,
			typed:          t09TypedTransaction,
			schema:         t09TransactionSchemaPaths,
			resolution:     t09Strict,
			protected:      txProtected,
			outside:        []string{"vendor"},
			projector:      true,
			mutationExpect: t09MutationStrict,
			mutationReject: 1,
			notes:          "JSON-escaped hex is covered by TestT092EscapedHexAdmissionBoundary; null fee fields are dropped as absence",
		},
		{
			method:         methods.MethodGetBlockReceipts,
			params:         []any{blockHash},
			variants:       t09Variants(t, receipts),
			other:          otherReceipts,
			mutation:       t09DeleteNestedField(t, receipts, 0, "logs"),
			fieldHint:      "receipt",
			decode:         t09DecodeBlockReceipts,
			typed:          t09TypedBlockReceipts,
			schema:         t09BlockReceiptsSchemaPaths,
			resolution:     t09Strict,
			protected:      receiptProtected,
			outside:        []string{"l1Fee", "vendor"},
			projector:      true,
			mutationExpect: t09MutationStrict,
			mutationReject: 1,
		},
		{
			method:         methods.MethodGetTransactionCount,
			params:         []any{t09Address, fmt.Sprintf("0x%x", t09AnchorHeight)},
			variants:       t09Variants(t, json.RawMessage(`"0xa"`)),
			other:          json.RawMessage(`"0xb"`),
			mutation:       json.RawMessage(`"0x01"`),
			fieldHint:      "hex quantity",
			decode:         t09DecodeBig,
			typed:          t09TypedNonce,
			resolution:     t09Strict,
			protected:      []string{"$"},
			mutationExpect: t09MutationStrict,
			mutationReject: 1,
		},
		{
			method:         methods.MethodGetStorageAt,
			params:         []any{t09Address, t09StorageKey, fmt.Sprintf("0x%x", t09AnchorHeight)},
			variants:       t09Variants(t, json.RawMessage(`"`+wordA+`"`)),
			other:          json.RawMessage(`"` + wordB + `"`),
			mutation:       json.RawMessage(`"0x01"`),
			fieldHint:      "storage word",
			decode:         t09DecodeBytes,
			typed:          t09TypedStorage,
			resolution:     t09Strict,
			protected:      []string{"$"},
			mutationExpect: t09MutationStrict,
			mutationReject: 1,
		},
		{
			method:         methods.MethodGetCode,
			params:         []any{t09Address, fmt.Sprintf("0x%x", t09AnchorHeight)},
			variants:       t09Variants(t, json.RawMessage(`"0x6080"`)),
			other:          json.RawMessage(`"0x6081"`),
			mutation:       json.RawMessage(`"0xabc"`),
			fieldHint:      "hex data",
			decode:         t09DecodeBytes,
			typed:          t09TypedCode,
			resolution:     t09Strict,
			protected:      []string{"$"},
			mutationExpect: t09MutationStrict,
			mutationReject: 1,
		},
		{
			method:         methods.MethodCall,
			params:         []any{map[string]any{"to": t09Address}, fmt.Sprintf("0x%x", t09AnchorHeight)},
			variants:       t09Variants(t, json.RawMessage(`"0xdeadbeef"`)),
			other:          json.RawMessage(`"0xdeadbeee"`),
			mutation:       json.RawMessage(`"0xdeadbee"`),
			fieldHint:      "hex data",
			decode:         t09DecodeBytes,
			typed:          t09TypedCall,
			resolution:     t09Strict,
			protected:      []string{"$"},
			mutationExpect: t09MutationStrict,
			mutationReject: 1,
			notes:          "success data only; revert Value/RevertContext and 4e withholding have dedicated tests",
		},
		{
			method:         methods.MethodEstimateGas,
			params:         []any{map[string]any{"to": t09Address}, fmt.Sprintf("0x%x", t09AnchorHeight)},
			variants:       t09Variants(t, json.RawMessage(`"0x5208"`)),
			other:          json.RawMessage(`"0x5209"`),
			mutation:       json.RawMessage(`"0x05208"`),
			fieldHint:      "hex quantity",
			decode:         t09DecodeQuantity,
			typed:          t09TypedEstimateGas,
			resolution:     t09Median,
			protected:      []string{"$"},
			mutationExpect: t09MutationMedian,
			mutationReject: 1,
		},
		{
			method:         methods.MethodGetLogs,
			params:         []any{map[string]any{"blockHash": blockHash}},
			variants:       t09Variants(t, logs),
			other:          otherLogs,
			mutation:       t09DeleteNestedField(t, logs, 0, "topics"),
			fieldHint:      "topics",
			decode:         t09DecodeLogs,
			typed:          t09TypedLogs,
			schema:         t09LogSchemaPaths,
			resolution:     t09Strict,
			protected:      logProtected,
			outside:        []string{"vendor"},
			projector:      true,
			mutationExpect: t09MutationStrict,
			mutationReject: 1,
		},
		{
			method:         methods.MethodFeeHistory,
			params:         []any{uint64(2), fmt.Sprintf("0x%x", t09AnchorHeight), []float64{50}},
			variants:       t09Variants(t, fee),
			other:          otherFee,
			mutation:       t09DeleteField(t, fee, "oldestBlock"),
			fieldHint:      "feeHistory",
			decode:         t09DecodeFee,
			typed:          t09TypedFeeHistory,
			schema:         t09FeeSchemaPaths,
			resolution:     t09Strict,
			protected:      feeProtected,
			outside:        []string{"vendor"},
			projector:      true,
			mutationExpect: t09MutationStrict,
			mutationReject: 1,
		},
		{
			method:         methods.MethodGasPrice,
			variants:       t09Variants(t, json.RawMessage(`"0x3b9aca00"`)),
			other:          json.RawMessage(`"0x3b9aca01"`),
			mutation:       json.RawMessage(`"0x03b9aca00"`),
			fieldHint:      "hex quantity",
			decode:         t09DecodeBig,
			typed:          t09TypedGasPrice,
			resolution:     t09Median,
			protected:      []string{"$"},
			mutationExpect: t09MutationMedian,
			mutationReject: 1,
		},
		{
			method:         methods.MethodMaxPriorityFeePerGas,
			variants:       t09Variants(t, json.RawMessage(`"0x5f5e100"`)),
			other:          json.RawMessage(`"0x5f5e101"`),
			mutation:       json.RawMessage(`"0x05f5e100"`),
			fieldHint:      "hex quantity",
			decode:         t09DecodeBig,
			typed:          t09TypedMaxPriorityFee,
			resolution:     t09Median,
			protected:      []string{"$"},
			mutationExpect: t09MutationMedian,
			mutationReject: 1,
		},
		{
			method:         methods.MethodBlobBaseFee,
			variants:       t09Variants(t, json.RawMessage(`"0x1"`)),
			other:          json.RawMessage(`"0x2"`),
			mutation:       json.RawMessage(`"0x01"`),
			fieldHint:      "hex quantity",
			decode:         t09DecodeBig,
			typed:          t09TypedBlobBaseFee,
			resolution:     t09Median,
			protected:      []string{"$"},
			mutationExpect: t09MutationMedian,
			mutationReject: 1,
		},
		{
			method:         methods.MethodBlockNumber,
			variants:       t09Variants(t, json.RawMessage(`"0x10"`)),
			other:          json.RawMessage(`"0x11"`),
			mutation:       json.RawMessage(`"0x010"`),
			fieldHint:      "hex quantity",
			decode:         t09DecodeBig,
			typed:          t09TypedBlockNumber,
			resolution:     t09Median,
			protected:      []string{"$"},
			mutationExpect: t09MutationMedian,
			mutationReject: 1,
		},
		{
			method:         methods.MethodCreateAccessList,
			params:         []any{map[string]any{"to": t09Address}, fmt.Sprintf("0x%x", t09AnchorHeight)},
			variants:       t09Variants(t, access),
			other:          otherAccess,
			mutation:       t09DeleteField(t, access, "accessList"),
			fieldHint:      "accessList",
			decode:         t09DecodeAccess,
			typed:          t09TypedAccessList,
			schema:         t09AccessSchemaPaths,
			resolution:     t09Strict,
			protected:      accessProtected,
			outside:        []string{"vendor"},
			omitted:        []string{"vendor"},
			mutationExpect: t09MutationStrict,
			mutationReject: 1,
			notes:          "no semantic projector is registered; vendor fields are a declared encoder omission",
		},
		{
			method:         methods.MethodPeerCount,
			variants:       []json.RawMessage{json.RawMessage(`"0x5"`), json.RawMessage(`"0x05"`), json.RawMessage(`"\u0030x5"`)},
			other:          json.RawMessage(`"0x6"`),
			mutation:       json.RawMessage(`"0xzz"`),
			fieldHint:      "hex quantity",
			decode:         t09DecodeQuantity,
			typed:          t09TypedPeerCount,
			resolution:     t09Local,
			noSchema:       true,
			protected:      []string{"$"},
			mutationExpect: t09MutationLocal,
		},
		{
			method:         methods.MethodSyncing,
			variants:       []json.RawMessage{json.RawMessage(`false`), json.RawMessage(` false `), json.RawMessage("false\n")},
			other:          json.RawMessage(`false`),
			mutationFault:  true,
			decode:         t09DecodeSyncing,
			typed:          t09TypedSyncing,
			schema:         t09SyncingSchemaPaths,
			resolution:     t09Local,
			protected:      []string{"currentBlock", "highestBlock", "startingBlock"},
			whole:          true,
			mutationExpect: t09MutationLocal,
			notes:          "T09 uses only the legal false state. The progress-object form is not synthesized (pipeline 5.1.1) and remains a capture gap; decision 0061 pins only its top-level shape, while nested field typing stays a typed-projection boundary",
		},
		{
			method:         methods.MethodNetVersion,
			variants:       []json.RawMessage{json.RawMessage(`"1"`), json.RawMessage(`"01"`), json.RawMessage(`"\u0031"`)},
			other:          json.RawMessage(`"2"`),
			mutation:       json.RawMessage(`"0x1"`),
			fieldHint:      "decimal string",
			decode:         t09DecodeDecimal,
			typed:          t09TypedNetVersion,
			resolution:     t09Strict,
			protected:      []string{"$"},
			mutationExpect: t09MutationStrict,
			mutationReject: 1,
		},
		{
			method:         methods.MethodListening,
			variants:       []json.RawMessage{json.RawMessage(`true`), json.RawMessage(`true`), json.RawMessage(`true`)},
			other:          json.RawMessage(`false`),
			mutationFault:  true,
			decode:         t09DecodeBool,
			typed:          t09TypedListening,
			resolution:     t09Local,
			noSchema:       true,
			whole:          true,
			mutationExpect: t09MutationLocal,
			notes:          "upstream-local whole answer: no cross-source protected-field admission; typed boundary test covers wrong-typed fields",
		},
	}
}

// TestT092FieldLifecycleCoverage keeps the T09.2 table and the registered
// method inventory in lockstep in both directions. A new registered method or
// a stale row fails with the method name.
func TestT092FieldLifecycleCoverage(t *testing.T) {
	rows := t09FieldRows(t)
	seen := make(map[string]bool, len(rows))
	for _, row := range rows {
		if seen[row.method] {
			t.Fatalf("%s: duplicate field-lifecycle row", row.method)
		}
		seen[row.method] = true
		if len(row.variants) < 3 {
			t.Fatalf("%s: %d representation variants, want at least 3", row.method, len(row.variants))
		}
		if row.mutation == nil && !row.mutationFault {
			t.Fatalf("%s: no protected-field mutation", row.method)
		}
		if row.decode == nil {
			t.Fatalf("%s: no typed decoder", row.method)
		}
		if !row.whole && len(row.protected) == 0 {
			t.Fatalf("%s: no protected field declaration", row.method)
		}
		if row.resolution != t09Strict && row.resolution != t09Median && row.resolution != t09Local {
			t.Fatalf("%s: unknown resolution %d", row.method, row.resolution)
		}
		assertSchemaFieldsDeclared(t, row)
		switch row.mutationExpect {
		case t09MutationStrict:
			if row.resolution != t09Strict {
				t.Fatalf("%s: strict mutation expectation on a non-strict method", row.method)
			}
		case t09MutationMedian:
			if row.resolution != t09Median {
				t.Fatalf("%s: median mutation expectation on a non-median method", row.method)
			}
		case t09MutationLocal:
			if row.resolution != t09Local {
				t.Fatalf("%s: local mutation expectation on a non-local method", row.method)
			}
		default:
			t.Fatalf("%s: unknown mutation expectation %d", row.method, row.mutationExpect)
		}
	}
	for _, name := range methods.RegisteredNames() {
		if !seen[name] {
			t.Fatalf("registered method %q has no T09.2 field-lifecycle row", name)
		}
		seen[name] = false
	}
	for name, present := range seen {
		if present {
			t.Fatalf("row %q names no registered method", name)
		}
	}
}

// assertSchemaFieldsDeclared compares the exact pinned execution-apis result
// path set with the row's declaration. Nested transaction, log, receipt, and
// fee paths are included, so adding one to the schema fails until the row names
// it. Each path is then classified by its nearest declared object, with
// schemaOmitted for nested fields the encoder drops.
func assertSchemaFieldsDeclared(t *testing.T, row t09FieldRow) {
	t.Helper()
	fields, err := projection.ExecutionAPIStandardFields(row.method)
	if err != nil {
		if row.noSchema {
			return
		}
		t.Fatalf("%s: schema fields: %v", row.method, err)
	}
	if row.noSchema {
		t.Fatalf("%s: row declares noSchema but a pinned result schema exists", row.method)
	}

	official := make(map[string]bool, len(fields))
	for _, field := range fields {
		official[field.Path] = true
	}
	if len(official) == 1 && official[""] {
		if len(row.protected) == 0 || row.protected[0] != "$" {
			t.Fatalf("%s: scalar schema lacks a $-root declaration", row.method)
		}
		return
	}

	declared := make(map[string]bool, len(row.schema))
	for _, path := range row.schema {
		declared[path] = true
	}
	for path := range official {
		if !declared[path] {
			t.Fatalf("%s: schema path %q has no exact lifecycle declaration", row.method, path)
		}
	}
	for path := range declared {
		if !official[path] {
			t.Fatalf("%s: declared schema path %q is not in the pinned schema", row.method, path)
		}
	}

	for path := range official {
		if path == "" {
			if !row.whole {
				t.Fatalf("%s: object-root schema path on a non-whole method", row.method)
			}
			continue
		}
		normalized := stripSchemaArrayPrefix(path)
		if normalized == "" || strings.Trim(normalized, "/*") == "" {
			continue
		}
		if containsString(row.schemaOmitted, normalized) {
			continue
		}
		top := topLevelField(normalized)
		if top == "" {
			t.Fatalf("%s: schema path %q has no field token", row.method, path)
		}
		if !containsString(row.protected, top) && !containsString(row.outside, top) && !containsString(row.omitted, top) {
			t.Fatalf("%s: schema path %q (root %q) has no lifecycle declaration", row.method, path, top)
		}
	}
}

// stripSchemaArrayPrefix removes leading array-element wrappers such as the
// /*/ in eth_getBlockReceipts and eth_getLogs results.
func stripSchemaArrayPrefix(path string) string {
	for strings.HasPrefix(path, "/*") {
		path = strings.TrimPrefix(path, "/*")
	}
	return path
}

func topLevelField(path string) string {
	for _, token := range strings.Split(strings.TrimPrefix(path, "/"), "/") {
		if token == "" || token == "*" {
			continue
		}
		token = strings.ReplaceAll(token, "~1", "/")
		return strings.ReplaceAll(token, "~0", "~")
	}
	return ""
}

// TestT092RepresentationStability places every spelling in every pool
// position. The canonical Value must not move, while the representative (or
// local Support) follows capture order. The same run checks that protected
// fields are present and declared outside fields are absent.
func TestT092RepresentationStability(t *testing.T) {
	for _, row := range t09FieldRows(t) {
		t.Run(row.method, func(t *testing.T) {
			var canonical json.RawMessage
			for rotation := 0; rotation < len(row.variants); rotation++ {
				fleet := t09NewFleet(t, len(row.variants))
				payloads := make([]json.RawMessage, len(row.variants))
				for i := range payloads {
					payloads[i] = row.variants[(i+rotation)%len(row.variants)]
				}
				fleet.returnRaw(row.method, payloads)
				decision, err := fleet.client.Call(fleet.context(t), row.method, row.params...)
				if err != nil {
					t.Fatalf("rotation %d: %v", rotation, err)
				}
				if canonical == nil {
					canonical = append(json.RawMessage(nil), decision.Value...)
				} else if !bytes.Equal(canonical, decision.Value) {
					t.Fatalf("rotation %d moved the canonical Value:\nfirst %s\nnow   %s", rotation, canonical, decision.Value)
				}
				switch row.resolution {
				case t09Strict:
					if decision.Status != rpc.Corroborated || len(decision.Support) != len(row.variants) || len(decision.Reject) != 0 {
						t.Fatalf("rotation %d: strict decision = %+v", rotation, decision)
					}
					if decision.Representative.Name != fleet.names[0] {
						t.Fatalf("rotation %d: representative = %q, want %q", rotation, decision.Representative.Name, fleet.names[0])
					}
				case t09Median:
					if decision.Status != rpc.Selected || len(decision.Support) != len(row.variants) || len(decision.Reject) != 0 {
						t.Fatalf("rotation %d: median decision = %+v", rotation, decision)
					}
					if decision.Representative.Name != fleet.names[0] {
						t.Fatalf("rotation %d: median representative = %q, want %q", rotation, decision.Representative.Name, fleet.names[0])
					}
				case t09Local:
					if decision.Status != rpc.Selected || len(decision.Support) != 1 || len(decision.Reject) != 0 {
						t.Fatalf("rotation %d: local decision = %+v", rotation, decision)
					}
					if decision.Representative != (rpc.SourceRef{}) {
						t.Fatalf("rotation %d: local representative = %+v, want zero", rotation, decision.Representative)
					}
					if decision.Support[0].Name != fleet.names[0] {
						t.Fatalf("rotation %d: local support = %q, want %q", rotation, decision.Support[0].Name, fleet.names[0])
					}
				}
				if len(decision.Participants) != len(row.variants) {
					t.Fatalf("rotation %d: participants = %d, want fixed basis %d", rotation, len(decision.Participants), len(row.variants))
				}
				if decision.Prepared == nil {
					t.Fatalf("rotation %d: prepared request missing", rotation)
				}
				t09AssertConservation(t, row, payloads[0], decision)
				t09AssertTypedRoundTrip(t, row, payloads[0], decision.Value)
			}
		})
	}
}

// TestT092DiagnosticsConservation sends one source whose out-of-domain vendor
// value differs from the quorum. The value must stay in Diagnostics, must not
// enter Value or Extensions, and its source still supports the core answer.
func TestT092DiagnosticsConservation(t *testing.T) {
	base := t09FeeHistory()
	outlier := t09SetField(t, base, "vendor", "other")
	fleet := t09NewFleet(t, 4)
	fleet.nodes[0].ReturnRaw(methods.MethodFeeHistory, base)
	fleet.nodes[1].ReturnRaw(methods.MethodFeeHistory, base)
	fleet.nodes[2].ReturnRaw(methods.MethodFeeHistory, base)
	fleet.nodes[3].ReturnRaw(methods.MethodFeeHistory, outlier)
	decision, err := fleet.client.Call(fleet.context(t), methods.MethodFeeHistory,
		uint64(2), fmt.Sprintf("0x%x", t09AnchorHeight), []float64{50})
	if err != nil {
		t.Fatal(err)
	}
	if decision.Status != rpc.Corroborated || len(decision.Support) != 4 {
		t.Fatalf("decision = %+v, want all four sources supporting the core value", decision)
	}
	if decision.Diagnostics == nil {
		t.Fatal("diagnostics missing")
	}
	var residual *rpc.FieldObservation
	for i := range decision.Diagnostics.Residuals {
		if decision.Diagnostics.Residuals[i].Path == "/vendor" {
			residual = &decision.Diagnostics.Residuals[i]
		}
	}
	if residual == nil || string(residual.Value) != `"other"` || len(residual.Sources) != 1 || residual.Sources[0].Name != fleet.names[3] {
		t.Fatalf("vendor residual = %+v", residual)
	}
	if decision.Extensions == nil || len(decision.Extensions.Fields) != 1 {
		t.Fatalf("extensions = %+v, want only the quorum vendor value", decision.Extensions)
	}
	extension := decision.Extensions.Fields[0]
	if extension.Path != "/vendor" || string(extension.Value) != `"t09"` || len(extension.Sources) != 3 {
		t.Fatalf("vendor extension = %+v", extension)
	}
	if bytes.Contains(decision.Value, []byte("vendor")) {
		t.Fatalf("vendor entered Value: %s", decision.Value)
	}
}

// TestT092ProtectedMutationFails mutates or deletes a protected field in one
// source of a four-member basis. The faulty source must not vote or shrink
// the denominator: two matching answers cannot reach Q = max(2, N/2+1) = 3.
func TestT092ProtectedMutationFails(t *testing.T) {
	for _, row := range t09FieldRows(t) {
		t.Run(row.method, func(t *testing.T) {
			fleet := t09NewFleet(t, 4)
			// Local stops at the first healthy source. A mutation after that
			// source could never be observed, so place it in the first slot;
			// the selected answer and the basis stay unchanged.
			faulty := 3
			if row.mutationExpect == t09MutationLocal {
				faulty = 0
			}
			healthy := []json.RawMessage{row.variants[0], row.variants[0], row.other}
			for i := 0; i < 4; i++ {
				switch {
				case i == faulty && row.mutationFault:
					continue
				case i == faulty:
					fleet.nodes[i].ReturnRaw(row.method, row.mutation)
				case faulty == 3:
					fleet.nodes[i].ReturnRaw(row.method, healthy[i])
				default:
					fleet.nodes[i].ReturnRaw(row.method, healthy[i-1])
				}
			}
			if row.mutationFault {
				fleet.failProvider(row.method, faulty)
			}
			decision, err := fleet.client.Call(fleet.context(t), row.method, row.params...)
			switch row.mutationExpect {
			case t09MutationStrict:
				if !errors.Is(err, rpc.ErrConflict) || decision.Status != rpc.Unknown {
					t.Fatalf("mutation was accepted: status %v err %v", decision.Status, err)
				}
				if len(decision.Support) != 2 || len(decision.Reject) != row.mutationReject {
					t.Fatalf("membership = support %d reject %d, want 2/%d", len(decision.Support), len(decision.Reject), row.mutationReject)
				}
			case t09MutationMedian:
				if err != nil || decision.Status != rpc.Selected {
					t.Fatalf("median mutation = status %v err %v", decision.Status, err)
				}
				if len(decision.Support) != 2 || len(decision.Reject) != 1 {
					t.Fatalf("median membership = support %d reject %d, want 2/1", len(decision.Support), len(decision.Reject))
				}
			case t09MutationLocal:
				if err != nil || decision.Status != rpc.Selected {
					t.Fatalf("local mutation = status %v err %v", decision.Status, err)
				}
				if len(decision.Support) != 1 || len(decision.Reject) != 0 {
					t.Fatalf("local membership = support %d reject %d, want 1/0", len(decision.Support), len(decision.Reject))
				}
			}
			for _, source := range append(append([]rpc.SourceRef(nil), decision.Support...), decision.Reject...) {
				if source.Name == fleet.names[faulty] {
					t.Fatalf("mutated source %q entered Support or Reject", source.Name)
				}
			}
			if len(decision.Faults) == 0 {
				t.Fatal("mutated source left no fault")
			}
			if row.fieldHint != "" {
				joined := ""
				for _, fault := range decision.Faults {
					joined += fault.Error() + "\n"
				}
				if !strings.Contains(strings.ToLower(joined), strings.ToLower(row.fieldHint)) {
					t.Fatalf("fault does not name the field %q:\n%s", row.fieldHint, joined)
				}
			}
			if row.mutationExpect == t09MutationLocal && row.mutationFault {
				found := false
				for _, fault := range decision.SourceFaults {
					if fault.Source.Name == fleet.names[faulty] {
						found = true
					}
				}
				if !found {
					t.Fatalf("local mutation source %q missing from SourceFaults: %+v", fleet.names[faulty], decision.SourceFaults)
				}
			}
			if len(decision.Value) != 0 {
				want, err := row.decode(t09SemanticJSON(t, row.variants[0]))
				if err != nil {
					t.Fatal(err)
				}
				got, err := row.decode(decision.Value)
				if err != nil || got != want {
					t.Fatalf("mutated decision Value = %s (%v), want canonical %q", decision.Value, err, want)
				}
			}
		})
	}
}

// TestT092EscapedHexAdmissionBoundary records the method boundary for the
// three responses admitted through geth's typed wire decoders: those decoders
// require a literal 0x prefix, so JSON-escaped hex is an admission fault, not
// a silent variant. The fault stays in the fixed basis and the cluster still
// reaches Q from the two literal sources without borrowing their fields.
func TestT092EscapedHexAdmissionBoundary(t *testing.T) {
	chain := t09NewChain(t)
	block, hash := chain.block(t09AnchorHeight, 1_700_000_000, 21_000)
	tx := chain.txObject(true, hash, t09AnchorHeight, 0)
	for _, tc := range []struct {
		method string
		params []any
		legal  json.RawMessage
		field  string
	}{
		{methods.MethodGetBlockByHash, []any{hash, true}, block, "block"},
		{methods.MethodGetBlockByNumber, []any{fmt.Sprintf("0x%x", t09AnchorHeight), true}, block, "block"},
		{methods.MethodGetTransactionByHash, []any{chain.txHash}, tx, "transaction"},
	} {
		t.Run(tc.method, func(t *testing.T) {
			fleet := t09NewFleet(t, 3)
			escaped := t09EscapeFirstHex(t, tc.legal)
			if bytes.Equal(escaped, tc.legal) {
				t.Fatal("escaped variant did not change the wire bytes")
			}
			fleet.nodes[0].ReturnRaw(tc.method, escaped)
			fleet.nodes[1].ReturnRaw(tc.method, tc.legal)
			fleet.nodes[2].ReturnRaw(tc.method, tc.legal)
			decision, err := fleet.client.Call(fleet.context(t), tc.method, tc.params...)
			if err != nil {
				t.Fatalf("literal sources did not carry the quorum: %v", err)
			}
			if decision.Status != rpc.Corroborated || len(decision.Support) != 2 || len(decision.Reject) != 0 {
				t.Fatalf("decision = %+v, want 2 supporting literal sources", decision)
			}
			if decision.Representative.Name != fleet.names[1] || decision.Support[0].Name != fleet.names[1] {
				t.Fatalf("representative/support = %q/%+v, want first literal source %q", decision.Representative.Name, decision.Support, fleet.names[1])
			}
			if len(decision.Faults) != 1 || !strings.Contains(strings.ToLower(decision.Faults[0].Error()), tc.field) {
				t.Fatalf("escaped source fault = %v, want %q admission fault", decision.Faults, tc.field)
			}
			want, err := t09DecodeForMethod(tc.method)(t09SemanticJSON(t, tc.legal))
			if err != nil {
				t.Fatal(err)
			}
			got, err := t09DecodeForMethod(tc.method)(decision.Value)
			if err != nil || got != want {
				t.Fatalf("Value = %s (%v), want %q", decision.Value, err, want)
			}
		})
	}
}

func t09DecodeForMethod(method string) func(json.RawMessage) (string, error) {
	switch method {
	case methods.MethodGetBlockByHash, methods.MethodGetBlockByNumber:
		return t09DecodeBlock
	case methods.MethodGetTransactionByHash:
		return t09DecodeTransaction
	default:
		panic("unexpected method " + method)
	}
}

// TestT092RoundTrip compares typed(raw) with typed(canonical) through the
// production Client/adapter. The canonical value comes from the production
// EncodeCheck/EncodeCommit path, not from a test-side encoder, and each source
// is exercised alone so no cluster can hide a per-source encoding difference.
func TestT092RoundTrip(t *testing.T) {
	for _, row := range t09FieldRows(t) {
		t.Run(row.method, func(t *testing.T) {
			for index, variant := range row.variants {
				fleet := t09NewFleet(t, 1)
				fleet.returnOne(row.method, variant)
				decision, err := fleet.client.Call(fleet.context(t), row.method, row.params...)
				if err != nil {
					t.Fatalf("variant %d: %v", index, err)
				}
				t09AssertTypedRoundTrip(t, row, variant, decision.Value)
			}
		})
	}
}

// t09AssertTypedRoundTrip feeds raw and canonical bytes to the method's
// production typed probe in separate one-source fleets. The comparison is the
// public typed result, not a test-side lossless summary.
func t09AssertTypedRoundTrip(t *testing.T, row t09FieldRow, raw, canonical json.RawMessage) {
	t.Helper()
	if row.typed == nil {
		t.Fatalf("%s: no production typed probe", row.method)
	}
	rawFleet := t09NewFleet(t, 1)
	rawFleet.returnOne(row.method, raw)
	want, err := row.typed(t, rawFleet, row.params)
	if err != nil {
		t.Fatalf("typed(raw %s): %v", trimValue(raw), err)
	}
	canonicalFleet := t09NewFleet(t, 1)
	canonicalFleet.returnOne(row.method, canonical)
	got, err := row.typed(t, canonicalFleet, row.params)
	if err != nil {
		t.Fatalf("typed(canonical %s): %v", trimValue(canonical), err)
	}
	if got != want {
		t.Fatalf("typed(raw) = %q, typed(canonical) = %q", want, got)
	}
}

// t09AssertConservation traces every field of one observation to a declared
// destination: the protected Value, a declared encoder omission, or (for
// upstream-local whole answers) the selected value itself.
func t09AssertConservation(t *testing.T, row t09FieldRow, raw json.RawMessage, decision rpc.Decision) {
	t.Helper()
	if row.whole {
		canonical, err := rpc.CanonicalJSON(raw)
		if err != nil {
			t.Fatal(err)
		}
		if string(decision.Value) != canonical {
			t.Fatalf("local whole answer = %s, want canonical %s", decision.Value, canonical)
		}
		return
	}
	if row.protected[0] == "$" {
		if len(decision.Value) == 0 {
			t.Fatal("scalar canonical Value is empty")
		}
		return
	}
	rawObject := t09FirstObject(t, raw)
	valueObject := t09FirstObject(t, decision.Value)
	for key := range rawObject {
		if !containsString(row.protected, key) && !containsString(row.outside, key) && !containsString(row.omitted, key) {
			t.Fatalf("field %q has no declared lifecycle destination", key)
		}
	}
	for key := range valueObject {
		if !containsString(row.protected, key) {
			t.Fatalf("Value contains undeclared field %q", key)
		}
	}
	for _, key := range row.protected {
		rawValue, present := rawObject[key]
		if !present {
			continue
		}
		_, inValue := valueObject[key]
		if bytes.Equal(bytes.TrimSpace(rawValue), []byte("null")) {
			if row.nullPreserved {
				if !inValue || !bytes.Equal(bytes.TrimSpace(valueObject[key]), []byte("null")) {
					t.Fatalf("null field %q was not preserved by the canonical encoder", key)
				}
				continue
			}
			// The canonical projection drops JSON null: the field has no
			// value, so its absence from Value is the declared destination.
			if inValue {
				t.Fatalf("null field %q entered Value", key)
			}
			continue
		}
		if !inValue {
			t.Fatalf("protected field %q disappeared from Value", key)
		}
	}
	for _, key := range row.outside {
		if _, present := valueObject[key]; present {
			t.Fatalf("outside field %q entered Value", key)
		}
	}
	for _, key := range row.omitted {
		if _, present := valueObject[key]; present {
			t.Fatalf("encoder-omitted field %q entered Value", key)
		}
	}
	if row.projector {
		found := false
		for _, field := range decision.Extensions.Fields {
			if strings.HasSuffix(field.Path, "/vendor") || field.Path == "/vendor" {
				found = true
				if len(field.Sources) != 3 {
					t.Fatalf("vendor extension sources = %d, want 3", len(field.Sources))
				}
			}
		}
		if !found {
			t.Fatalf("vendor field was not projected as an extension: %+v", decision.Extensions)
		}
	}
}

func t09FirstObject(t *testing.T, raw json.RawMessage) map[string]json.RawMessage {
	t.Helper()
	var rows []json.RawMessage
	if err := json.Unmarshal(raw, &rows); err == nil {
		if len(rows) == 0 {
			return map[string]json.RawMessage{}
		}
		raw = rows[0]
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil {
		t.Fatalf("decode object %s: %v", raw, err)
	}
	return object
}

// TestT092CallRevertValueAndContext pins the T08 split: provider code and
// message are context in Decision.Revert, while Value carries only the
// accepted data projection. Reordering the captured sources must not move
// Value; it moves the context and representative.
func TestT092CallRevertValueAndContext(t *testing.T) {
	const data = `"0xdead"`
	payloads := []struct {
		code    int
		message string
		data    json.RawMessage
	}{
		{3, "execution reverted", json.RawMessage(data)},
		{-32000, "execution reverted: reverted", json.RawMessage(`"0xDEAD"`)},
		{3, "Reverted", json.RawMessage(`"\u0030xdead"`)},
	}
	params := []any{map[string]any{"to": t09Address}, fmt.Sprintf("0x%x", t09AnchorHeight)}
	var canonical json.RawMessage
	for rotation := 0; rotation < len(payloads); rotation++ {
		fleet := t09NewFleet(t, len(payloads))
		for i, node := range fleet.nodes {
			p := payloads[(i+rotation)%len(payloads)]
			node.FailWithData(methods.MethodCall, p.code, p.message, p.data, true)
		}
		decision, err := fleet.client.Call(fleet.context(t), methods.MethodCall, params...)
		if err != nil {
			t.Fatalf("rotation %d: %v", rotation, err)
		}
		if decision.Status != rpc.Corroborated || decision.Revert == nil {
			t.Fatalf("rotation %d: decision = %+v", rotation, decision)
		}
		if canonical == nil {
			canonical = append(json.RawMessage(nil), decision.Value...)
		} else if !bytes.Equal(canonical, decision.Value) {
			t.Fatalf("rotation %d: Value moved: %s vs %s", rotation, canonical, decision.Value)
		}
		if string(decision.Value) != `{"data":"0xdead"}` {
			t.Fatalf("rotation %d: Value = %s, want only canonical data", rotation, decision.Value)
		}
		if bytes.Contains(decision.Value, []byte("code")) || bytes.Contains(decision.Value, []byte("message")) {
			t.Fatalf("rotation %d: provider context entered Value: %s", rotation, decision.Value)
		}
		want := payloads[rotation]
		if decision.Revert.Code != want.code || decision.Revert.Message != want.message {
			t.Fatalf("rotation %d: revert context = %+v, want %d/%q", rotation, decision.Revert, want.code, want.message)
		}
		if decision.Representative.Name != fleet.names[0] {
			t.Fatalf("rotation %d: representative = %q, want %q", rotation, decision.Representative.Name, fleet.names[0])
		}
		if len(decision.Dimensions) != 2 {
			t.Fatalf("rotation %d: dimensions = %+v", rotation, decision.Dimensions)
		}
		dataDimension := decision.Dimensions[1]
		if dataDimension.Name != "data" || !dataDimension.Returned || len(dataDimension.Support) != len(payloads) {
			t.Fatalf("rotation %d: data dimension = %+v", rotation, dataDimension)
		}
	}
}

// TestT092CallRevertNonHexDataCanonical extends the Value invariant to
// non-hex revert data: key order, whitespace, and numeric spelling are
// comparison-only differences, so rotating them through the pool must not
// move the canonical data bytes.
func TestT092CallRevertNonHexDataCanonical(t *testing.T) {
	payloads := []json.RawMessage{
		json.RawMessage(`{"b":2,"a":1}`),
		json.RawMessage(`{ "a": 1, "b": 2 }`),
		json.RawMessage(`{"a":1.0,"b":2}`),
	}
	params := []any{map[string]any{"to": t09Address}, fmt.Sprintf("0x%x", t09AnchorHeight)}
	for rotation := range payloads {
		fleet := t09NewFleet(t, len(payloads))
		for i, node := range fleet.nodes {
			node.FailWithData(methods.MethodCall, 3, "execution reverted", payloads[(i+rotation)%len(payloads)], true)
		}
		decision, err := fleet.client.Call(fleet.context(t), methods.MethodCall, params...)
		if err != nil {
			t.Fatalf("rotation %d: %v", rotation, err)
		}
		if decision.Status != rpc.Corroborated || string(decision.Value) != `{"data":{"a":1,"b":2}}` {
			t.Fatalf("rotation %d: decision = %+v, Value = %s", rotation, decision, decision.Value)
		}
		if decision.Dimensions[1].Returned != true || len(decision.Dimensions[1].Support) != len(payloads) {
			t.Fatalf("rotation %d: data dimension = %+v", rotation, decision.Dimensions[1])
		}
	}
}

// TestT092CallRevertWithheldDataConservation covers the reductive deletion
// destination: a short data cluster is removed from Value, its bytes stay in
// Provenance, and the sources remain in the fixed denominator.
func TestT092CallRevertWithheldDataConservation(t *testing.T) {
	fleet := t09NewFleet(t, 4)
	fleet.nodes[0].FailWithData(methods.MethodCall, 3, "execution reverted", json.RawMessage(`"0xaaaa"`), true)
	fleet.nodes[1].FailWithData(methods.MethodCall, -32000, "execution reverted", json.RawMessage(`"0xAAAA"`), true)
	fleet.nodes[2].FailWithData(methods.MethodCall, 3, "execution reverted", json.RawMessage(`"0xbbbb"`), true)
	fleet.nodes[3].FailWithData(methods.MethodCall, -32000, "execution reverted", json.RawMessage(`"0xBBBB"`), true)
	params := []any{map[string]any{"to": t09Address}, fmt.Sprintf("0x%x", t09AnchorHeight)}
	decision, err := fleet.client.Call(fleet.context(t), methods.MethodCall, params...)
	if err != nil {
		t.Fatal(err)
	}
	if decision.Status != rpc.Corroborated || decision.Revert == nil {
		t.Fatalf("decision = %+v", decision)
	}
	if len(decision.DeletedFields) != 1 || decision.DeletedFields[0] != "data" {
		t.Fatalf("deleted fields = %v, want [data]", decision.DeletedFields)
	}
	if bytes.Contains(decision.Value, []byte("aaaa")) || bytes.Contains(decision.Value, []byte("bbbb")) {
		t.Fatalf("withheld data stayed in Value: %s", decision.Value)
	}
	if !bytes.Contains(decision.Provenance, []byte("0xaaaa")) {
		t.Fatalf("withheld data missing from Provenance: %s", decision.Provenance)
	}
	if bytes.Contains(decision.Provenance, []byte("0xbbbb")) {
		t.Fatalf("Provenance spliced another source's data: %s", decision.Provenance)
	}
	if decision.Representative.Name != fleet.names[0] {
		t.Fatalf("representative = %q, want %q", decision.Representative.Name, fleet.names[0])
	}
	dataDimension := decision.Dimensions[1]
	if dataDimension.Returned || len(dataDimension.Support) != 2 || len(dataDimension.Reject) != 2 {
		t.Fatalf("data dimension = %+v", dataDimension)
	}
}

// TestT092CallRevertAbstainConservation separates missing, JSON null, empty
// bytes, and concrete data. Missing/null abstain and stay out of data Reject;
// the concrete split cannot reach Q, so data is withheld.
func TestT092CallRevertAbstainConservation(t *testing.T) {
	fleet := t09NewFleet(t, 4)
	fleet.nodes[0].FailWithData(methods.MethodCall, 3, "execution reverted", nil, false)
	fleet.nodes[1].FailWithData(methods.MethodCall, 3, "execution reverted", json.RawMessage(`null`), true)
	fleet.nodes[2].FailWithData(methods.MethodCall, 3, "execution reverted", json.RawMessage(`"0x"`), true)
	fleet.nodes[3].FailWithData(methods.MethodCall, 3, "execution reverted", json.RawMessage(`"0xdead"`), true)
	params := []any{map[string]any{"to": t09Address}, fmt.Sprintf("0x%x", t09AnchorHeight)}
	decision, err := fleet.client.Call(fleet.context(t), methods.MethodCall, params...)
	if err != nil {
		t.Fatal(err)
	}
	if decision.Status != rpc.Corroborated {
		t.Fatalf("status = %v", decision.Status)
	}
	dataDimension := decision.Dimensions[1]
	if len(dataDimension.Support) != 1 || len(dataDimension.Reject) != 1 || len(dataDimension.Abstain) != 2 || dataDimension.Returned {
		t.Fatalf("data dimension = %+v", dataDimension)
	}
	if dataDimension.Support[0].Name != fleet.names[2] {
		t.Fatalf("empty-data support = %+v, want %q", dataDimension.Support, fleet.names[2])
	}
	for _, source := range dataDimension.Abstain {
		if source.Name != fleet.names[0] && source.Name != fleet.names[1] {
			t.Fatalf("unexpected abstainer %q", source.Name)
		}
	}
	if len(decision.DeletedFields) != 1 || decision.DeletedFields[0] != "data" {
		t.Fatalf("deleted fields = %v", decision.DeletedFields)
	}
}

// TestT092LocalWholeAnswerShapeBoundary records the method-specific limit of
// the mutation invariant for upstream-local whole answers. T09 exercises only
// the top-level shapes each method declares (boolean for net_listening, false
// for eth_syncing); an illegal top-level shape is a policy fault and the typed
// path reports the same no-valid-response boundary. The syncing progress
// object is deliberately not synthesized here; pipeline 5.1.1 keeps that state
// a capture gap.
func TestT092LocalWholeAnswerShapeBoundary(t *testing.T) {
	t.Run(methods.MethodListening, func(t *testing.T) {
		fleet := t09NewFleet(t, 1)
		fleet.returnOne(methods.MethodListening, json.RawMessage(`"yes"`))
		decision, err := fleet.client.Call(fleet.context(t), methods.MethodListening)
		if !errors.Is(err, rpc.ErrNoValidResponse) || decision.Status != rpc.Unknown || len(decision.Value) != 0 {
			t.Fatalf("non-boolean listening = status %v value %s err %v", decision.Status, decision.Value, err)
		}
		if _, _, err := fleet.client.Listening(fleet.context(t)); !errors.Is(err, rpc.ErrNoValidResponse) {
			t.Fatalf("typed boundary error = %v, want no valid response", err)
		}
	})
	t.Run(methods.MethodSyncing, func(t *testing.T) {
		fleet := t09NewFleet(t, 1)
		fleet.returnOne(methods.MethodSyncing, json.RawMessage(`true`))
		decision, err := fleet.client.Call(fleet.context(t), methods.MethodSyncing)
		if !errors.Is(err, rpc.ErrNoValidResponse) || decision.Status != rpc.Unknown || len(decision.Value) != 0 {
			t.Fatalf("true syncing = status %v value %s err %v", decision.Status, decision.Value, err)
		}
		if _, _, err := fleet.client.Syncing(fleet.context(t)); !errors.Is(err, rpc.ErrNoValidResponse) {
			t.Fatalf("typed boundary error = %v, want no valid response", err)
		}
	})
}

func t09FieldRowFor(t *testing.T, method string) t09FieldRow {
	t.Helper()
	for _, row := range t09FieldRows(t) {
		if row.method == method {
			return row
		}
	}
	t.Fatalf("%s: no field-lifecycle row", method)
	return t09FieldRow{}
}

func t09SourceNames(refs []rpc.SourceRef) []string {
	out := make([]string, len(refs))
	for i, ref := range refs {
		out[i] = ref.Name
	}
	return out
}

// TestT092UpperPrefixSpelling covers the `0X` form where the method contract
// accepts it. Both eth_chainId and eth_getBalance use NormalizeWireQuantity,
// whose prefix comparison is case-insensitive; the canonical Value must still
// be the lowercase 0x form. Methods whose wire grammar requires the literal
// "0x" prefix are deliberately not extended here.
func TestT092UpperPrefixSpelling(t *testing.T) {
	for _, tc := range []struct {
		method string
		params []any
		raw    string
		want   string
	}{
		{methods.MethodChainID, nil, `"0X1"`, `"0x1"`},
		{methods.MethodGetBalance, []any{t09Address, "0x10"}, `"0X64"`, `"0x64"`},
	} {
		t.Run(tc.method, func(t *testing.T) {
			fleet := t09NewFleet(t, 3)
			fleet.returnOne(tc.method, json.RawMessage(tc.raw))
			decision, err := fleet.client.Call(fleet.context(t), tc.method, tc.params...)
			if err != nil {
				t.Fatal(err)
			}
			if decision.Status != rpc.Corroborated || string(decision.Value) != tc.want {
				t.Fatalf("decision = %+v, Value = %s, want %s", decision, decision.Value, tc.want)
			}
		})
	}
}
