package aggregator

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/codermaybe/ethguard/internal/ethcodec"
	nativhttp "github.com/codermaybe/ethguard/internal/transport/http"
	"github.com/codermaybe/ethguard/pool"
	"github.com/codermaybe/ethguard/rpc"
	"github.com/codermaybe/ethguard/rpc/methods"
	rpctest "github.com/codermaybe/ethguard/rpctest"
)

// The createAccessList route through a real Aggregator: the same request,
// fixture bytes, and scripted fleet run the production plan and the
// pre-migration plan, so the per-call cost comparison includes collection,
// comparison-key normalization, adjudication, and the value commit — not
// only the encoder function.

// accessListBenchValue builds one createAccessList-shaped result over n
// items: an empty list, a typical three-item list, and a 64-item list.
func accessListBenchValue(n int) json.RawMessage {
	items := make([]string, n)
	for i := range items {
		items[i] = fmt.Sprintf(`{"address":"0x%040x","storageKeys":["0x%064x","0x%064x"]}`, i+1, i+1, i+2)
	}
	return json.RawMessage(fmt.Sprintf(
		`{"accessList":[%s],"gasUsed":"0x5208","error":"execution reverted"}`,
		strings.Join(items, ",")))
}

// baselineAccessListEncode is the pre-migration encoder, copied verbatim
// from the c3188f8 implementation (internal/ethobject/accesslist/encode.go)
// with only the identifiers renamed: the same typed scalar checks
// (ethcodec.ValidateWireFixedHex, ethcodec.CanonicalQuantity) and the same
// serialization (ethcodec.MarshalNoEscape), so the route comparison pins
// the encoder rather than a re-written approximation.
// Encode checks one eth_createAccessList result and returns the canonical JSON
// of the fields the comparison mask retains: accessList, error, and gasUsed.
// Each access-list item keeps only address and storageKeys. A field the mask
// drops does not enter the value.
//
// Admission is section 4.2 step 1 of return-value-design.md: a field is in the
// protected domain because it is in the comparison key. A missing address or
// storageKeys fails. error and gasUsed are omitted when absent and are not
// filled with null. An empty accessList or storageKeys stays [].
//
// Addresses and storage keys are lowercase. gasUsed is canonical QUANTITY.
// error stays the original JSON string. Array order is unchanged. JSON is
// compact, HTML escaping is off, and field order is the encoder struct order.
func baselineAccessListEncode(value json.RawMessage) (json.RawMessage, error) {
	trimmed := bytes.TrimSpace(value)
	if len(trimmed) == 0 || trimmed[0] != '{' || bytes.Equal(trimmed, []byte("null")) {
		return nil, fmt.Errorf("accessList: expected an object")
	}
	var raw struct {
		AccessList json.RawMessage `json:"accessList"`
		Error      json.RawMessage `json:"error"`
		GasUsed    json.RawMessage `json:"gasUsed"`
	}
	if err := json.Unmarshal(trimmed, &raw); err != nil {
		return nil, fmt.Errorf("accessList: malformed response")
	}
	if !ethcodec.PresentNonNull(raw.AccessList) {
		return nil, fmt.Errorf("accessList: missing accessList")
	}
	var rows []json.RawMessage
	if err := json.Unmarshal(raw.AccessList, &rows); err != nil || rows == nil {
		return nil, fmt.Errorf("accessList: expected an array")
	}
	items := make([]baselineAccessListEntry, len(rows))
	for i, row := range rows {
		item, err := baselineAccessListEncodeItem(row)
		if err != nil {
			return nil, fmt.Errorf("accessList[%d]: %w", i, err)
		}
		items[i] = item
	}
	out := baselineAccessListObject{AccessList: items}
	if ethcodec.PresentNonNull(raw.Error) {
		var text string
		if err := json.Unmarshal(raw.Error, &text); err != nil {
			return nil, fmt.Errorf("accessList: error must be a string")
		}
		out.Error = &text
	}
	if ethcodec.PresentNonNull(raw.GasUsed) {
		gas, err := ethcodec.CanonicalQuantity(raw.GasUsed)
		if err != nil {
			return nil, fmt.Errorf("accessList: gasUsed: %w", err)
		}
		out.GasUsed = &gas
	}
	return ethcodec.MarshalNoEscape(out)
}

func baselineAccessListEncodeItem(value json.RawMessage) (baselineAccessListEntry, error) {
	var raw struct {
		Address     json.RawMessage   `json:"address"`
		StorageKeys []json.RawMessage `json:"storageKeys"`
	}
	if err := json.Unmarshal(value, &raw); err != nil {
		return baselineAccessListEntry{}, fmt.Errorf("expected an object")
	}
	if !ethcodec.PresentNonNull(raw.Address) {
		return baselineAccessListEntry{}, fmt.Errorf("missing address")
	}
	if raw.StorageKeys == nil {
		return baselineAccessListEntry{}, fmt.Errorf("missing storageKeys")
	}
	address, err := ethcodec.ValidateWireFixedHex(raw.Address, 40, "address")
	if err != nil {
		return baselineAccessListEntry{}, fmt.Errorf("address: %w", err)
	}
	keys := make([]string, len(raw.StorageKeys))
	for i, key := range raw.StorageKeys {
		keys[i], err = ethcodec.ValidateWireFixedHex(key, 64, "hash")
		if err != nil {
			return baselineAccessListEntry{}, fmt.Errorf("storageKeys[%d]: %w", i, err)
		}
	}
	return baselineAccessListEntry{Address: address, StorageKeys: keys}, nil
}

// canonical is the protected eth_createAccessList object. Field order is
// fixed. error and gasUsed are omitted when the source did not provide them.
type baselineAccessListObject struct {
	AccessList []baselineAccessListEntry `json:"accessList"`
	Error      *string                   `json:"error,omitempty"`
	GasUsed    *string                   `json:"gasUsed,omitempty"`
}

type baselineAccessListEntry struct {
	Address     string   `json:"address"`
	StorageKeys []string `json:"storageKeys"`
}

// BenchmarkAccessListRoute measures one corroborated Aggregate per source
// count and list size, once through the production plan and once through the
// pre-migration value encoder. Server setup and one warm-up invocation stay
// outside the timed loop.
func BenchmarkAccessListRoute(b *testing.B) {
	plan, err := methods.PlanFor(methods.MethodCreateAccessList)
	if err != nil {
		b.Fatal(err)
	}
	// The baseline variant keeps the route's declared shape (same normalizer,
	// same resolver class, same reference and pending columns) and swaps only
	// the value encoder for the pre-migration one.
	baselinePlan := plan
	baselinePlan.Resolver = rpc.Strict(plan.Resolver.Normalize, rpc.WithCanonicalValue(baselineAccessListEncode))
	sizes := []struct {
		name  string
		items int
	}{
		{"empty", 0},
		{"typical", 3},
		{"items-64", 64},
	}
	for _, size := range sizes {
		body := accessListBenchValue(size.items)
		for _, sources := range []int{1, 3, 5} {
			b.Run(fmt.Sprintf("%s/sources-%d", size.name, sources), func(b *testing.B) {
				members := make([]pool.Member, sources)
				for i := range members {
					node := accessListBenchNode(b, body)
					members[i] = pool.Member{Name: fmt.Sprintf("access-%d", i), URL: node}
				}
				p, err := pool.NewWithMembers(0, members, pool.WithDialer(nativhttp.NewDialer()))
				if err != nil {
					b.Fatal(err)
				}
				b.Cleanup(p.Close)
				// The floor follows the fleet size: one member resolves as a
				// single-source Selected read, larger fleets corroborate.
				floor := 2
				wantStatus := rpc.Corroborated
				if sources == 1 {
					floor, wantStatus = 1, rpc.Selected
				}
				shared, err := rpc.NewCorroborationShared(rpc.CorroborationMajority, floor)
				if err != nil {
					b.Fatal(err)
				}
				request := rpc.Request{Method: methods.MethodCreateAccessList, Params: []any{
					map[string]any{"from": "0x" + strings.Repeat("11", 20), "gas": "0x5208"},
					"0x1", // an explicit height pins the read: no moving reference is established
				}}
				for _, variant := range []struct {
					name string
					plan rpc.ReadPlan
				}{
					{"baseline-encode", baselinePlan},
					{"production-encode", plan},
				} {
					b.Run(variant.name, func(b *testing.B) {
						agg := New(p, WithPlans(map[string]rpc.ReadPlan{methods.MethodCreateAccessList: variant.plan}), WithCorroboration(shared))
						warm, warmErr := agg.Aggregate(context.Background(), request)
						if warmErr != nil || warm.Status != wantStatus || len(warm.Support) != sources {
							b.Fatalf("warm-up = %v, %v (sources %d)", warm.Status, warmErr, sources)
						}
						b.ReportAllocs()
						b.ResetTimer()
						for range b.N {
							decision, aggErr := agg.Aggregate(context.Background(), request)
							if aggErr != nil || decision.Status != wantStatus || len(decision.Support) != sources {
								b.Fatalf("aggregate = %v, %v", decision.Status, aggErr)
							}
						}
					})
				}
			})
		}
	}
}

func accessListBenchNode(b *testing.B, body json.RawMessage) string {
	b.Helper()
	node := rpctest.NewNode(0)
	node.ReturnRaw(methods.MethodCreateAccessList, body)
	b.Cleanup(node.Close)
	return node.URL()
}
