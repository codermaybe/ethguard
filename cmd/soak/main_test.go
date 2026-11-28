package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/codermaybe/ethguard"
	"github.com/codermaybe/ethguard/config"
	"github.com/codermaybe/ethguard/rpctest"
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

func TestDeepDiveMakesAnotherFullSampleAndNormalizesClusters(t *testing.T) {
	cfg := config.Config{}
	nodes := []*rpctest.Node{rpctest.NewNode(1), rpctest.NewNode(1)}
	for i, node := range nodes {
		t.Cleanup(node.Close)
		value := "0xa"
		if i == 1 {
			value = "0xA"
		}
		node.Return("eth_chainId", value)
		cfg.Upstreams = append(cfg.Upstreams, config.Upstream{ID: value, Endpoint: node.URL()})
	}
	client, err := ethguard.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.Close)
	if _, err := client.Call(context.Background(), "eth_chainId"); err != nil {
		t.Fatal(err)
	}
	output, err := os.CreateTemp(t.TempDir(), "deep-dive")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := output.Close(); err != nil {
			t.Errorf("close diagnostic output: %v", err)
		}
	}()
	previous := os.Stderr
	os.Stderr = output
	defer func() { os.Stderr = previous }()
	diveInto(client, step{name: "chainId", method: "eth_chainId"}, time.Second)
	content, err := os.ReadFile(output.Name())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), "1 clusters, 0 faulted") {
		t.Fatalf("deep dive skipped Normalize clustering: %s", content)
	}
	for i, node := range nodes {
		if calls := node.Calls(); len(calls) != 2 {
			t.Fatalf("source %d received %d requests, want original plus diagnostic sample", i, len(calls))
		}
		if h := client.Sources()[i].Health; h.Successes != 2 || h.Failures != 0 {
			t.Fatalf("second diagnostic request health: %+v", h)
		}
	}
}
