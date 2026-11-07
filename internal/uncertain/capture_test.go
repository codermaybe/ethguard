package uncertain

import (
	"bytes"
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/codermaybe/ethguard/rpc"
)

func testConfig(budget int) Config { return Config{BudgetBytes: budget} }

func TestCollectorKeepsSourceFieldArraysAndMalformedResponses(t *testing.T) {
	c := NewCollector(testConfig(DefaultBudget))
	c.Record("response", "m", rpc.SourceRef{ID: "a"}, []byte(`{"vendor":1}`), "")
	c.Record("response", "m", rpc.SourceRef{ID: "b"}, []byte(`{"vendor":1}`), "")
	c.Record("response", "m", rpc.SourceRef{ID: "c"}, []byte(`{"vendor":`), "")
	s := c.Snapshot()
	if len(s.Fields) != 2 || len(s.Raw) != 3 {
		t.Fatalf("fields=%d raw=%d", len(s.Fields), len(s.Raw))
	}
	if !bytes.Equal(s.Raw[2].Value, []byte(`{"vendor":`)) {
		t.Fatalf("malformed response was not retained: %q", s.Raw[2].Value)
	}
}

func TestCollectorSnapshotIsDefensive(t *testing.T) {
	c := NewCollector(testConfig(DefaultBudget))
	c.Record("response", "m", rpc.SourceRef{ID: "a"}, []byte(`{"x":"before"}`), "")
	s := c.Snapshot()
	s.Fields[0].Value[0] = 'X'
	s.Raw[0].Value[0] = 'X'
	c.Record("response", "m", rpc.SourceRef{ID: "late"}, []byte(`{"x":"late"}`), "")
	if got := c.Snapshot(); len(got.Fields) != 2 || got.Fields[0].Value[0] != '"' {
		t.Fatalf("snapshot changed the retained material: %+v", got)
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = c.Snapshot()
		}()
	}
	wg.Wait()
}

func TestCollectorItemCapAndBudgetBound(t *testing.T) {
	// One raw item keeps at most maxItemBytes even under a huge budget.
	c := NewCollector(testConfig(4 << 20))
	c.Record("response", "m", rpc.SourceRef{ID: "a"}, bytes.Repeat([]byte{'x'}, 2<<20), "")
	s := c.Snapshot()
	// 64 KiB of the raw payload plus the record's own metadata text
	// (stage 8 + method 1 + ID 1), billed before the payload copy.
	if s.Bytes != maxItemBytes+10 || !s.Truncated || s.Dropped != 1 {
		t.Fatalf("item cap: bytes=%d truncated=%v dropped=%d", s.Bytes, s.Truncated, s.Dropped)
	}
	if len(s.Raw) != 1 || len(s.Raw[0].Value) != maxItemBytes || !s.Raw[0].Truncated {
		t.Fatalf("per-record truncation flag missing: %+v", s.Raw[0])
	}
	// An exactly-spent budget stops retention entirely: counters move, no
	// records or empty-value records grow.
	spent := NewCollector(testConfig(maxItemBytes + 10)) // raw cap + the record's own metadata
	spent.Record("response", "m", rpc.SourceRef{ID: "a"}, bytes.Repeat([]byte{'x'}, 2<<20), "")
	spent.Record("response", "m", rpc.SourceRef{ID: "b"}, []byte(`{"y":1}`), "")
	after := spent.Snapshot()
	if len(after.Raw) != 1 || len(after.Fields) != 0 {
		t.Fatalf("exhausted budget added records: raw=%d fields=%d", len(after.Raw), len(after.Fields))
	}
	if after.Dropped != 2 || !after.Truncated {
		t.Fatalf("exhausted budget counters: dropped=%d truncated=%v", after.Dropped, after.Truncated)
	}
	if after.Bytes != maxItemBytes+10 {
		t.Fatalf("exhausted budget kept billing bytes: %d", after.Bytes)
	}
}

func TestCollectorRecordCapCountsRawAndFieldsTogether(t *testing.T) {
	c := NewCollector(testConfig(DefaultBudget))
	payload := []byte(`{"k1":1,"k2":2,"k3":3,"k4":4,"k5":5,"k6":6}`)
	// Six fields per value-bearing response: 40 responses hit 240 records,
	// the 41st hits the 256 cap mid-expansion, and beyond that only
	// counters move.
	for i := 0; i < 45; i++ {
		c.Record("response", "m", rpc.SourceRef{ID: "s"}, payload, "")
	}
	s := c.Snapshot()
	if len(s.Raw)+len(s.Fields) > maxRecords {
		t.Fatalf("record cap exceeded: raw=%d fields=%d", len(s.Raw), len(s.Fields))
	}
	if len(s.Raw)+len(s.Fields) != maxRecords {
		t.Fatalf("record cap not exactly filled: raw=%d fields=%d", len(s.Raw), len(s.Fields))
	}
	if s.Dropped == 0 || !s.Truncated {
		t.Fatalf("over-cap entries must count as dropped: dropped=%d truncated=%v", s.Dropped, s.Truncated)
	}
}

func TestCollectorMetadataTextCap(t *testing.T) {
	c := NewCollector(testConfig(1 << 20))
	long := strings.Repeat("e", 8<<10)
	c.Record("response", "m", rpc.SourceRef{ID: "a"}, nil, long)
	s := c.Snapshot()
	if len(s.Raw) != 1 || len(s.Raw[0].Error) != maxMetadataBytes {
		t.Fatalf("error text not capped: %d", len(s.Raw[0].Error))
	}
	// The cut text is a partially retained entry: Truncated reports it and
	// Dropped counts the logical entry once. Bytes bills the retained
	// metadata text plus the record's own stage/method/source text.
	if !s.Truncated || s.Dropped != 1 {
		t.Fatalf("cut metadata text must surface as truncated: dropped=%d truncated=%v", s.Dropped, s.Truncated)
	}
	if s.Bytes != maxMetadataBytes+10 {
		t.Fatalf("metadata bytes not billed: %d", s.Bytes)
	}
	// Source identity components are variable metadata too.
	c2 := NewCollector(testConfig(1 << 20))
	c2.Record("response", "m", rpc.SourceRef{Name: long}, []byte(`{}`), "")
	s2 := c2.Snapshot()
	if len(s2.Raw[0].Source.Name) != maxMetadataBytes {
		t.Fatalf("source name not capped: %d", len(s2.Raw[0].Source.Name))
	}
	if !s2.Truncated || s2.Dropped != 1 {
		t.Fatalf("cut source identity must surface as truncated: %+v", s2)
	}
}

func TestCollectorFieldExpansionStopsAtBudget(t *testing.T) {
	// A budget that fits the raw response but not its field expansion:
	// retention stops mid-object, the counters account for what was left.
	c := NewCollector(testConfig(64 + 8))
	c.Record("response", "m", rpc.SourceRef{ID: "a"}, []byte(`{"a1":"xxxxxxxx","a2":"yyyyyyyy","a3":"zzzzzzzz"}`), "")
	s := c.Snapshot()
	if s.Bytes > s.Limit {
		t.Fatalf("budget exceeded: bytes=%d limit=%d", s.Bytes, s.Limit)
	}
	if !s.Truncated || s.Dropped == 0 {
		t.Fatalf("field expansion over budget must drop and mark: dropped=%d truncated=%v", s.Dropped, s.Truncated)
	}
	// The walk stopped before the last member; no empty field records.
	for _, field := range s.Fields {
		if len(field.Value) == 0 {
			t.Fatalf("empty field record retained: %+v", field)
		}
	}
}

func TestConfigRidesContextAndLastWins(t *testing.T) {
	ctx := context.Background()
	if _, ok := ConfigFromContext(ctx); ok {
		t.Fatal("no configuration by default")
	}
	ctx = WithConfig(ctx, testConfig(10))
	if cfg, ok := ConfigFromContext(ctx); !ok || cfg.BudgetBytes != 10 {
		t.Fatalf("configuration lost: %+v %v", cfg, ok)
	}
	// The last configuration on a derivation chain wins.
	ctx = WithConfig(ctx, testConfig(20))
	if cfg, _ := ConfigFromContext(ctx); cfg.BudgetBytes != 20 {
		t.Fatalf("last configuration did not win: %+v", cfg)
	}
	// A collector rides the context as the per-call consumer.
	collector := NewCollector(testConfig(5))
	ctx = WithCollector(ctx, collector)
	if got := CollectorFromContext(ctx); got != collector {
		t.Fatal("collector lost on the context")
	}
	if CollectorFromContext(context.Background()) != nil {
		t.Fatal("no collector by default")
	}
	// Non-positive budgets are not a configuration.
	if _, ok := ConfigFromContext(WithConfig(context.Background(), Config{BudgetBytes: 0})); ok {
		t.Fatal("a zero budget must not count as configured")
	}
}

// The review regressions: a budget too small for even the record's own
// metadata drops the whole entry without billing; an exhausted budget stops
// the field walk immediately instead of scanning the discarded tail.
func TestCollectorTinyBudgetDropsWholeEntry(t *testing.T) {
	c := NewCollector(testConfig(1))
	c.Record("response", "m", rpc.SourceRef{ID: "a"}, []byte("x"), "")
	s := c.Snapshot()
	if s.Bytes != 0 || s.Limit != 1 {
		t.Fatalf("budget exceeded: Bytes=%d Limit=%d", s.Bytes, s.Limit)
	}
	if !s.Truncated || s.Dropped != 1 || len(s.Raw) != 0 {
		t.Fatalf("whole-entry drop = dropped=%d truncated=%v raw=%d", s.Dropped, s.Truncated, len(s.Raw))
	}
	// A budget that fits the metadata but not the payload keeps the
	// metadata-only record (the error text is evidence) and bills exactly
	// what it retained: stage 8 + method 1 + ID 1 + error 4.
	exact := NewCollector(testConfig(14))
	exact.Record("response", "m", rpc.SourceRef{ID: "a"}, []byte("payload"), "boom")
	s2 := exact.Snapshot()
	if s2.Bytes != 14 || s2.Limit != 14 {
		t.Fatalf("metadata-only record billing: Bytes=%d Limit=%d", s2.Bytes, s2.Limit)
	}
	if len(s2.Raw) != 1 || string(s2.Raw[0].Value) != "" || s2.Raw[0].Error != "boom" {
		t.Fatalf("metadata-only record = %+v", s2.Raw)
	}
	if !s2.Truncated || s2.Dropped != 1 {
		t.Fatalf("unretained payload must surface: dropped=%d truncated=%v", s2.Dropped, s2.Truncated)
	}
}

func TestCollectorFieldWalkStopsAtExhaustion(t *testing.T) {
	// A budget that exhausts mid-object must stop the walk: exactly one
	// dropped count for the abandoned tail, and no further member visits.
	stop := NewCollector(testConfig(1 << 20))
	// Pre-spend the whole budget so the first member sees an exhausted
	// budget; the scan must not walk any member at all.
	stop.bytes = stop.cfg.BudgetBytes
	var payload []byte
	payload = append(payload, '{')
	for i := 0; i < 1000; i++ {
		payload = append(payload, []byte(`"k0000":"v","k0001":"v",`)...)
	}
	payload = append(payload, []byte(`"k0002":"v"}`)...)
	stop.Record("response", "m", rpc.SourceRef{ID: "a"}, payload, "")
	s := stop.Snapshot()
	if len(s.Fields) != 0 {
		t.Fatalf("exhausted walk retained fields: %d", len(s.Fields))
	}
	if s.Dropped != 1 || !s.Truncated {
		t.Fatalf("abandoned tail counts once: dropped=%d truncated=%v", s.Dropped, s.Truncated)
	}
}

func TestCollectorFieldScanIsBoundedAndFaithful(t *testing.T) {
	// The byte-range scan must see exactly the top-level members, survive
	// nesting, escapes, and malformed tails, and never allocate for content
	// it does not retain.
	c := NewCollector(testConfig(1 << 20))
	nested := `{"a":"x\"y","b":{"n":1,"deep":[true,null,-1.5e3]},"c":[{},{"z":"}"}]}`
	c.Record("response", "m", rpc.SourceRef{ID: "a"}, []byte(nested), "")
	s := c.Snapshot()
	paths := map[string]bool{}
	for _, f := range s.Fields {
		paths[f.Path] = true
	}
	for _, want := range []string{"a", "b", "c"} {
		if !paths[want] {
			t.Fatalf("member %q missing from the scan: %v", want, paths)
		}
	}
	// A malformed tail stops the scan without fabricating members, and a
	// malformed member value ends the walk at that point.
	bad := NewCollector(testConfig(1 << 20))
	bad.Record("response", "m", rpc.SourceRef{ID: "a"}, []byte(`{"ok":1,"broken`), "")
	if got := len(bad.Snapshot().Fields); got != 1 {
		t.Fatalf("malformed tail members = %d, want the single parseable one", got)
	}
	badValue := NewCollector(testConfig(1 << 20))
	badValue.Record("response", "m", rpc.SourceRef{ID: "a"}, []byte(`{"ok":1,"broken":}`), "")
	if got := len(badValue.Snapshot().Fields); got != 1 {
		t.Fatalf("malformed value members = %d, want the members before it", got)
	}
	// Non-object values expand nothing.
	nonObj := NewCollector(testConfig(1 << 20))
	nonObj.Record("response", "m", rpc.SourceRef{ID: "a"}, []byte(`[1,2,3]`), "")
	if got := len(nonObj.Snapshot().Fields); got != 0 {
		t.Fatalf("array expansion produced %d fields", got)
	}
}
