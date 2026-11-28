package ipc

import (
	"bytes"
	"testing"
)

func FuzzIPCMessageScanner(f *testing.F) {
	for _, seed := range []string{`{}`, `{}{} `, "{\n\"result\":\"}\\\"{\"\n}", `[{}]`, `{"x":[[[[`, "\x00", " {\"result\":null}\n"} {
		f.Add([]byte(seed), uint16(64))
	}
	f.Fuzz(func(t *testing.T, data []byte, limitSeed uint16) {
		if len(data) > 65536 {
			t.Skip()
		}
		limit := int64(limitSeed) + 1
		s := newScanner(bytes.NewReader(data), limit)
		var previous [][]byte
		var snapshots [][]byte
		for attempts := 0; attempts <= len(data); attempts++ {
			got, err := s.readMessage()
			if err != nil {
				if got != nil {
					t.Fatal("partial value escaped on failure")
				}
				if _, again := s.readMessage(); again != err { //nolint:errorlint // Verify sticky error identity, not only its category.
					t.Fatal("failure was not sticky")
				}
				return
			}
			if len(got) < 2 || got[0] != '{' || got[len(got)-1] != '}' || int64(len(got)) > limit || int64(cap(got)) > limit {
				t.Fatalf("invalid admitted boundary/size: %q", got)
			}
			for i := range previous {
				if !bytes.Equal(previous[i], snapshots[i]) {
					t.Fatal("prior object reused")
				}
			}
			previous = append(previous, got)
			snapshots = append(snapshots, append([]byte(nil), got...))
		}
		t.Fatal("scanner made no progress")
	})
}
