package shapeprobe

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/codermaybe/ethguard/internal/chainlist"
	"github.com/codermaybe/ethguard/internal/rpcsample"
)

// EndpointHeight is one public endpoint that answered eth_blockNumber.
// URL is held only in memory; persisted records keep the redacted form.
type EndpointHeight struct {
	URL    string
	Height uint64
}

// FilterPublicHTTPS keeps absolute https endpoints with no template placeholders.
// The registry order is preserved. This list is for one probe run, not a
// library default.
func FilterPublicHTTPS(endpoints []chainlist.Endpoint) []chainlist.Endpoint {
	var out []chainlist.Endpoint
	seen := map[string]bool{}
	for _, endpoint := range endpoints {
		if !publicHTTPS(endpoint.URL) || seen[endpoint.URL] {
			continue
		}
		seen[endpoint.URL] = true
		out = append(out, endpoint)
	}
	return out
}

func publicHTTPS(raw string) bool {
	if strings.ContainsAny(raw, "{} $") {
		return false
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.User != nil {
		return false
	}
	return parsed.Scheme == "https" && parsed.Host != "" && parsed.Host != "localhost"
}

// SelectByBlockNumber returns the first want endpoints, in input order, that
// answer eth_blockNumber. Input order is the registry order.
func SelectByBlockNumber(ctx context.Context, endpoints []chainlist.Endpoint, want int, timeout time.Duration) ([]EndpointHeight, error) {
	if want < 1 {
		return nil, fmt.Errorf("shapeprobe: want at least one endpoint")
	}
	const window = 16
	var selected []EndpointHeight
	client := &http.Client{Timeout: timeout}
	for start := 0; start < len(endpoints) && len(selected) < want; start += window {
		batch := endpoints[start:min(start+window, len(endpoints))]
		type result struct {
			index  int
			height uint64
			ok     bool
		}
		results := make([]result, len(batch))
		var wg sync.WaitGroup
		for i, endpoint := range batch {
			wg.Add(1)
			go func(i int, rawURL string) {
				defer wg.Done()
				height, err := blockNumber(ctx, client, rawURL)
				results[i] = result{index: i, height: height, ok: err == nil}
			}(i, endpoint.URL)
		}
		wg.Wait()
		for _, item := range results {
			if !item.ok {
				continue
			}
			selected = append(selected, EndpointHeight{URL: batch[item.index].URL, Height: item.height})
			if len(selected) == want {
				break
			}
		}
	}
	if len(selected) < want {
		return nil, fmt.Errorf("shapeprobe: %d endpoints answered eth_blockNumber, want %d", len(selected), want)
	}
	return selected, nil
}

// MinHeight is the batch anchor: every selected endpoint had already reached it.
func MinHeight(endpoints []EndpointHeight) (uint64, error) {
	if len(endpoints) == 0 {
		return 0, fmt.Errorf("shapeprobe: no heights")
	}
	minHeight := endpoints[0].Height
	for _, endpoint := range endpoints[1:] {
		if endpoint.Height < minHeight {
			minHeight = endpoint.Height
		}
	}
	return minHeight, nil
}

// Plan builds a collector plan. Source ids are s1, s2, ... in endpoint order.
// The plan is not written to disk.
func Plan(height uint64, endpoints []EndpointHeight) (rpcsample.Plan, error) {
	requests, err := ProbeRequests(height)
	if err != nil {
		return rpcsample.Plan{}, err
	}
	sources := make([]rpcsample.Source, len(endpoints))
	for i, endpoint := range endpoints {
		sources[i] = rpcsample.Source{ID: fmt.Sprintf("s%d", i+1), Endpoint: endpoint.URL}
	}
	return rpcsample.Plan{
		Schema:   rpcsample.PlanSchema,
		Network:  rpcsample.Network{ChainID: 1, Fork: "unspecified"},
		Sources:  sources,
		Requests: requests,
	}, nil
}

func blockNumber(ctx context.Context, client *http.Client, rawURL string) (uint64, error) {
	body := []byte(`{"jsonrpc":"2.0","id":1,"method":"eth_blockNumber","params":[]}`)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, rawURL, bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "ethguard-shapeprobe/0.1")
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return 0, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return 0, fmt.Errorf("status %s", resp.Status)
	}
	var envelope struct {
		Result string `json:"result"`
	}
	if err := json.Unmarshal(payload, &envelope); err != nil {
		return 0, err
	}
	return parseHexQuantity(envelope.Result)
}

func parseHexQuantity(value string) (uint64, error) {
	if len(value) < 3 || !strings.HasPrefix(value, "0x") {
		return 0, fmt.Errorf("not a hex quantity")
	}
	return strconv.ParseUint(value[2:], 16, 64)
}
