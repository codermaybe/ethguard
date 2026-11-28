// Package chainlist parses chainlist.org's public RPC registry (rpcs.json)
// into endpoint lists for EthGuard's internal soak command.
//
// This package is a test-tooling aid, deliberately not a runtime dependency:
// zero-config public endpoint pools are a recorded product rejection (see
// docs/design/roadmap.md), and nothing here runs inside the library.
package chainlist

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
)

// Endpoint is one RPC entry of the registry.
type Endpoint struct {
	URL string
	// Tracking is the registry's own privacy label (none / limited / yes /
	// unspecified / empty). It is surfaced in soak reports because it says
	// something about the operator, not because EthGuard treats tracked
	// sources differently — every source is untrusted here by definition.
	Tracking string
}

// Chain is one network entry of the registry.
type Chain struct {
	Name    string
	ChainID uint64
	RPCs    []Endpoint
}

// Parse reads a registry document. RPC entries arrive in two shapes across
// registry versions — objects with url/tracking and bare URL strings — and
// both must survive parsing so schema drift cannot silently drop endpoints.
func Parse(r io.Reader) ([]Chain, error) {
	var doc []struct {
		Name    string            `json:"name"`
		ChainID uint64            `json:"chainId"`
		RPC     []json.RawMessage `json:"rpc"`
	}
	if err := json.NewDecoder(r).Decode(&doc); err != nil {
		return nil, fmt.Errorf("chainlist: parse registry: %w", err)
	}
	chains := make([]Chain, 0, len(doc))
	for _, entry := range doc {
		chain := Chain{Name: entry.Name, ChainID: entry.ChainID}
		for _, raw := range entry.RPC {
			endpoint, err := parseEndpoint(raw)
			if err != nil {
				return nil, fmt.Errorf("chainlist: chain %d: %w", entry.ChainID, err)
			}
			chain.RPCs = append(chain.RPCs, endpoint)
		}
		chains = append(chains, chain)
	}
	return chains, nil
}

func parseEndpoint(raw json.RawMessage) (Endpoint, error) {
	var obj struct {
		URL      string `json:"url"`
		Tracking string `json:"tracking"`
	}
	if err := json.Unmarshal(raw, &obj); err == nil && obj.URL != "" {
		return Endpoint{URL: obj.URL, Tracking: obj.Tracking}, nil
	}
	var url string
	if err := json.Unmarshal(raw, &url); err == nil && url != "" {
		return Endpoint{URL: url}, nil
	}
	return Endpoint{}, fmt.Errorf("unrecognized rpc entry %s", truncate(string(raw), 80))
}

// ForChain collects one chain's endpoints, deterministically ordered by URL
// and capped at max (0 = all). The registry carries no quality signal, so
// the deterministic order is the honest one: stable selection across runs,
// with no pretense of preference.
func ForChain(chains []Chain, chainID uint64, max int) []Endpoint {
	var out []Endpoint
	for _, chain := range chains {
		if chain.ChainID != chainID {
			continue
		}
		out = append(out, chain.RPCs...)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].URL < out[j].URL })
	if max > 0 && len(out) > max {
		out = out[:max]
	}
	return out
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
