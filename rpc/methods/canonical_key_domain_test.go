package methods

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"

	"ethguard/rpc"
	accesslistobj "ethguard/rpc/internal/objects/accesslist"
	feehistoryobj "ethguard/rpc/internal/objects/feehistory"
	logobj "ethguard/rpc/internal/objects/log"
	receiptobj "ethguard/rpc/internal/objects/receipt"
)

// The comparison key of these five methods is the canonical bytes (0047), so
// one product decides both what is compared and what is returned. Two
// properties hold at once and pull in opposite directions: a difference
// outside the protected domain must not split a cluster, and a difference
// inside it must. A key that was too coarse would fail the second; a key that
// was too fine, the whole payload for instance, would fail the first.
//
// These tests cannot catch a regression in where the key comes from. Both a
// too-coarse key and a too-fine key change clustering, but a different key
// source could reproduce the same clusters. The key source itself is guarded
// white-box in rpc/canonical_key_source_test.go; what is pinned here is the
// classification.
func TestMigratedMethodsClusterByProtectedDomain(t *testing.T) {
	agree := func(t *testing.T, policy rpc.Policy, a, b string) rpc.Decision {
		t.Helper()
		decision, err := policy.(rpc.CorroboratingPolicy).ResolveCorroborated(rpc.Corroboration{Floor: 1}, []rpc.Observation{
			observation(rpc.SourceRef{Name: "a", ID: "a"}, a, ""),
			observation(rpc.SourceRef{Name: "b", ID: "b"}, b, ""),
		})
		if err != nil {
			t.Fatal(err)
		}
		if decision.Status != rpc.Corroborated || len(decision.Support) != 2 || len(decision.Reject) != 0 || len(decision.Faults) != 0 {
			t.Fatalf("out-of-domain fields split the cluster: status %v support %d reject %d faults %v",
				decision.Status, len(decision.Support), len(decision.Reject), decision.Faults)
		}
		return decision
	}
	differ := func(t *testing.T, policy rpc.Policy, a, b string) {
		t.Helper()
		decision, err := policy.(rpc.CorroboratingPolicy).ResolveCorroborated(rpc.Corroboration{Floor: 1}, []rpc.Observation{
			observation(rpc.SourceRef{Name: "a", ID: "a"}, a, ""),
			observation(rpc.SourceRef{Name: "b", ID: "b"}, b, ""),
		})
		if !errors.Is(err, rpc.ErrConflict) || decision.Status != rpc.Unknown || len(decision.Support) != 1 || len(decision.Reject) != 1 || len(decision.Faults) != 0 {
			t.Fatalf("a protected field did not split the cluster: %+v err %v", decision, err)
		}
	}

	t.Run("eth_getTransactionReceipt", func(t *testing.T) {
		policy := TransactionReceiptPolicy(rpc.WithQuorum(1))
		base := sampleReceipt(t)
		marked := injectReceiptField(t, base, "mark", "1")
		decision := agree(t, policy, string(base), string(marked))
		if bytes.Contains(decision.Value, []byte(`"mark"`)) {
			t.Fatalf("a field outside the protected domain entered Value: %s", decision.Value)
		}
		differ(t, policy, string(base), string(replaceReceiptField(t, base, "gasUsed", `"0x1"`)))
	})

	t.Run("eth_getBlockReceipts", func(t *testing.T) {
		policy := BlockReceiptsPolicy(nil, rpc.WithQuorum(1))
		base := string(receiptArray(t, sampleReceipt(t)))
		marked := string(receiptArray(t, injectReceiptField(t, sampleReceipt(t), "mark", "1")))
		decision := agree(t, policy, base, marked)
		if bytes.Contains(decision.Value, []byte(`"mark"`)) {
			t.Fatalf("a field outside the protected domain entered Value: %s", decision.Value)
		}
		differ(t, policy, base, string(receiptArray(t, replaceReceiptField(t, sampleReceipt(t), "gasUsed", `"0x1"`))))
	})

	t.Run("eth_getLogs", func(t *testing.T) {
		policy := LogsPolicy(func(context.Context) (uint64, error) { return 1, nil }, rpc.WithQuorum(1))
		one := "0x0000000000000000000000000000000000000001"
		two := "0x0000000000000000000000000000000000000002"
		base := "[" + logEntry(one, "0x00ff", "0xa", "false", "") + "]"
		marked := "[" + logEntry(one, "0x00ff", "0xa", "false", `"mark":1,"vendor":"kept-out"`) + "]"
		decision := agree(t, policy, base, marked)
		if bytes.Contains(decision.Value, []byte(`"mark"`)) || bytes.Contains(decision.Value, []byte(`"vendor"`)) {
			t.Fatalf("a field outside the protected domain entered Value: %s", decision.Value)
		}
		differ(t, policy, base, "["+logEntry(two, "0x00ff", "0xa", "false", "")+"]")
	})

	t.Run("eth_createAccessList", func(t *testing.T) {
		policy := CreateAccessListPolicy(func(context.Context) (string, error) { return accessKey, nil }, rpc.WithQuorum(1))
		base := `{"accessList":[{"storageKeys":["` + accessKey + `"],"address":"` + accessAddress + `"}],"gasUsed":"0x5208"}`
		marked := `{"accessList":[{"storageKeys":["` + accessKey + `"],"address":"` + accessAddress + `"}],"gasUsed":"0x5208","mark":1,"vendor":"kept-out"}`
		other := `{"accessList":[{"storageKeys":["` + accessKey + `"],"address":"0x0000000000000000000000000000000000000abd"}],"gasUsed":"0x5208"}`
		decision := agree(t, policy, base, marked)
		if bytes.Contains(decision.Value, []byte(`"mark"`)) || bytes.Contains(decision.Value, []byte(`"vendor"`)) {
			t.Fatalf("a field outside the protected domain entered Value: %s", decision.Value)
		}
		differ(t, policy, base, other)
	})

	t.Run("eth_feeHistory", func(t *testing.T) {
		policy := FeeHistoryPolicy(func(context.Context) (uint64, error) { return 1, nil }, rpc.WithQuorum(1))
		base := feeHistory("0xa", "0.5", "0x10")
		marked := `{"mark":1,"vendor":"kept-out",` + strings.TrimPrefix(base, "{")
		other := `{"oldestBlock":"0xb","baseFeePerGas":["0x1","0x2"],"gasUsedRatio":[0.5],"reward":[["0x10"]]}`
		decision := agree(t, policy, base, marked)
		if bytes.Contains(decision.Value, []byte(`"mark"`)) || bytes.Contains(decision.Value, []byte(`"vendor"`)) {
			t.Fatalf("a field outside the protected domain entered Value: %s", decision.Value)
		}
		differ(t, policy, base, other)
	})
}

// The key is the encoder's output, so the encoder has to be a function of the
// payload alone. Go randomizes map iteration order, so an encoder that built
// its result by ranging a map would pass a single-shot test and fail under
// repetition. Encoding the same payload a hundred times must give the same
// bytes every time, because those bytes are now the comparison key: a key
// that moved between two otherwise identical sources would split a
// corroborated cluster.
func TestMigratedEncodersAreDeterministic(t *testing.T) {
	log := logFixture()
	receipt := replaceReceiptField(t, sampleReceipt(t), "logs", string(mustMarshal(t, []any{log})))
	receipt = replaceReceiptField(t, receipt, "logsBloom", string(mustMarshal(t, bloomOf(t, log))))
	cases := map[string]struct {
		encode func(json.RawMessage) (json.RawMessage, error)
		value  json.RawMessage
	}{
		"receipt":       {receiptobj.Encode, receipt},
		"blockReceipts": {receiptobj.EncodeBlock, receiptArray(t, receipt)},
		"logs":          {logobj.Encode, json.RawMessage("[" + logEntry("0x0000000000000000000000000000000000000001", "0x00ff", "0xa", "false", `"mark":1,"vendor":"0xFF"`) + "]")},
		"accessList":    {accesslistobj.Encode, json.RawMessage(`{"accessList":[{"storageKeys":["` + accessKey + `","` + accessKeyTwo + `"],"address":"` + accessAddress + `"}],"error":"reverted","gasUsed":"0x5208","mark":1,"vendor":"0xFF"}`)},
		"feeHistory":    {feehistoryobj.Encode, json.RawMessage(feeHistory("0xa", "0.5", "0x10"))},
	}
	for name, item := range cases {
		t.Run(name, func(t *testing.T) {
			first, err := item.encode(item.value)
			if err != nil {
				t.Fatal(err)
			}
			for i := 0; i < 100; i++ {
				again, err := item.encode(item.value)
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(again, first) {
					t.Fatalf("encoding %d of 100 moved the bytes\nfirst %s\nagain  %s", i+2, first, again)
				}
			}
		})
	}
}

// logFixture is one log the receipt encoder will accept: it carries the hashes
// of the receipt that owns it, so the per-log binding holds.
func logFixture() map[string]any {
	return map[string]any{
		"address":          "0x0000000aa232009084bd71a5797d089aa4edfad4",
		"topics":           []string{"0x" + strings.Repeat("11", 32), "0x" + strings.Repeat("22", 32)},
		"data":             "0xdeadbeef",
		"blockNumber":      "0x18d592f",
		"transactionIndex": "0x0",
		"transactionHash":  "0xaa1c6c878d686b6c14b77c6b78ff23ae4b069cf941736678e9c1f8102b219b15",
		"blockHash":        "0x9bc1666a9b54a70b8fc7f4caf052c25d2bef94897c2815dc168163d6bc188b1e",
		"logIndex":         "0x1",
		"removed":          false,
		"blockTimestamp":   "0x64",
		"mark":             1,
		"vendor":           "0xFF",
	}
}

// bloomOf is geth's own bloom over the fixture's log, so the receipt encoder
// accepts the payload instead of rejecting a mismatched bloom.
func bloomOf(t *testing.T, log map[string]any) string {
	t.Helper()
	entry := &types.Log{Address: common.HexToAddress(log["address"].(string)), Data: common.FromHex(log["data"].(string))}
	for _, topic := range log["topics"].([]string) {
		entry.Topics = append(entry.Topics, common.HexToHash(topic))
	}
	return hexutil.Encode(types.CreateBloom(&types.Receipt{Logs: []*types.Log{entry}}).Bytes())
}

func mustMarshal(t *testing.T, value any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// Since decision 0047 the key is the encoder's output, so cluster() no longer
// calls the normalizer for these five methods. That moved a check rather than
// removing one: a normalizer that grew stricter than its encoder would stop
// rejecting a payload in cluster() while usableAnswer, the anchor validation
// and the pending path still call Normalize. The same source would then vote
// in a cluster and be marked Failed as an unusable answer, and no fault would
// explain the contradiction, because neither side is wrong on its own terms.
//
// The direction that matters is therefore one way: anything the encoder
// accepts, the normalizer must accept too. The other direction is already
// accounted for. A payload the normalizer accepts and the encoder rejects
// becomes an encode fault inside the denominator, which is the pre-existing
// EncodeCheck path and is not asserted here.
//
// This is a live test rather than a recorded argument, because the two
// functions are separate pieces of code that only this relation keeps in
// agreement, and tightening either one is exactly the change that breaks it.
func TestMigratedNormalizersAcceptWhatEncodersAccept(t *testing.T) {
	receipt := sampleReceipt(t)
	withLog := injectReceiptField(t, receipt, "logs",
		`[{"address":"0x0000000aa232009084bd71a5797d089aa4edfad4","topics":[],"data":"0x","blockNumber":"0x18d592f","transactionIndex":"0x0","transactionHash":"0xaa1c6c878d686b6c14b77c6b78ff23ae4b069cf941736678e9c1f8102b219b15","blockHash":"0x9bc1666a9b54a70b8fc7f4caf052c25d2bef94897c2815dc168163d6bc188b1e","logIndex":"0x1","removed":false}]`)
	// The captured receipt carries the empty-log bloom, so a receipt with a
	// real log needs one that matches it, or the encoder refuses the payload
	// and the row proves nothing.
	logged := replaceReceiptField(t, withLog, "logsBloom", string(mustMarshal(t, bloomOf(t, logFixture()))))
	badBloom := replaceReceiptField(t, receipt, "logsBloom", `"0x`+strings.Repeat("00", 255)+`01"`)
	logs := "[" + logEntry("0x0000000000000000000000000000000000000001", "0x00ff", "0xa", "false", "") + "]"
	access := `{"accessList":[{"storageKeys":["` + accessKey + `"],"address":"` + accessAddress + `"}],"gasUsed":"0x5208"}`
	fee := feeHistory("0xa", "0.5", "0x10")

	cases := []struct {
		method    string
		encode    func(json.RawMessage) (json.RawMessage, error)
		normalize func(json.RawMessage) (string, error)
		payloads  map[string]json.RawMessage
	}{
		{
			method: MethodGetTransactionReceipt,
			encode: receiptobj.Encode, normalize: normalizeReceiptObject,
			payloads: map[string]json.RawMessage{
				"captured":      receipt,
				"with a log":    logged,
				"uppercase hex": foldReceiptHex(t, receipt),
				"no from":       deleteReceiptField(t, receipt, "from"),
				"null":          json.RawMessage(`null`),
				"empty object":  json.RawMessage(`{}`),
				"one field":     json.RawMessage(`{"status":"0x1"}`),
				"array":         json.RawMessage(`[]`),
				"bare string":   json.RawMessage(`"0x1"`),
				"not json":      json.RawMessage(`{`),
				"no gasUsed":    deleteReceiptField(t, receipt, "gasUsed"),
				"logs null":     replaceReceiptField(t, receipt, "logs", "null"),
				"status null":   replaceReceiptField(t, receipt, "status", "null"),
				"no status":     deleteReceiptField(t, receipt, "status"),
				"leading zero":  replaceReceiptField(t, receipt, "transactionIndex", `"0x01"`),
				"bad bloom":     badBloom,
			},
		},
		{
			method: MethodGetBlockReceipts,
			encode: receiptobj.EncodeBlock, normalize: normalizeBlockReceiptList,
			payloads: map[string]json.RawMessage{
				"one receipt":  receiptArray(t, receipt),
				"array null":   json.RawMessage(`[null]`),
				"empty array":  json.RawMessage(`[]`),
				"null":         json.RawMessage(`null`),
				"object":       json.RawMessage(`{}`),
				"bare string":  json.RawMessage(`"x"`),
				"array number": json.RawMessage(`[1]`),
				"not json":     json.RawMessage(`[`),
				"no logs":      receiptArray(t, deleteReceiptField(t, receipt, "logs")),
			},
		},
		{
			method: MethodGetLogs,
			encode: logobj.Encode, normalize: normalizeLogs,
			payloads: map[string]json.RawMessage{
				"one log":     json.RawMessage(logs),
				"empty array": json.RawMessage(`[]`),
				"null":        json.RawMessage(`null`),
				"object":      json.RawMessage(`{}`),
				"bare string": json.RawMessage(`"x"`),
				"not json":    json.RawMessage(`[`),
				"no address":  json.RawMessage(`[{"topics":[],"data":"0x","blockNumber":"0xa","transactionHash":"0x1","transactionIndex":"0x0","blockHash":"0x2","logIndex":"0x0","removed":false}]`),
			},
		},
		{
			method: MethodCreateAccessList,
			encode: accesslistobj.Encode, normalize: normalizeAccessList,
			payloads: map[string]json.RawMessage{
				"with items":   json.RawMessage(access),
				"empty list":   json.RawMessage(`{"accessList":[],"gasUsed":"0x1"}`),
				"error text":   json.RawMessage(`{"accessList":[],"error":"reverted","gasUsed":"0x1"}`),
				"null":         json.RawMessage(`null`),
				"empty object": json.RawMessage(`{}`),
				"bare string":  json.RawMessage(`"x"`),
				"not json":     json.RawMessage(`{`),
				"leading zero": json.RawMessage(`{"accessList":[],"gasUsed":"0x01"}`),
			},
		},
		{
			method: MethodFeeHistory,
			encode: feehistoryobj.Encode, normalize: normalizeFeeHistory,
			payloads: map[string]json.RawMessage{
				"full":         json.RawMessage(fee),
				"no reward":    json.RawMessage(feeHistory("0xa", "0.5", "")),
				"null":         json.RawMessage(`null`),
				"empty object": json.RawMessage(`{}`),
				"bare string":  json.RawMessage(`"x"`),
				"not json":     json.RawMessage(`{`),
				"short base":   json.RawMessage(`{"oldestBlock":"0x1","baseFeePerGas":["0x1"],"gasUsedRatio":[0.5]}`),
				"ratio range":  json.RawMessage(`{"oldestBlock":"0x1","baseFeePerGas":["0x1","0x2"],"gasUsedRatio":[1.5]}`),
				"leading zero": json.RawMessage(`{"oldestBlock":"0x01","baseFeePerGas":["0x1","0x2"],"gasUsedRatio":[0.5]}`),
			},
		},
	}
	for _, item := range cases {
		t.Run(item.method, func(t *testing.T) {
			accepted := 0
			for name, payload := range item.payloads {
				if _, encErr := item.encode(payload); encErr != nil {
					continue // the encoder rejects it, so containment says nothing
				}
				accepted++
				if _, normErr := item.normalize(payload); normErr != nil {
					t.Errorf("%s/%s: encoder accepted, normalizer rejected: %v", item.method, name, normErr)
				}
			}
			// Rows that only exercise the rejection path would make the
			// assertion vacuous, so each method needs payloads the encoder
			// actually takes.
			if accepted == 0 {
				t.Fatalf("%s: the encoder accepted nothing in the table, so nothing was checked", item.method)
			}
			t.Logf("%d of %d payloads accepted by the encoder, all accepted by the normalizer", accepted, len(item.payloads))
		})
	}
}
