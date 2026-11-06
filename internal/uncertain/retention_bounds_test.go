package uncertain

import (
	"strings"
	"testing"
	"unsafe"

	"github.com/codermaybe/ethguard/rpc"
)

func TestCollectorMetadataOwnsBoundedStorage(t *testing.T) {
	backing := strings.Repeat("x", 8<<20)
	for _, input := range []string{backing, backing[100:117]} {
		c := NewCollector(testConfig(DefaultBudget))
		c.Record(input, input, rpc.SourceRef{Name: input, URL: input, ID: input}, nil, input)
		s := c.Snapshot()
		if len(s.Raw) != 1 {
			t.Fatalf("raw records = %d", len(s.Raw))
		}
		r := s.Raw[0]
		for name, retained := range map[string]string{
			"stage": r.Stage, "method": r.Method, "name": r.Source.Name,
			"URL": r.Source.URL, "ID": r.Source.ID, "error": r.Error,
		} {
			if retained != input[:min(len(input), maxMetadataBytes)] {
				t.Fatalf("%s: changed retained text", name)
			}
			// Both a capped text and an already-short substring must release
			// the caller's large backing allocation. No unsafe writes occur.
			if unsafe.StringData(retained) == unsafe.StringData(input) {
				t.Fatalf("%s: %d retained bytes still alias an 8 MiB buffer", name, len(retained))
			}
		}
		if s.Bytes != 6*min(len(input), maxMetadataBytes) {
			t.Fatalf("incorrect metadata billing: %d", s.Bytes)
		}
		if len(input) > maxMetadataBytes && !r.Truncated {
			t.Fatal("raw record did not report its truncated metadata")
		}
	}
}

func TestCollectorFieldWalkerChecksCapacityBeforeScanning(t *testing.T) {
	// The capacity callback runs before even a malformed key is inspected,
	// including after the first successfully retained member.
	for _, input := range []string{`{"unterminated`, `{"ok":1,"unterminated`} {
		checks, visits := 0, 0
		complete := scanObjectMembers([]byte(input), func() int {
			checks++
			if strings.HasPrefix(input, `{"ok"`) && checks == 1 {
				return maxMetadataBytes
			}
			return -1
		}, func(string, []byte) bool { visits++; return true })
		if complete || checks != visits+1 {
			t.Fatalf("complete=%v capacity checks=%d visits=%d", complete, checks, visits)
		}
	}
}

func TestCollectorFieldExpansionAllocationsAreBounded(t *testing.T) {
	// Payload construction is outside measurement. The original implementation
	// allocated about 8 MiB here, even after a one-byte budget was exhausted.
	payload := []byte(`{"` + strings.Repeat(`\u0061`, 1<<20) + `":1}`)
	for _, tc := range []struct {
		name   string
		budget int
		max    int64
	}{
		{"exhausted", 1, 4 << 10},
		{"small remainder", maxItemBytes + 8, 128 << 10},
		{"ample remainder", DefaultBudget, 128 << 10},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bench := testing.Benchmark(func(b *testing.B) {
				b.ReportAllocs()
				for i := 0; i < b.N; i++ {
					c := NewCollector(testConfig(tc.budget))
					c.Record("", "", rpc.SourceRef{}, payload, "")
				}
			})
			if got := bench.AllocedBytesPerOp(); got > tc.max {
				t.Fatalf("%d bytes/op exceeds bounded retention allowance %d", got, tc.max)
			}
			t.Logf("%d bytes/op", bench.AllocedBytesPerOp())
		})
	}
}

func TestCollectorExpandsOnlyCompleteRetainedMembers(t *testing.T) {
	c := NewCollector(testConfig(DefaultBudget))
	payload := []byte(`{"ok":1,"cut":"` + strings.Repeat("x", 2*maxItemBytes) + `","later":2}`)
	c.Record("", "", rpc.SourceRef{}, payload, "")
	s := c.Snapshot()
	if len(s.Fields) != 1 || s.Fields[0].Path != "ok" || string(s.Fields[0].Value) != "1" {
		t.Fatalf("fields from truncated raw: %+v", s.Fields)
	}
	if len(s.Raw[0].Value) != maxItemBytes || !s.Truncated {
		t.Fatal("raw prefix or truncation evidence missing")
	}
	numeric := NewCollector(testConfig(DefaultBudget))
	numeric.Record("", "", rpc.SourceRef{}, []byte(`{"ok":1,"cut":`+strings.Repeat("1", 2*maxItemBytes)+`}`), "")
	if fields := numeric.Snapshot().Fields; len(fields) != 1 || fields[0].Path != "ok" {
		t.Fatalf("truncated number was published as a complete field: %+v", fields)
	}
	// Preserve escaped keys and UTF-8 at the text cap, without retaining the
	// temporary decoded key's larger backing storage.
	key := strings.Repeat(`\u0061`, maxMetadataBytes-1) + `\u20ac`
	unicode := NewCollector(testConfig(DefaultBudget))
	unicode.Record("", "", rpc.SourceRef{}, []byte(`{"`+key+`":1}`), "")
	u := unicode.Snapshot()
	if len(u.Fields) != 1 || u.Fields[0].Path != strings.Repeat("a", maxMetadataBytes-1) || !u.Truncated {
		t.Fatalf("UTF-8 path truncation: fields=%d truncated=%v", len(u.Fields), u.Truncated)
	}
	bad := NewCollector(testConfig(DefaultBudget))
	bad.Record("", "", rpc.SourceRef{}, []byte(`{"ok":1,"bad":[},"after":2}`), "")
	if fields := bad.Snapshot().Fields; len(fields) != 1 || fields[0].Path != "ok" {
		t.Fatalf("malformed nested member produced fields: %+v", fields)
	}
}
