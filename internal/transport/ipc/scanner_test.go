package ipc

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/codermaybe/ethguard/internal/jsonsafe"
	"github.com/codermaybe/ethguard/internal/transport"
)

type fragmentedReader struct {
	r    io.Reader
	size int
}

func (r fragmentedReader) Read(p []byte) (int, error) { return r.r.Read(p[:min(len(p), r.size)]) }

func TestScannerSeparatesAdjacentMultilineAndEscapedObjects(t *testing.T) {
	escaped, err := json.MarshalIndent(map[string]any{"result": map[string]any{"array": []any{1, 2, map[string]any{"text": "} { \" \\"}}}}, "", " ")
	if err != nil {
		t.Fatal(err)
	}
	objects := []string{`{"result":"first"}`, string(escaped), `{"result":null}`}
	wire := strings.Join(objects, "")
	for _, size := range []int{1, 2, 7, 4096} {
		s := newScanner(fragmentedReader{strings.NewReader(" \t\r\n" + wire + "\n"), size}, 8192)
		var held [][]byte
		for _, want := range objects {
			got, err := s.readMessage()
			if err != nil || string(got) != want {
				t.Fatalf("fragment=%d: got=%q err=%v want=%q", size, got, err, want)
			}
			held = append(held, got)
		}
		if _, err := s.readMessage(); !errors.Is(err, io.EOF) {
			t.Fatalf("terminal=%v", err)
		}
		for i, got := range held {
			if string(got) != objects[i] {
				t.Fatal("later read overwrote handed-off object")
			}
		}
	}
}

func TestScannerBoundsAndStickyFailures(t *testing.T) {
	for _, tc := range []struct {
		name, input string
		limit       int64
		cause       error
	}{
		{"body limit", `{"result":"` + strings.Repeat("x", 10000), 64, transport.ErrBodyTooLarge},
		{"one byte limit", `{}`, 1, transport.ErrBodyTooLarge},
		{"batch", "[{\"result\":1}]", 1024, ErrBatchUnsupported},
		{"scalar", "true", 1024, ErrInvalidMessage},
		{"string", `"data"`, 1024, ErrInvalidMessage},
		{"garbage", "!", 1024, ErrInvalidMessage},
		{"mismatched", `{"x":[}`, 1024, ErrInvalidMessage},
		{"truncated object", `{"x":1`, 1024, io.ErrUnexpectedEOF},
		{"truncated string", `{"x":"abc`, 1024, io.ErrUnexpectedEOF},
		{"truncated escape", `{"x":"abc\`, 1024, io.ErrUnexpectedEOF},
		{"too deep", `{"x":` + strings.Repeat("[", jsonsafe.MaxNestingDepth) + "0", 1024, jsonsafe.ErrTooDeep},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newScanner(strings.NewReader(tc.input), tc.limit)
			got, first := s.readMessage()
			if got != nil || !errors.Is(first, tc.cause) {
				t.Fatalf("got=%q err=%v want=%v", got, first, tc.cause)
			}
			_, again := s.readMessage()
			if again != first { //nolint:errorlint // A sticky parser must return its original error instance.
				t.Fatal("scanner retried after its terminal failure")
			}
		})
	}
}

type repeatedWhitespace struct{ remaining int }

func (r *repeatedWhitespace) Read(p []byte) (int, error) {
	if r.remaining == 0 {
		return 0, io.EOF
	}
	n := min(len(p), r.remaining)
	for i := range p[:n] {
		p[i] = ' '
	}
	r.remaining -= n
	return n, nil
}

func TestScannerWhitespaceDoesNotConsumeTheMessageBudget(t *testing.T) {
	// This models an arbitrarily long whitespace prefix without retaining it.
	s := newScanner(io.MultiReader(&repeatedWhitespace{remaining: 2 << 20}, strings.NewReader(`{}{`)), 2)
	got, err := s.readMessage()
	if err != nil || string(got) != `{}` {
		t.Fatalf("after whitespace: %q %v", got, err)
	}
	if s.reader.Size() != 4096 {
		t.Fatalf("read buffer grew with whitespace: %d", s.reader.Size())
	}
	_, first := s.readMessage()
	if !errors.Is(first, io.ErrUnexpectedEOF) {
		t.Fatalf("truncated adjacent message: %v", first)
	}
	_, again := s.readMessage()
	if again != first { //nolint:errorlint // A sticky parser must return its original error instance.
		t.Fatal("adjacent message failure was not sticky")
	}
}

func TestScannerExactLimitsAndIndependentMessages(t *testing.T) {
	for _, n := range []int{2, 128, 4096, 4097, 8193} {
		body := append([]byte{'{'}, bytes.Repeat([]byte{' '}, n-2)...)
		body = append(body, '}')
		s := newScanner(bytes.NewReader(append(append([]byte(nil), body...), []byte(`{}`)...)), int64(n))
		got, err := s.readMessage()
		if err != nil || !bytes.Equal(got, body) || int64(cap(got)) > s.limit {
			t.Fatalf("limit=%d len=%d cap=%d err=%v", n, len(got), cap(got), err)
		}
		second, err := s.readMessage()
		if err != nil {
			t.Fatal(err)
		}
		second[0] = '!'
		if !bytes.Equal(got, body) {
			t.Fatal("returned objects alias")
		}
	}
	accepted := `{"x":` + strings.Repeat("[", jsonsafe.MaxNestingDepth-1) + "0" + strings.Repeat("]", jsonsafe.MaxNestingDepth-1) + "}"
	if _, err := newScanner(strings.NewReader(accepted), 4096).readMessage(); err != nil {
		t.Fatalf("allowed nesting: %v", err)
	}
}

func TestScannerLeavesJSONSemanticsToRPCWire(t *testing.T) {
	// Boundary scanning is not a second semantic parser. A complete malformed
	// object must reach rpcwire, where associated calls retain error evidence.
	for _, wire := range []string{`{"id":1,"result":oops}`, `{"id":1,"result":1,"result":2}`} {
		got, err := newScanner(strings.NewReader(wire), 1024).readMessage()
		if err != nil || string(got) != wire {
			t.Fatalf("scanner performed JSON validation: %q %v", got, err)
		}
	}
}
