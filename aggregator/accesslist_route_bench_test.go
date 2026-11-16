package aggregator

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	nativhttp "github.com/codermaybe/ethguard/internal/transport/http"
	"github.com/codermaybe/ethguard/rpc/methods"
	"github.com/codermaybe/ethguard/pool"
	"github.com/codermaybe/ethguard/rpc"
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

// baselineAccessListEncode is the pre-migration encoder, verbatim in
// behavior: it pins the comparison point of the current generated-model
// route without borrowing the production implementation.
type baselineAccessListResult struct {
	AccessList []baselineAccessListItem `json:"accessList"`
	Error      *string                  `json:"error,omitempty"`
	GasUsed    *string                  `json:"gasUsed,omitempty"`
}

type baselineAccessListItem struct {
	Address     string   `json:"address"`
	StorageKeys []string `json:"storageKeys"`
}

func baselineAccessListEncode(value json.RawMessage) (json.RawMessage, error) {
	trimmed := strings.TrimSpace(string(value))
	if len(trimmed) == 0 || trimmed[0] != '{' || trimmed == "null" {
		return nil, fmt.Errorf("accessList: expected an object")
	}
	var raw struct {
		AccessList json.RawMessage `json:"accessList"`
		Error      json.RawMessage `json:"error"`
		GasUsed    json.RawMessage `json:"gasUsed"`
	}
	if err := json.Unmarshal([]byte(trimmed), &raw); err != nil {
		return nil, fmt.Errorf("accessList: malformed response")
	}
	if len(raw.AccessList) == 0 || string(raw.AccessList) == "null" {
		return nil, fmt.Errorf("accessList: missing accessList")
	}
	var rows []json.RawMessage
	if err := json.Unmarshal(raw.AccessList, &rows); err != nil || rows == nil {
		return nil, fmt.Errorf("accessList: expected an array")
	}
	items := make([]baselineAccessListItem, len(rows))
	for i, row := range rows {
		item, err := baselineAccessListItem2(row)
		if err != nil {
			return nil, fmt.Errorf("accessList[%d]: %w", i, err)
		}
		items[i] = item
	}
	out := baselineAccessListResult{AccessList: items}
	if len(raw.Error) != 0 && string(raw.Error) != "null" {
		var text string
		if err := json.Unmarshal(raw.Error, &text); err != nil {
			return nil, fmt.Errorf("accessList: error must be a string")
		}
		out.Error = &text
	}
	if len(raw.GasUsed) != 0 && string(raw.GasUsed) != "null" {
		gas, err := benchQuantity(raw.GasUsed)
		if err != nil {
			return nil, fmt.Errorf("accessList: gasUsed: %w", err)
		}
		out.GasUsed = &gas
	}
	marshaled, err := json.Marshal(out)
	return marshaled, err
}

func baselineAccessListItem2(value json.RawMessage) (baselineAccessListItem, error) {
	var raw struct {
		Address     json.RawMessage   `json:"address"`
		StorageKeys []json.RawMessage `json:"storageKeys"`
	}
	if err := json.Unmarshal(value, &raw); err != nil {
		return baselineAccessListItem{}, fmt.Errorf("expected an object")
	}
	if len(raw.Address) == 0 || string(raw.Address) == "null" {
		return baselineAccessListItem{}, fmt.Errorf("missing address")
	}
	if raw.StorageKeys == nil {
		return baselineAccessListItem{}, fmt.Errorf("missing storageKeys")
	}
	address, err := benchFixedHex(raw.Address, 40)
	if err != nil {
		return baselineAccessListItem{}, fmt.Errorf("address: %w", err)
	}
	keys := make([]string, len(raw.StorageKeys))
	for i, key := range raw.StorageKeys {
		keys[i], err = benchFixedHex(key, 64)
		if err != nil {
			return baselineAccessListItem{}, fmt.Errorf("storageKeys[%d]: %w", i, err)
		}
	}
	return baselineAccessListItem{Address: address, StorageKeys: keys}, nil
}

func benchFixedHex(value json.RawMessage, digits int) (string, error) {
	var text string
	if err := json.Unmarshal(value, &text); err != nil {
		return "", fmt.Errorf("%s: expected string", "member")
	}
	if len(text) != digits+2 || len(text) < 2 || (text[0] != '0') || (text[1] != 'x' && text[1] != 'X') {
		return "", fmt.Errorf("%s: expected 0x-prefixed value with %d hex digits", "member", digits)
	}
	return "0x" + strings.ToLower(text[2:]), nil
}

func benchQuantity(value json.RawMessage) (string, error) {
	var text string
	if err := json.Unmarshal(value, &text); err != nil {
		return "", fmt.Errorf("hex quantity: expected string")
	}
	if len(text) < 3 || text[:2] != "0x" {
		return "", fmt.Errorf("hex quantity: invalid quantity %q", text)
	}
	digits := text[2:]
	if digits != "0" && strings.HasPrefix(digits, "0") {
		return "", fmt.Errorf("hex quantity: non-canonical leading zero in %q", text)
	}
	return "0x" + strings.ToLower(digits), nil
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
