// Command shapeprobe collects one mainnet response shape for each read method
// that still has no capture batch. It writes redacted records and an
// observation report. It does not write expect.json and it does not decide
// compatibility.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/codermaybe/ethguard/internal/chainlist"
	"github.com/codermaybe/ethguard/internal/rpcsample"
	"github.com/codermaybe/ethguard/internal/rpcsample/replay"
	"github.com/codermaybe/ethguard/internal/shapeprobe"
)

const registryURL = "https://chainlist.org/rpcs.json"

func main() {
	outDir := flag.String("out", "", "probe output directory (required)")
	anchorFlag := flag.Uint64("anchor", 0, "decimal anchor height; 0 uses the minimum preflight eth_blockNumber")
	endpoints := flag.Int("endpoints", 4, "number of answering endpoints to keep, at least 3")
	timeout := flag.Duration("timeout", 90*time.Second, "timeout for each recorded request")
	preflight := flag.Duration("preflight-timeout", 12*time.Second, "timeout for each eth_blockNumber probe")
	maxScan := flag.Int("max-scan", 64, "maximum registry endpoints to probe")
	file := flag.String("file", "", "local rpcs.json (default: fetch chainlist)")
	reportOnly := flag.Bool("report-only", false, "write batch.json and report.md from records already in -out; requires -anchor")
	flag.Parse()
	if *outDir == "" || *endpoints < 3 {
		flag.Usage()
		os.Exit(2)
	}
	if *reportOnly {
		if *anchorFlag == 0 {
			fail("-report-only requires -anchor")
		}
		if err := writeReport(*outDir, *anchorFlag, nil, "minimum preflight eth_blockNumber across the selected endpoints"); err != nil {
			fail("%v", err)
		}
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	candidates, err := loadCandidates(ctx, *file, *maxScan)
	if err != nil {
		fail("%v", err)
	}
	selected, err := shapeprobe.SelectByBlockNumber(ctx, candidates, *endpoints, *preflight)
	if err != nil {
		fail("%v", err)
	}
	anchor := *anchorFlag
	rule := "caller-supplied -anchor; preflight heights are context only"
	if anchor == 0 {
		anchor, err = shapeprobe.MinHeight(selected)
		if err != nil {
			fail("%v", err)
		}
		rule = "minimum preflight eth_blockNumber across the selected endpoints"
	}
	plan, err := shapeprobe.Plan(anchor, selected)
	if err != nil {
		fail("plan: %v", err)
	}
	collector := rpcsample.Collector{Client: &http.Client{Timeout: *timeout}}
	if _, err := collector.Collect(ctx, plan, *outDir); err != nil {
		fail("collect: %v", err)
	}
	if err := rawEndpointAbsent(*outDir, selected); err != nil {
		fail("%v", err)
	}
	heights := map[string]string{}
	for i, endpoint := range selected {
		heights[fmt.Sprintf("s%d", i+1)] = shapeprobe.HeightHex(endpoint.Height)
	}
	if err := writeReport(*outDir, anchor, heights, rule); err != nil {
		fail("%v", err)
	}
}

func writeReport(outDir string, anchor uint64, preflight map[string]string, rule string) error {
	index, records, err := replay.LoadBatch(outDir)
	if err != nil {
		return err
	}
	if err := shapeprobe.CheckPinnedRequests(records, anchor); err != nil {
		return err
	}
	capturedAt := records[0].ObservedAt.UTC()
	for _, record := range records[1:] {
		if record.ObservedAt.Before(capturedAt) {
			capturedAt = record.ObservedAt.UTC()
		}
	}
	meta := shapeprobe.Meta{
		Schema:             shapeprobe.MetaSchema,
		BatchID:            filepath.Base(outDir),
		CapturedAt:         capturedAt,
		Network:            rpcsample.Network{ChainID: 1, Fork: "unspecified"},
		BlockHeight:        shapeprobe.HeightHex(anchor),
		BlockHeightDecimal: anchor,
		AnchorRule:         rule,
		LogsWindow:         shapeprobe.LogsWindowAt(anchor),
		PreflightHeight:    preflight,
		Web3ClientVersion:  clientVersions(records),
		Note:               shapeprobe.ReproductionNote,
	}
	report, err := shapeprobe.RenderReport(meta, index, records)
	if err != nil {
		return err
	}
	if err := writeJSON(filepath.Join(outDir, "batch.json"), meta); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(outDir, "report.md"), []byte(report), 0o600); err != nil {
		return err
	}
	fmt.Printf("probed anchor %s; report %s\n", meta.BlockHeight, filepath.Join(outDir, "report.md"))
	return nil
}

func loadCandidates(ctx context.Context, file string, maxScan int) ([]chainlist.Endpoint, error) {
	var reader io.Reader
	if file != "" {
		opened, err := os.Open(file)
		if err != nil {
			return nil, err
		}
		defer opened.Close()
		reader = opened
	} else {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, registryURL, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("User-Agent", "ethguard-shapeprobe/0.1")
		resp, err := (&http.Client{Timeout: time.Minute}).Do(req)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("chainlist: status %s", resp.Status)
		}
		reader = resp.Body
	}
	chains, err := chainlist.Parse(reader)
	if err != nil {
		return nil, err
	}
	endpoints := shapeprobe.FilterPublicHTTPS(chainlist.ForChain(chains, 1, 0))
	if maxScan > 0 && len(endpoints) > maxScan {
		endpoints = endpoints[:maxScan]
	}
	if len(endpoints) == 0 {
		return nil, fmt.Errorf("shapeprobe: no public https endpoints")
	}
	return endpoints, nil
}

func clientVersions(records []rpcsample.Record) map[string]*string {
	out := map[string]*string{}
	for _, record := range records {
		if record.Method != shapeprobe.ClientVersionMethod {
			continue
		}
		out[record.Source.ID] = versionOf(record.Response.Body)
	}
	return out
}

func versionOf(body string) *string {
	var fields map[string]json.RawMessage
	if json.Unmarshal([]byte(body), &fields) != nil {
		return nil
	}
	raw, ok := fields["result"]
	if !ok {
		return nil
	}
	var version string
	if json.Unmarshal(raw, &version) != nil || version == "" {
		return nil
	}
	return &version
}

func rawEndpointAbsent(dir string, endpoints []shapeprobe.EndpointHeight) error {
	return filepath.WalkDir(dir, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		text := string(body)
		for _, endpoint := range endpoints {
			if !urlHasSecret(endpoint.URL) {
				continue
			}
			if strings.Contains(text, endpoint.URL) {
				return fmt.Errorf("output still contains a raw endpoint URL")
			}
		}
		return nil
	})
}

// urlHasSecret reports whether the URL carries more than the scheme and host
// that the collector is supposed to persist.
func urlHasSecret(raw string) bool {
	parsed, err := url.Parse(raw)
	if err != nil {
		return true
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return true
	}
	return parsed.Path != "" && parsed.Path != "/"
}

func writeJSON(path string, value any) error {
	body, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(body, '\n'), 0o600)
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "shapeprobe: "+format+"\n", args...)
	os.Exit(1)
}
