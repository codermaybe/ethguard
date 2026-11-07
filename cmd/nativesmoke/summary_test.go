package main

import (
	"io"
	"os"
	"strings"
	"testing"
)

// TestPrintSummaryToleratesMissingNativeAdapter pins the summary printer's
// behavior for a geth-only adapter selection: the native columns degrade to
// "?" instead of dereferencing the missing map entry, so a legal
// `-adapters geth` run reaches its report instead of panicking after all
// probes completed.
func TestPrintSummaryToleratesMissingNativeAdapter(t *testing.T) {
	report := smokeReport{
		Providers: []providerReport{
			{
				URL:     "https://geth-only.example",
				Verdict: "pass",
				Adapters: map[string]*adapterReport{
					"geth": {ChainID: "1", EnvelopeOK: true},
				},
			},
		},
	}
	read, write, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	original := os.Stdout
	os.Stdout = write
	done := make(chan string, 1)
	go func() {
		// Read to EOF so the capture covers every write printSummary makes,
		// no matter how the pipe chunks them under load.
		data, _ := io.ReadAll(read)
		done <- string(data)
	}()
	printSummary(report)
	os.Stdout = original
	_ = write.Close()
	out := <-done
	if !strings.Contains(out, "https://geth-only.example") {
		t.Fatalf("summary lost the provider row: %q", out)
	}
	if !strings.Contains(out, "?") {
		t.Fatalf("missing native adapter must print ? placeholders: %q", out)
	}
}
