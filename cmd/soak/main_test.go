package main

import (
	"bytes"
	"errors"
	"io"
	"testing"

	"github.com/codermaybe/ethguard"
	"github.com/codermaybe/ethguard/config"
)

func TestReportsReturnOutputFailures(t *testing.T) {
	client, err := ethguard.New(config.Config{Upstreams: []config.Upstream{{ID: "report-source", Endpoint: "http://127.0.0.1:1"}}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.Close)
	steps := []step{{name: "chainId"}}
	results := []stepReport{{outcome: "corroborated", support: 2}}
	for _, tc := range []struct {
		name  string
		write func(io.Writer) error
	}{
		{name: "JSON", write: func(w io.Writer) error { return encodeJSON(w, client, steps, results) }},
		{name: "text", write: func(w io.Writer) error { return printReport(w, 1, 1, "finalized", steps, results) }},
		{name: "source health", write: func(w io.Writer) error { return printSources(w, client) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var complete bytes.Buffer
			if err := tc.write(&complete); err != nil {
				t.Fatal(err)
			}
			for _, limit := range []int{0, complete.Len() / 2} {
				failure := errors.New("report destination failed")
				writer := &reportFailureWriter{remaining: limit, err: failure}
				if err := tc.write(writer); !errors.Is(err, failure) {
					t.Fatalf("write after %d bytes = %v, want output failure", limit, err)
				}
				if !bytes.Equal(writer.written.Bytes(), complete.Bytes()[:limit]) {
					t.Fatalf("partial output = %q, want first %d report bytes", writer.written.Bytes(), limit)
				}
			}
		})
	}
}

type reportFailureWriter struct {
	remaining int
	err       error
	written   bytes.Buffer
}

func (w *reportFailureWriter) Write(p []byte) (int, error) {
	if len(p) <= w.remaining {
		w.remaining -= len(p)
		return w.written.Write(p)
	}
	n, _ := w.written.Write(p[:w.remaining])
	w.remaining = 0
	return n, w.err
}
