package methods

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"testing"

	"ethguard/rpc"
	accesslistobj "ethguard/rpc/internal/objects/accesslist"
	feehistoryobj "ethguard/rpc/internal/objects/feehistory"
	logobj "ethguard/rpc/internal/objects/log"
	receiptobj "ethguard/rpc/internal/objects/receipt"
)

// admissionCase is everything a method must satisfy once it is admitted to
// canonical encoding. Onboarding a method is one row, not one test: the four
// drivers below read this table and nothing else, so a method cannot be added
// to one property and forgotten in another.
//
// The four uniform properties are the ones that hold for every method whose
// comparison key is its canonical bytes, so the key is what is compared and
// what is returned at once. A difference outside the protected domain must not
// split a cluster, a difference inside it must, anything the encoder accepts
// its normalizer must accept, and the encoder must be a function of the
// payload alone.
//
// This table serves that one prototype only. Two others exist and are not
// representable here yet. A narrow-key object, which is what the two block
// methods become when they are re-encoded, would declare keySource normalizer
// and keep the same outside and inside meanings, since a key narrower than the
// value is the point, but it still needs a rejection test through a real block
// policy, which 0047 records as open. An admission-bound object declares
// keySource normalizer and has no encoder today, so encode, normalize and valid
// do not apply to it until it is re-encoded. A multi-dimensional key cannot be
// expressed by a single-key signature at all. The keySource field is here
// already so those rows can arrive without reshaping the table.
type admissionCase struct {
	method    string
	policy    func(...rpc.StrictOption) rpc.Policy
	encode    func(json.RawMessage) (json.RawMessage, error)
	normalize func(json.RawMessage) (string, error)

	valid   json.RawMessage   // the encoder accepts it and so does the normalizer
	more    []json.RawMessage // other payloads both accept, so containment is not checked on one byte string
	outside json.RawMessage   // differs only outside the protected domain: one cluster, identical Value
	inside  json.RawMessage   // differs inside the protected domain: two clusters
	reject  []json.RawMessage // the encoder must refuse every one of these

	keySource string                      // MethodRule().KeySource, the declaration cluster() reads
	edges     map[string]func(*testing.T) // shape rules specific to this method, optional
}

// admissionCases is the single list of methods under canonical encoding with
// the key equal to the canonical bytes, in lexicographic order. Anchors are
// bound here so the drivers can treat every policy as the same signature.
func admissionCases(t *testing.T) []admissionCase {
	t.Helper()

	// The captured receipt, the payload every receipt row is derived from.
	receipt := sampleReceipt(t)
	// A receipt carrying one real log, with geth's bloom over that log, since
	// the captured receipt ships the empty-log bloom and the encoder checks the
	// two against each other.
	loggedLog := map[string]any{
		"address":          "0x0000000aa232009084bd71a5797d089aa4edfad4",
		"topics":           []string{"0x" + strings.Repeat("11", 32), "0x" + strings.Repeat("22", 32)},
		"data":             "0x",
		"blockNumber":      "0x18d592f",
		"transactionIndex": "0x0",
		"transactionHash":  "0xaa1c6c878d686b6c14b77c6b78ff23ae4b069cf941736678e9c1f8102b219b15",
		"blockHash":        "0x9bc1666a9b54a70b8fc7f4caf052c25d2bef94897c2815dc168163d6bc188b1e",
		"logIndex":         "0x1",
		"removed":          false,
	}
	logged := replaceReceiptField(t, receipt, "logs", string(mustMarshal(t, []any{loggedLog})))
	logged = replaceReceiptField(t, logged, "logsBloom", string(mustMarshal(t, bloomOf(t, loggedLog))))

	badBloom := replaceReceiptField(t, receipt, "logsBloom", `"0x`+strings.Repeat("00", 255)+`01"`)
	logAddress := "0x0000000000000000000000000000000000000001"
	log := "[" + logEntry(logAddress, "0x00ff", "0xa", "false", "") + "]"
	// Two storage keys, not one: an encoder that collected them through a map
	// would be deterministic by accident on a single-element map, and the
	// determinism driver would never see it move.
	accessItem := `{"storageKeys":["` + accessKey + `","` + accessKeyTwo + `"],"address":"` + accessAddress + `"}`
	fee := feeHistory("0xa", "0.5", "0x10")
	access := `{"accessList":[` + accessItem + `],"gasUsed":"0x5208"}`

	return []admissionCase{
		{
			method: MethodCreateAccessList,
			policy: func(opts ...rpc.StrictOption) rpc.Policy {
				return CreateAccessListPolicy(func(context.Context) (string, error) { return accessKey, nil }, opts...)
			},
			encode:    accesslistobj.Encode,
			normalize: normalizeAccessList,
			valid:     json.RawMessage(access),
			more: []json.RawMessage{
				json.RawMessage(`{"accessList":[],"gasUsed":"0x1"}`),
				json.RawMessage(`{"accessList":[],"error":"reverted","gasUsed":"0x1"}`),
			},
			outside: json.RawMessage(`{"accessList":[` + accessItem + `],"gasUsed":"0x5208","mark":1,"vendor":"kept-out"}`),
			inside:  json.RawMessage(`{"accessList":[{"storageKeys":["` + accessKey + `"],"address":"0x0000000000000000000000000000000000000abd"}],"gasUsed":"0x5208"}`),
			reject: []json.RawMessage{
				json.RawMessage(`null`),
				json.RawMessage(`{}`),
				json.RawMessage(`"x"`),
				json.RawMessage(`{`),
				json.RawMessage(`{"accessList":[],"gasUsed":"0x01"}`),
				json.RawMessage(`{"accessList":null}`),
				json.RawMessage(`{"accessList":[{"address":"` + accessAddress + `"}]}`),
			},
			keySource: rpc.RuleKeyCanonicalBytes,
			edges: map[string]func(*testing.T){
				"error text keeps its spelling": func(t *testing.T) {
					// error is text, not hex. It must survive the encoder and
					// the key byte for byte, case included, or a reworded
					// revert message would split a cluster on wording.
					const spelled = "Reverted: BAD Instruction"
					encoded, err := accesslistobj.Encode(json.RawMessage(`{"accessList":[],"error":"` + spelled + `","gasUsed":"0x1"}`))
					if err != nil {
						t.Fatal(err)
					}
					if !bytes.Contains(encoded, []byte(`"error":"`+spelled+`"`)) {
						t.Fatalf("error was rewritten: %s", encoded)
					}
					key, err := normalizeAccessList(json.RawMessage(`{"accessList":[],"error":"` + spelled + `","gasUsed":"0x1"}`))
					if err != nil || !strings.Contains(key, spelled) {
						t.Fatalf("key = %s err %v", key, err)
					}
				},
			},
		},
		{
			method: MethodFeeHistory,
			policy: func(opts ...rpc.StrictOption) rpc.Policy {
				return FeeHistoryPolicy(func(context.Context) (uint64, error) { return 1, nil }, opts...)
			},
			encode:    feehistoryobj.Encode,
			normalize: normalizeFeeHistory,
			valid:     json.RawMessage(fee),
			more:      []json.RawMessage{json.RawMessage(feeHistory("0xa", "0.5", ""))},
			outside:   json.RawMessage(`{"mark":1,"vendor":"kept-out",` + strings.TrimPrefix(fee, "{")),
			inside:    json.RawMessage(`{"oldestBlock":"0xb","baseFeePerGas":["0x1","0x2"],"gasUsedRatio":[0.5],"reward":[["0x10"]]}`),
			reject: []json.RawMessage{
				json.RawMessage(`null`),
				json.RawMessage(`{}`),
				json.RawMessage(`"x"`),
				json.RawMessage(`{`),
				json.RawMessage(`{"oldestBlock":"0x1","baseFeePerGas":["0x1"],"gasUsedRatio":[0.5]}`),
				json.RawMessage(`{"oldestBlock":"0x1","baseFeePerGas":["0x1","0x2"],"gasUsedRatio":[1.5]}`),
				json.RawMessage(`{"oldestBlock":"0x01","baseFeePerGas":["0x1","0x2"],"gasUsedRatio":[0.5]}`),
				json.RawMessage(`{"oldestBlock":"0x1","baseFeePerGas":["0x1","0x2"],"gasUsedRatio":[0.5],"reward":[["0x1"],["0x1","0x2"]]}`),
			},
			keySource: rpc.RuleKeyCanonicalBytes,
			edges: map[string]func(*testing.T){
				"ratio spellings are one value": func(t *testing.T) {
					// The shortest decimal form decides, so 0.5 and 0.50 are one
					// answer and so are 1e-1 and 0.1. Both pairs must land in
					// one cluster with the same Value, or a provider that
					// writes the longer form would read as dissent.
					for _, pair := range [][2]string{
						{`{"oldestBlock":"0xa","baseFeePerGas":["0x1","0x2"],"gasUsedRatio":[0.5]}`, `{"oldestBlock":"0xa","baseFeePerGas":["0x1","0x2"],"gasUsedRatio":[0.50]}`},
						{`{"oldestBlock":"0xa","baseFeePerGas":["0x1","0x2"],"gasUsedRatio":[1e-1]}`, `{"oldestBlock":"0xa","baseFeePerGas":["0x1","0x2"],"gasUsedRatio":[0.1]}`},
					} {
						decision, err := FeeHistoryPolicy(func(context.Context) (uint64, error) { return 1, nil }, rpc.WithQuorum(1)).(rpc.CorroboratingPolicy).
							ResolveCorroborated(rpc.Corroboration{Floor: 1}, []rpc.Observation{
								observation(rpc.SourceRef{Name: "a", ID: "a"}, pair[0], ""),
								observation(rpc.SourceRef{Name: "b", ID: "b"}, pair[1], ""),
							})
						if err != nil || decision.Status != rpc.Corroborated || len(decision.Support) != 2 {
							t.Fatalf("%s and %s did not form one cluster: %+v err %v", pair[0], pair[1], decision, err)
						}
						if !bytes.Contains(decision.Value, []byte(`"gasUsedRatio":[0.5]`)) && !bytes.Contains(decision.Value, []byte(`"gasUsedRatio":[0.1]`)) {
							t.Fatalf("ratio was not reduced to its shortest form: %s", decision.Value)
						}
					}
				},
			},
		},
		{
			method:    MethodGetBlockReceipts,
			policy:    func(opts ...rpc.StrictOption) rpc.Policy { return BlockReceiptsPolicy(nil, opts...) },
			encode:    receiptobj.EncodeBlock,
			normalize: normalizeBlockReceiptList,
			valid:     receiptArray(t, receipt),
			more:      []json.RawMessage{json.RawMessage(`[]`)},
			outside:   receiptArray(t, injectReceiptField(t, receipt, "mark", "1")),
			inside:    receiptArray(t, replaceReceiptField(t, receipt, "gasUsed", `"0x1"`)),
			reject: []json.RawMessage{
				json.RawMessage(`[null]`),
				json.RawMessage(`{}`),
				json.RawMessage(`"x"`),
				json.RawMessage(`[1]`),
				json.RawMessage(`[`),
				receiptArray(t, deleteReceiptField(t, receipt, "logs")),
				receiptArray(t, receipt, json.RawMessage("null")),
			},
			keySource: rpc.RuleKeyCanonicalBytes,
			edges: map[string]func(*testing.T){
				"null and empty array stay distinct": func(t *testing.T) {
					// An absent block and an empty block are different answers.
					// Folding them together would let a source report no
					// receipts for a block that has them.
					decision, err := BlockReceiptsPolicy(nil, rpc.WithQuorum(1)).(rpc.CorroboratingPolicy).
						ResolveCorroborated(rpc.Corroboration{Floor: 1}, []rpc.Observation{
							observation(rpc.SourceRef{Name: "a", ID: "a"}, `null`, ""),
							observation(rpc.SourceRef{Name: "b", ID: "b"}, `[]`, ""),
						})
					if !errors.Is(err, rpc.ErrConflict) || decision.Status != rpc.Unknown {
						t.Fatalf("null and [] formed one answer: %+v err %v", decision, err)
					}
				},
				"a null element fails the whole array and keeps the source in the denominator": func(t *testing.T) {
					// Four sources, floor 2, so the required support is 3 and
					// two matching answers cannot reach it. The array with a
					// null element must become a fault rather than being
					// dropped, because dropping it would leave a denominator of
					// 3 where the same two votes would carry.
					good := receiptArray(t, receipt)
					other := receiptArray(t, replaceReceiptField(t, receipt, "gasUsed", `"0x1"`))
					broken := receiptArray(t, receipt, json.RawMessage("null"))
					decision, err := BlockReceiptsPolicy(nil, rpc.WithQuorum(2)).(rpc.CorroboratingPolicy).
						ResolveCorroborated(rpc.Corroboration{Floor: 2}, []rpc.Observation{
							observation(rpc.SourceRef{Name: "a", ID: "a"}, string(good), ""),
							observation(rpc.SourceRef{Name: "b", ID: "b"}, string(good), ""),
							observation(rpc.SourceRef{Name: "c", ID: "c"}, string(other), ""),
							observation(rpc.SourceRef{Name: "d", ID: "d"}, string(broken), ""),
						})
					if !errors.Is(err, rpc.ErrConflict) || decision.Status != rpc.Unknown {
						t.Fatalf("decision = %+v err %v", decision, err)
					}
					if len(decision.Support) != 2 || len(decision.Reject) != 1 {
						t.Fatalf("membership = support %d reject %d", len(decision.Support), len(decision.Reject))
					}
					if len(decision.Faults) != 1 || !strings.Contains(decision.Faults[0].Error(), "encode:") {
						t.Fatalf("faults = %v, want one encode fault", decision.Faults)
					}
				},
				"array order is not sorted": func(t *testing.T) {
					other := replaceReceiptField(t, receipt, "gasUsed", `"0x1"`)
					first := receiptArray(t, receipt, other)
					second := receiptArray(t, other, receipt)
					decision, err := BlockReceiptsPolicy(nil, rpc.WithQuorum(1)).(rpc.CorroboratingPolicy).
						ResolveCorroborated(rpc.Corroboration{Floor: 1}, []rpc.Observation{
							observation(rpc.SourceRef{Name: "a", ID: "a"}, string(first), ""),
							observation(rpc.SourceRef{Name: "b", ID: "b"}, string(second), ""),
						})
					if !errors.Is(err, rpc.ErrConflict) || decision.Status != rpc.Unknown {
						t.Fatalf("reordering the array formed one answer: %+v err %v", decision, err)
					}
				},
			},
		},
		{
			method: MethodGetLogs,
			policy: func(opts ...rpc.StrictOption) rpc.Policy {
				return LogsPolicy(func(context.Context) (uint64, error) { return 1, nil }, opts...)
			},
			encode:    logobj.Encode,
			normalize: normalizeLogs,
			valid:     json.RawMessage(log),
			more:      []json.RawMessage{json.RawMessage(`[]`)},
			outside:   json.RawMessage("[" + logEntry(logAddress, "0x00ff", "0xa", "false", `"mark":1,"vendor":"kept-out"`) + "]"),
			inside:    json.RawMessage("[" + logEntry("0x0000000000000000000000000000000000000002", "0x00ff", "0xa", "false", "") + "]"),
			reject: []json.RawMessage{
				json.RawMessage(`null`),
				json.RawMessage(`{}`),
				json.RawMessage(`"x"`),
				json.RawMessage(`[`),
				json.RawMessage(`[{"topics":[],"data":"0x","blockNumber":"0xa","transactionHash":"0x1","transactionIndex":"0x0","blockHash":"0x2","logIndex":"0x0","removed":false}]`),
			},
			keySource: rpc.RuleKeyCanonicalBytes,
			edges: map[string]func(*testing.T){
				"null is an illegal answer, not an empty array": func(t *testing.T) {
					// The two must not corroborate. If null read as an empty
					// array, a source that failed to answer would be able to
					// vouch for a block with no logs.
					decision, err := LogsPolicy(func(context.Context) (uint64, error) { return 1, nil }, rpc.WithQuorum(1)).(rpc.CorroboratingPolicy).
						ResolveCorroborated(rpc.Corroboration{Floor: 1}, []rpc.Observation{
							observation(rpc.SourceRef{Name: "a", ID: "a"}, `null`, ""),
							observation(rpc.SourceRef{Name: "b", ID: "b"}, `[]`, ""),
						})
					if !errors.Is(err, rpc.ErrConflict) || decision.Status == rpc.Corroborated || len(decision.Support) != 1 {
						t.Fatalf("null corroborated with an empty array: %+v err %v", decision, err)
					}
					// The empty array is the only answer; null is a fault, so
					// it never became a second vote.
					if len(decision.Faults) != 1 || !strings.Contains(decision.Faults[0].Error(), "encode:") {
						t.Fatalf("faults = %v, want one encode fault for null", decision.Faults)
					}
				},
				"an absent blockTimestamp is omitted": func(t *testing.T) {
					encoded, err := logobj.Encode(json.RawMessage(log))
					if err != nil {
						t.Fatal(err)
					}
					if bytes.Contains(encoded, []byte(`"blockTimestamp"`)) {
						t.Fatalf("an absent blockTimestamp entered Value: %s", encoded)
					}
				},
			},
		},
		{
			method:    MethodGetTransactionReceipt,
			policy:    func(opts ...rpc.StrictOption) rpc.Policy { return TransactionReceiptPolicy(opts...) },
			encode:    receiptobj.Encode,
			normalize: normalizeReceiptObject,
			valid:     receipt,
			// Containment has to hold for real spellings, not only for the
			// one payload the valid column carries. A receipt with a log, one
			// with uppercase hex and one with no from are all answers, so the
			// normalizer must take them too. Without these rows the relation
			// would only ever be checked on a single byte string, and a
			// normalizer tightened to require from would pass.
			more:    []json.RawMessage{logged, foldReceiptHex(t, receipt), deleteReceiptField(t, receipt, "from")},
			outside: injectReceiptField(t, receipt, "mark", "1"),
			inside:  replaceReceiptField(t, receipt, "gasUsed", `"0x1"`),
			reject: []json.RawMessage{
				json.RawMessage(`{}`),
				json.RawMessage(`{"status":"0x1"}`),
				json.RawMessage(`[]`),
				json.RawMessage(`"0x1"`),
				json.RawMessage(`{`),
				deleteReceiptField(t, receipt, "gasUsed"),
				replaceReceiptField(t, receipt, "logs", "null"),
				replaceReceiptField(t, receipt, "status", "null"),
				deleteReceiptField(t, receipt, "status"),
				replaceReceiptField(t, receipt, "transactionIndex", `"0x01"`),
				badBloom,
			},
			keySource: rpc.RuleKeyCanonicalBytes,
			edges: map[string]func(*testing.T){
				"from stays in both the key and the Value": func(t *testing.T) {
					raw := injectReceiptField(t, receipt, "from", `"0x`+strings.Repeat("ab", 20)+`"`)
					encoded, err := receiptobj.Encode(raw)
					if err != nil {
						t.Fatal(err)
					}
					if !bytes.Contains(encoded, []byte(`"from"`)) {
						t.Fatalf("from was dropped from Value: %s", encoded)
					}
					key, err := normalizeReceiptObject(raw)
					if err != nil || !strings.Contains(key, `"from"`) {
						t.Fatalf("key dropped from = %s err %v", key, err)
					}
				},
				"null encodes as null": func(t *testing.T) {
					encoded, err := receiptobj.Encode(json.RawMessage(`null`))
					if err != nil || string(encoded) != "null" {
						t.Fatalf("null encoded as %s err %v", encoded, err)
					}
					key, err := normalizeReceiptObject(json.RawMessage(`null`))
					if err != nil || key != "null" {
						t.Fatalf("null keyed as %s err %v", key, err)
					}
				},
			},
		},
	}
}

// TestAdmissionProtectedDomainClustering pins the two halves of one key. A
// difference outside the protected domain must not split a cluster and must
// not reach Value; a difference inside it must split. A key too coarse fails
// the second, a key too fine, the whole payload for instance, fails the first.
func TestAdmissionProtectedDomainClustering(t *testing.T) {
	for _, item := range admissionCases(t) {
		t.Run(item.method, func(t *testing.T) {
			policy := item.policy(rpc.WithQuorum(1))
			resolve := func(a, b string) (rpc.Decision, error) {
				return policy.(rpc.CorroboratingPolicy).ResolveCorroborated(rpc.Corroboration{Floor: 1}, []rpc.Observation{
					observation(rpc.SourceRef{Name: "a", ID: "a"}, a, ""),
					observation(rpc.SourceRef{Name: "b", ID: "b"}, b, ""),
				})
			}
			decision, err := resolve(string(item.valid), string(item.outside))
			if err != nil {
				t.Fatal(err)
			}
			if decision.Status != rpc.Corroborated || len(decision.Support) != 2 || len(decision.Reject) != 0 || len(decision.Faults) != 0 {
				t.Fatalf("out-of-domain fields split the cluster: status %v support %d reject %d faults %v",
					decision.Status, len(decision.Support), len(decision.Reject), decision.Faults)
			}
			// Byte equality, not just agreement: the key is the canonical bytes
			// now, so the two payloads must produce the identical Value rather
			// than two spellings of it.
			alone, err := resolve(string(item.valid), string(item.outside))
			if err != nil {
				t.Fatal(err)
			}
			other, err := resolve(string(item.outside), string(item.valid))
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(alone.Value, other.Value) {
				t.Fatalf("Value depends on capture order\nforward %s\nbackward %s", alone.Value, other.Value)
			}
			for _, field := range []string{"mark", "vendor"} {
				if bytes.Contains(decision.Value, []byte(`"`+field+`"`)) {
					t.Fatalf("%q entered Value: %s", field, decision.Value)
				}
			}
			split, err := resolve(string(item.valid), string(item.inside))
			if !errors.Is(err, rpc.ErrConflict) || split.Status != rpc.Unknown || len(split.Support) != 1 || len(split.Reject) != 1 || len(split.Faults) != 0 {
				t.Fatalf("a protected field did not split the cluster: %+v err %v", split, err)
			}
		})
	}
}

// TestAdmissionAcceptanceDomain pins the relation that cluster() stopped
// checking. Since 0047 the key is the encoder's output, so the normalizer is
// no longer on the clustering path for these methods, while usableAnswer, the
// anchor validation and the pending path still call it. A normalizer tightened
// past its encoder would stop rejecting a payload in cluster() and keep
// rejecting it in usableAnswer, and the same source would vote in a cluster
// while being marked Failed as an unusable answer, with no fault naming the
// contradiction. So anything the encoder accepts, the normalizer accepts too.
//
// The other direction is not asserted because it is already accounted for: a
// payload the normalizer takes and the encoder refuses becomes an encode fault
// inside the denominator through EncodeCheck.
func TestAdmissionAcceptanceDomain(t *testing.T) {
	for _, item := range admissionCases(t) {
		t.Run(item.method, func(t *testing.T) {
			accepted := 0
			for i, payload := range append([]json.RawMessage{item.valid}, item.more...) {
				if _, err := item.encode(payload); err != nil {
					t.Errorf("payload %d: the encoder refused a payload this row claims it accepts: %v", i, err)
					continue
				}
				accepted++
				if _, err := item.normalize(payload); err != nil {
					t.Errorf("payload %d: encoder accepted, normalizer rejected: %v\npayload %s", i, err, payload)
				}
			}
			// A row whose every payload the encoder refuses would make the
			// assertion vacuous, so the row has to carry answers.
			if accepted == 0 {
				t.Fatalf("the encoder accepted nothing in this row, so nothing was checked")
			}
			t.Logf("%d payloads accepted by the encoder, all accepted by the normalizer", accepted)
			for i, payload := range item.reject {
				if _, err := item.encode(payload); err == nil {
					t.Errorf("reject[%d]: the encoder accepted a payload this method must refuse: %s", i, payload)
				}
			}
		})
	}
}

// TestAdmissionEncoderDeterminism pins that the encoder is a function of the
// payload alone. Go randomizes map iteration order, so an encoder that built
// its result by ranging a map would pass a single-shot check and fail under
// repetition. These bytes are the comparison key, so a key that moved between
// two otherwise identical sources would split a corroborated cluster. Every
// accepted payload in the row is repeated, not only valid: the captured
// receipt has no logs, so only the logged receipt in more exercises the log
// and topic encoding.
func TestAdmissionEncoderDeterminism(t *testing.T) {
	for _, item := range admissionCases(t) {
		t.Run(item.method, func(t *testing.T) {
			for n, payload := range append([]json.RawMessage{item.valid}, item.more...) {
				first, err := item.encode(payload)
				if err != nil {
					t.Fatalf("payload %d: %v", n, err)
				}
				for i := 0; i < 100; i++ {
					again, err := item.encode(payload)
					if err != nil {
						t.Fatalf("payload %d: %v", n, err)
					}
					if !bytes.Equal(again, first) {
						t.Fatalf("payload %d: encoding %d of 100 moved the bytes\nfirst %s\nagain  %s", n, i+2, first, again)
					}
				}
			}
		})
	}
}

// TestAdmissionMethodEdges runs the per-method shape rules. These are not
// parameters of the four uniform properties: an access list keeping its error
// text verbatim, a receipt's null and a block array's null meaning different
// things, a feeHistory ratio reduced to its shortest form, and so on. Each is
// a rule about one method's own shape, so they stay in the row as named hooks
// and the uniform drivers stay uniform.
func TestAdmissionMethodEdges(t *testing.T) {
	for _, item := range admissionCases(t) {
		names := make([]string, 0, len(item.edges))
		for name := range item.edges {
			names = append(names, name)
		}
		sort.Strings(names)
		if len(names) == 0 {
			t.Logf("%s: no method-specific edges", item.method)
			continue
		}
		for _, name := range names {
			t.Run(item.method+"/"+name, func(t *testing.T) {
				item.edges[name](t)
			})
		}
	}
}

// TestAdmissionIndexRow pins the declaration the generated method-rules table
// is built from, so that a row cannot go stale without the table going stale
// with it. The key source is what cluster() reads, and it is the only place
// the choice between the encoder and the normalizer is recorded.
func TestAdmissionIndexRow(t *testing.T) {
	for _, item := range admissionCases(t) {
		t.Run(item.method, func(t *testing.T) {
			rule, _ := item.policy().(rpc.MethodRuleSource).MethodRule()
			if rule.KeySource != item.keySource {
				t.Fatalf("key source = %q, want %q", rule.KeySource, item.keySource)
			}
			// Every method in this table is migrated, so it writes a
			// canonical result. This is the column that separates the
			// narrow-key case, canonical with a normalizer key, from an
			// unmigrated one, raw with a normalizer key.
			if rule.Result != rpc.RuleResultCanonical {
				t.Fatalf("result = %q, want %q", rule.Result, rpc.RuleResultCanonical)
			}
			if rule.Resolver != rpc.RuleResolverStrict {
				t.Fatalf("resolver = %q, want %q", rule.Resolver, rpc.RuleResolverStrict)
			}
		})
	}
}
