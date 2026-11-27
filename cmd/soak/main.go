// Command soak runs EthGuard's corroboration pipeline against a live fleet
// of public endpoints from chainlist.org, producing a per-method census of
// outcomes (corroborated / selected / conflict / notfound / faults) plus
// per-source health. Conflicts on strictly comparable methods trigger a
// deep-dive that clusters each source's canonical answer — the direct
// probe for real-world normalizer divergence.
//
// This is a validation harness, not a runtime feature: zero-config public
// endpoint pools are a recorded product rejection (roadmap "Deliberately
// out of scope"), so nothing here belongs to the library. The registry has
// no quality signal; selection is deterministic by sorted URL and every
// endpoint is treated as untrusted. Public endpoints rate-limit, so the
// soak paces itself — one pass, per-step context timeouts, no retries.
//
// Usage:
//
//	go run ./cmd/soak [-file rpcs.json] [-max-sources 12] [-timeout 20s]
//
// All references are derived from the fleet itself: the anchor comes from
// the corroborated pipeline, the sample transaction from the pinned block.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"math/big"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	ethereum "github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"

	"github.com/codermaybe/ethguard"
	"github.com/codermaybe/ethguard/config"
	"github.com/codermaybe/ethguard/ethapi"
	"github.com/codermaybe/ethguard/internal/diag/chainlist"
	"github.com/codermaybe/ethguard/internal/limits"
	"github.com/codermaybe/ethguard/internal/methods"
	"github.com/codermaybe/ethguard/internal/typed"
	"github.com/codermaybe/ethguard/rpc"
)

// Well-known, stable mainnet fixtures. These are observation targets, not
// expectations: the soak reports what the fleet says, and disagreement is
// the finding.
const (
	// A heavily-funded EOA whose balance and nonce change over time.
	soakAddress = "0xd8dA6BF26964aF9D7eEd9e03E53415D37aA96045"
	// Canonical WETH-9 deployment: contract code and its slot-zero storage.
	soakContract = "0xC02aaA39b223FE8D0A0e5C4F27eAD9083C756Cc2"
	registryURL  = "https://chainlist.org/rpcs.json"
	userAgent    = "ethguard-soak/0.1"
)

type step struct {
	name string
	// method and params feed the conflict deep-dive, which re-collects the
	// step straight from the pool; derived steps have none and never dive.
	method  string
	params  []any
	compare bool // strict-comparable: a conflict earns the deep-dive
	run     func(ctx context.Context) (stepReport, error)
}

type stepReport struct {
	outcome string
	detail  string
	support int
	reject  int
	faults  int
}

func main() {
	if err := run(); err != nil {
		fail("%v", err)
	}
}

func run() error {
	var (
		file       = flag.String("file", "", "local rpcs.json (default: fetch "+registryURL+")")
		chainFlag  = flag.Uint64("chain", 1, "chain id to soak")
		maxSources = flag.Int("max-sources", 12, "endpoint cap, 0 = all")
		timeout    = flag.Duration("timeout", 20*time.Second, "per-step request timeout")
		anchoring  = flag.String("anchoring", "finalized", "anchoring preset: finalized | strict")
		asJSON     = flag.Bool("json", false, "emit machine-readable JSON")
	)
	flag.Parse()

	endpoints := load(*file, *chainFlag, *maxSources)
	cfg := config.Config{ChainID: *chainFlag}
	for i, ep := range endpoints {
		cfg.Upstreams = append(cfg.Upstreams, config.Upstream{
			ID:       fmt.Sprintf("node-%02d", i+1),
			Endpoint: ep.URL,
		})
	}
	var opts []ethguard.Option
	if *anchoring == "finalized" {
		opts = append(opts, ethguard.WithFinalizedAnchoring())
	}
	client, err := ethguard.New(cfg, opts...)
	if err != nil {
		fail("build client: %v", err)
	}
	defer client.Close()

	anchor := mustAnchor(client, *timeout)
	// The height rides the same establishment as the hash (cached here), so
	// it survives a sampling conflict and the height-anchored steps keep
	// testing the real pinned block instead of degrading to genesis.
	anchorHeight := mustAnchorHeight(client, *timeout)
	txHash, sampleErr := sampleTx(client, anchor, *timeout)
	if sampleErr != nil {
		// Sampling is data, not a prerequisite: report what the fleet said
		// and continue — transaction steps degrade to "skipped".
		fmt.Fprintf(os.Stderr, "sampling: %v\n", sampleErr)
		diveInto(client, step{name: "sampling(getBlockByHash)", method: methods.MethodGetBlockByHash,
			params: []any{anchor, true}}, 2**timeout)
	}

	state := fleetState{client: client, anchor: anchor,
		anchorHeight: anchorHeight, txHash: txHash}
	steps := state.buildSteps(*chainFlag)

	results := make([]stepReport, len(steps))
	for i, s := range steps {
		ctx, cancel := context.WithTimeout(context.Background(), *timeout)
		report, err := s.run(ctx)
		cancel()
		if err != nil {
			report.outcome = "error: " + truncate(err.Error(), 120)
		}
		if report.outcome == "" {
			report.outcome = "error"
		}
		results[i] = report
		if s.compare && (strings.HasPrefix(report.outcome, "conflict") || strings.HasPrefix(report.outcome, "error:")) {
			// The deep-dive replays what the wire actually carried (post-
			// anchoring params) and gets a doubled timeout: the fleet is
			// slower on the second round than the census that triggered it.
			diveInto(client, s, 2**timeout)
		}
	}

	if *asJSON {
		if err := encodeJSON(os.Stdout, client, steps, results); err != nil {
			return fmt.Errorf("write JSON report: %w", err)
		}
		return nil
	}
	if err := printReport(os.Stdout, len(endpoints), *chainFlag, *anchoring, steps, results); err != nil {
		return fmt.Errorf("write text report: %w", err)
	}
	if err := printSources(os.Stdout, client); err != nil {
		return fmt.Errorf("write source health: %w", err)
	}
	return nil
}

// fleetState carries the derived references every step shares: one anchor
// established through the corroborated pipeline and one transaction sampled
// from the pinned block.
type fleetState struct {
	client       *ethguard.Client
	anchor       string
	anchorHeight uint64
	txHash       common.Hash
}

func (f fleetState) buildSteps(chainID uint64) []step {
	addr := ethapi.Address(strings.ToLower(soakAddress))
	contract := ethapi.Address(strings.ToLower(soakContract))
	slot0 := common.Hash{}
	return []step{
		{"chainId", methods.MethodChainID, nil, true, func(ctx context.Context) (stepReport, error) {
			result, err := f.client.Call(ctx, methods.MethodChainID)
			return classify(result.Decision, err, identityDetail(canonicalQuantity(f.client, methods.MethodChainID, result.Decision), chainID))
		}},
		{"net_version", methods.MethodNetVersion, nil, true, func(ctx context.Context) (stepReport, error) {
			result, err := f.client.Call(ctx, methods.MethodNetVersion)
			return classify(result.Decision, err, identityDetail(canonicalQuantity(f.client, methods.MethodNetVersion, result.Decision), chainID))
		}},
		{"blockNumber(derived)", "", nil, false, func(ctx context.Context) (stepReport, error) {
			height, _, err := f.client.BlockNumber(ctx)
			if err != nil {
				return stepReport{}, err
			}
			return stepReport{outcome: "derived", detail: fmt.Sprintf("height %d", height)}, nil
		}},
		{"getBlockByNumber(pinned)", methods.MethodGetBlockByHash, []any{f.anchor, true}, true, func(ctx context.Context) (stepReport, error) {
			block, _, err := f.client.BlockByNumber(ctx, nil)
			if err != nil {
				return stepReport{}, err
			}
			return stepReport{outcome: "ok", detail: string(block.Hash)}, nil
		}},
		{"getBlockByHash", methods.MethodGetBlockByHash, []any{f.anchor, true}, true, func(ctx context.Context) (stepReport, error) {
			result, err := f.client.Call(ctx, methods.MethodGetBlockByHash, f.anchor, true)
			var block *ethapi.Block
			if err == nil {
				block, err = typed.DecodeBlockAdmitted(result.Decision.Value)
			}
			return classify(result.Decision, err, blockDetail(block))
		}},
		{"getBalance", methods.MethodGetBalance, []any{addr, f.anchor}, true, func(ctx context.Context) (stepReport, error) {
			result, err := f.client.Call(ctx, methods.MethodGetBalance, addr, f.anchor)
			return classify(result.Decision, err, "")
		}},
		{"getBalance(typed)", "", nil, false, func(ctx context.Context) (stepReport, error) {
			bal, _, err := f.client.BalanceAt(ctx, addr, nil)
			if err != nil {
				return stepReport{}, err
			}
			return stepReport{outcome: "ok", detail: bal.String()}, nil
		}},
		{"getTransactionCount", methods.MethodGetTransactionCount, []any{addr, f.anchor}, true, func(ctx context.Context) (stepReport, error) {
			result, err := f.client.Call(ctx, methods.MethodGetTransactionCount, addr, f.anchor)
			return classify(result.Decision, err, "")
		}},
		{"getCode", methods.MethodGetCode, []any{contract, f.anchor}, true, func(ctx context.Context) (stepReport, error) {
			result, err := f.client.Call(ctx, methods.MethodGetCode, contract, f.anchor)
			return classify(result.Decision, err, "")
		}},
		{"getStorageAt", methods.MethodGetStorageAt, []any{contract, slot0, f.anchor}, true, func(ctx context.Context) (stepReport, error) {
			result, err := f.client.Call(ctx, methods.MethodGetStorageAt, contract, slot0, f.anchor)
			return classify(result.Decision, err, "")
		}},
		{"getTransactionByHash", methods.MethodGetTransactionByHash, []any{f.txHash}, true, func(ctx context.Context) (stepReport, error) {
			if f.txHash == (common.Hash{}) {
				return stepReport{outcome: "skipped", detail: "no sampled transaction"}, nil
			}
			result, err := f.client.Call(ctx, methods.MethodGetTransactionByHash, f.txHash)
			var tx *ethapi.Transaction
			if err == nil {
				tx, _, err = typed.DecodeTransaction(result.Decision.Value)
			}
			return classify(result.Decision, err, txDetail(tx))
		}},
		{"getTransactionReceipt", methods.MethodGetTransactionReceipt, []any{f.txHash}, true, func(ctx context.Context) (stepReport, error) {
			if f.txHash == (common.Hash{}) {
				return stepReport{outcome: "skipped", detail: "no sampled transaction"}, nil
			}
			result, err := f.client.Call(ctx, methods.MethodGetTransactionReceipt, f.txHash)
			var r *ethapi.Receipt
			if err == nil {
				r, err = typed.DecodeReceipt(result.Decision.Value)
			}
			return classify(result.Decision, err, receiptDetail(r))
		}},
		{"getBlockReceipts", methods.MethodGetBlockReceipts, []any{f.anchor}, true, func(ctx context.Context) (stepReport, error) {
			result, err := f.client.Call(ctx, methods.MethodGetBlockReceipts, f.anchor)
			var rs []ethapi.Receipt
			if err == nil {
				rs, err = typed.DecodeReceipts(result.Decision.Value)
			}
			return classify(result.Decision, err, fmt.Sprintf("%d receipts", len(rs)))
		}},
		{"getBlockReceipts(number)", methods.MethodGetBlockReceipts, []any{f.heightRef()}, true, func(ctx context.Context) (stepReport, error) {
			if f.anchorHeight > uint64(math.MaxInt64) {
				return stepReport{}, fmt.Errorf("anchor height %d exceeds geth block number range", f.anchorHeight)
			}
			ref, refErr := ethapi.NewBlockReference(json.RawMessage(fmt.Sprintf("%q", f.heightRef())))
			if refErr != nil {
				return stepReport{}, fmt.Errorf("build block reference: %w", refErr)
			}
			rs, _, err := f.client.BlockReceipts(ctx, ref)
			if err != nil {
				return stepReport{}, err
			}
			return stepReport{outcome: "ok", detail: fmt.Sprintf("%d receipts", len(rs))}, nil
		}},
		{"eth_call(totalSupply)", methods.MethodCall, []any{f.totalSupplyCall(), f.anchor}, true, func(ctx context.Context) (stepReport, error) {
			result, err := f.client.Call(ctx, methods.MethodCall, f.totalSupplyCall(), f.anchor)
			return classify(result.Decision, err, callResultDetail(result.Decision))
		}},
		{"estimateGas(totalSupply)", methods.MethodEstimateGas, []any{f.totalSupplyCall(), f.anchor}, true, func(ctx context.Context) (stepReport, error) {
			result, err := f.client.Call(ctx, methods.MethodEstimateGas, f.totalSupplyCall(), f.anchor)
			return classify(result.Decision, err, estimateDetail(result.Decision))
		}},
		{"getLogs(one-block range)", methods.MethodGetLogs, []any{f.logFilter()}, true, func(ctx context.Context) (stepReport, error) {
			result, err := f.client.Call(ctx, methods.MethodGetLogs, f.logFilter())
			return classify(result.Decision, err, logsDetail(result.Decision))
		}},
		{"feeHistory(percentiles)", methods.MethodFeeHistory, []any{uint64(2), f.heightRef(), []float64{25, 50, 75}}, true, func(ctx context.Context) (stepReport, error) {
			result, err := f.client.Call(ctx, methods.MethodFeeHistory, uint64(2), f.heightRef(), []float64{25, 50, 75})
			var detail string
			if err == nil {
				if history, derr := typed.DecodeFeeHistory(result.Decision.Value); derr == nil {
					detail = fmt.Sprintf("oldest %s, %d base fees", history.OldestBlock, len(history.BaseFeePerGas))
				}
			}
			return classify(result.Decision, err, detail)
		}},
		{"gasPrice", methods.MethodGasPrice, nil, false, func(ctx context.Context) (stepReport, error) {
			result, err := f.client.Call(ctx, methods.MethodGasPrice)
			return classify(result.Decision, err, bigDetail(canonicalQuantity(f.client, methods.MethodGasPrice, result.Decision)))
		}},
		{"maxPriorityFeePerGas", methods.MethodMaxPriorityFeePerGas, nil, false, func(ctx context.Context) (stepReport, error) {
			result, err := f.client.Call(ctx, methods.MethodMaxPriorityFeePerGas)
			return classify(result.Decision, err, bigDetail(canonicalQuantity(f.client, methods.MethodMaxPriorityFeePerGas, result.Decision)))
		}},
		{"net_peerCount", methods.MethodPeerCount, nil, false, func(ctx context.Context) (stepReport, error) {
			result, err := f.client.Call(ctx, methods.MethodPeerCount)
			n := canonicalQuantity(f.client, methods.MethodPeerCount, result.Decision)
			detail := ""
			if n != nil {
				detail = fmt.Sprintf("%d peers", n)
			}
			return classify(result.Decision, err, detail)
		}},
		{"eth_syncing", methods.MethodSyncing, nil, false, func(ctx context.Context) (stepReport, error) {
			result, err := f.client.Call(ctx, methods.MethodSyncing)
			var progress *ethapi.SyncProgress
			if err == nil {
				progress, err = typed.DecodeSyncProgress(result.Decision.Value)
			}
			return classify(result.Decision, err, syncingDetail(progress))
		}},
		{"net_listening", methods.MethodListening, nil, false, func(ctx context.Context) (stepReport, error) {
			result, err := f.client.Call(ctx, methods.MethodListening)
			var listening bool
			if err == nil {
				err = json.Unmarshal(result.Decision.Value, &listening)
			}
			return classify(result.Decision, err, fmt.Sprintf("%v", listening))
		}},
	}
}

// canonicalQuantity reduces one decision's canonical quantity spelling for
// the census detail; nil on any decode trouble (the census row keeps the
// outcome regardless).
func canonicalQuantity(client *ethguard.Client, method string, dec *rpc.Decision) *big.Int {
	if dec == nil {
		return nil
	}
	canonical, err := client.Normalize(method, dec.Value)
	if err != nil {
		return nil
	}
	value, ok := new(big.Int).SetString(canonical, 10)
	if !ok {
		return nil
	}
	return value
}

// classify turns one decision into a census row. On the conflict path the
// decision still carries the winning cluster, so the counts stay informative.
func classify(dec *rpc.Decision, err error, detail string) (stepReport, error) {
	report := stepReport{detail: detail}
	if dec != nil {
		report.support = len(dec.Support)
		report.reject = len(dec.Reject)
		report.faults = len(dec.Faults)
	}
	switch {
	case err == nil && dec != nil:
		report.outcome = dec.Status.String()
	case errors.Is(err, rpc.ErrInsufficientSupport), errors.Is(err, rpc.ErrGuaranteeNotMet):
		report.outcome = "support-unmet"
		report.detail = ""
	case errors.Is(err, rpc.ErrConflict):
		report.outcome = "conflict"
		report.detail = ""
	case errors.Is(err, ethereum.NotFound):
		report.outcome = "notfound"
		report.detail = ""
	default:
		report.outcome = "error: " + truncate(err.Error(), 120)
	}
	return report, nil
}

// diveInto samples a step again through the client and clusters each
// source's canonical answer. This is a second fan-out and costs one extra
// round of requests; it runs on strict-comparable conflicts and on whole-
// step failures, where "which source said what, and which failed how" is
// the finding itself. These diagnostics go to stderr on a best-effort basis;
// report output is checked separately.
func diveInto(client *ethguard.Client, s step, timeout time.Duration) {
	if s.method == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	emissions, err := client.Sample(ctx, s.method, s.params...)
	if err != nil {
		fmt.Fprintf(os.Stderr, "  deep-dive %s: collect: %v\n", s.name, err)
		return
	}
	var order []string
	clusters := map[string][]string{}
	faults := map[string]int{}
	faulted := 0
	for _, em := range emissions {
		if em.Response.Err() != nil {
			msg := truncate(em.Response.Err().Error(), 90)
			faults[msg]++
			faulted++
			continue
		}
		canonical, err := client.Normalize(s.method, em.Response.Answer.Result)
		if err != nil {
			msg := "normalize: " + truncate(err.Error(), 80)
			faults[msg]++
			faulted++
			continue
		}
		if _, seen := clusters[canonical]; !seen {
			order = append(order, canonical)
		}
		clusters[canonical] = append(clusters[canonical], em.Source.Name)
	}
	fmt.Fprintf(os.Stderr, "  deep-dive %s: %d clusters, %d faulted\n", s.name, len(order), faulted)
	for _, canonical := range order {
		fmt.Fprintf(os.Stderr, "    %2d× %-40s %s\n", len(clusters[canonical]),
			strings.Join(clusters[canonical], ","), truncate(canonical, 120))
	}
	msgs := make([]string, 0, len(faults))
	for msg := range faults {
		msgs = append(msgs, msg)
	}
	sort.Strings(msgs)
	for _, msg := range msgs {
		fmt.Fprintf(os.Stderr, "    fault %2d× %s\n", faults[msg], msg)
	}
}

func load(file string, chainID uint64, max int) []chainlist.Endpoint {
	var r io.Reader
	if file != "" {
		f, err := os.Open(file)
		if err != nil {
			fail("open %s: %v", file, err)
		}
		defer f.Close() //nolint:errcheck // Read-only registry input; parsing owns data errors and Close commits no output.
		r = f
	} else {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, registryURL, nil)
		if err != nil {
			fail("build request: %v", err)
		}
		req.Header.Set("User-Agent", userAgent)
		hc := &http.Client{Timeout: time.Minute}
		resp, err := hc.Do(req)
		if err != nil {
			fail("fetch registry: %v", err)
		}
		defer resp.Body.Close() //nolint:errcheck // Read-only response; parsing owns data errors and Close only releases the connection.
		if resp.StatusCode != http.StatusOK {
			fail("fetch registry: status %s", resp.Status)
		}
		r = io.LimitReader(resp.Body, limits.MaxBodyBytes)
	}
	chains, err := chainlist.Parse(r)
	if err != nil {
		fail("parse registry: %v", err)
	}
	eps := chainlist.ForChain(chains, chainID, max)
	if len(eps) == 0 {
		fail("no endpoints for chain %d", chainID)
	}
	// Duplicate URLs exist in the registry and the pool deduplicates by
	// endpoint; drop repeats here so the source count stays honest.
	seen := map[string]bool{}
	out := eps[:0]
	for _, ep := range eps {
		if seen[ep.URL] {
			continue
		}
		seen[ep.URL] = true
		out = append(out, ep)
	}
	return out
}

func mustAnchor(client *ethguard.Client, timeout time.Duration) string {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	anchor, _, err := client.AnchorBlockHash(ctx)
	if err != nil {
		fail("establish anchor: %v", err)
	}
	return anchor
}

// sampleTx takes the first transaction of the pinned block so every
// reference the soak tests is derived from the fleet itself. A failure is
// reported, not fatal — the soak degrades to skipping transaction steps.
func sampleTx(client *ethguard.Client, anchor string, timeout time.Duration) (common.Hash, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	block, _, err := client.BlockByHash(ctx, ethapi.Hash(strings.ToLower(anchor)))
	if err != nil {
		return common.Hash{}, fmt.Errorf("fetch pinned block: %w", err)
	}
	txs := block.Transactions
	if txs.FullTransactions == nil || len(*txs.FullTransactions) == 0 {
		return common.Hash{}, nil
	}
	first := (*txs.FullTransactions)[0]
	hash, ok := blockTransactionHash(first)
	if !ok {
		return common.Hash{}, fmt.Errorf("pinned block transaction %s carries no selected branch", string(firstHash(first)))
	}
	return common.HexToHash(hash), nil
}

// mustAnchorHeight resolves the pinned reference's height through the
// corroborated pipeline (AnchorHeightProvider); it shares the anchor cache.
func mustAnchorHeight(client *ethguard.Client, timeout time.Duration) uint64 {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	height, _, err := client.AnchorBlockHeight(ctx)
	if err != nil {
		fail("establish anchor height: %v", err)
	}
	return height
}

func printReport(out io.Writer, sources int, chainID uint64, anchoring string, steps []step, results []stepReport) error {
	if _, err := fmt.Fprintf(out, "== soak: %d sources, chain %d, anchoring=%s ==\n", sources, chainID, anchoring); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(out, "%-28s %-30s %s\n", "step", "outcome", "sup/rej/fault  detail"); err != nil {
		return err
	}
	for i, s := range steps {
		r := results[i]
		if _, err := fmt.Fprintf(out, "%-28s %-30s %d/%d/%d  %s\n", s.name, r.outcome, r.support, r.reject, r.faults, r.detail); err != nil {
			return err
		}
	}
	return nil
}

func printSources(out io.Writer, client *ethguard.Client) error {
	sources := client.Sources()
	sort.Slice(sources, func(i, j int) bool { return sources[i].Source.Name < sources[j].Source.Name })
	if _, err := fmt.Fprintln(out, "\n-- source health --"); err != nil {
		return err
	}
	for _, s := range sources {
		h := s.Health
		if _, err := fmt.Fprintf(out, "%-10s %-52s ok=%-5d fail=%-4d 429=%-3d p95=%s\n",
			s.Source.Name, truncate(s.Source.URL, 52), h.Successes, h.Failures, h.RateLimited, h.LatencyP95); err != nil {
			return err
		}
	}
	return nil
}

func encodeJSON(outWriter io.Writer, client *ethguard.Client, steps []step, results []stepReport) error {
	type row struct {
		Step    string `json:"step"`
		Outcome string `json:"outcome"`
		Support int    `json:"support,omitempty"`
		Reject  int    `json:"reject,omitempty"`
		Faults  int    `json:"faults,omitempty"`
		Detail  string `json:"detail,omitempty"`
	}
	type src struct {
		Name        string `json:"name"`
		URL         string `json:"url"`
		Successes   uint64 `json:"successes"`
		Failures    uint64 `json:"failures"`
		RateLimited uint64 `json:"rateLimited,omitempty"`
		LatencyP95  string `json:"p95,omitempty"`
	}
	out := struct {
		Steps   []row `json:"steps"`
		Sources []src `json:"sources"`
	}{Steps: make([]row, 0, len(steps))}
	for i, s := range steps {
		out.Steps = append(out.Steps, row{Step: s.name, Outcome: results[i].outcome,
			Support: results[i].support, Reject: results[i].reject, Faults: results[i].faults, Detail: results[i].detail})
	}
	for _, s := range client.Sources() {
		h := s.Health
		out.Sources = append(out.Sources, src{Name: s.Source.Name, URL: s.Source.URL, Successes: h.Successes,
			Failures: h.Failures, RateLimited: h.RateLimited, LatencyP95: h.LatencyP95.String()})
	}
	return json.NewEncoder(outWriter).Encode(out)
}

// heightRef spells the pinned anchor height — the wire form the
// height-anchored methods (getLogs bounds, feeHistory newest) must carry,
// since their number positions reject hashes.
func (f fleetState) heightRef() string {
	return fmt.Sprintf("0x%x", f.anchorHeight)
}

// totalSupplyCall builds the canonical WETH totalSupply() read: a
// deterministic, gas-cheap call whose result is chain-determined at the
// pinned reference.
func (f fleetState) totalSupplyCall() map[string]any {
	return map[string]any{
		"from":  "0x0000000000000000000000000000000000000000",
		"to":    soakContract,
		"input": "0x18160ddd",
	}
}

// logFilter pins a one-block range at the anchor height, so the response is
// a deterministic log set (possibly empty) rather than a moving window.
func (f fleetState) logFilter() map[string]any {
	return map[string]any{
		"fromBlock": f.heightRef(),
		"toBlock":   f.heightRef(),
		"address":   soakContract,
	}
}

func callResultDetail(dec *rpc.Decision) string {
	if dec == nil {
		return ""
	}
	var data string
	if json.Unmarshal(dec.Value, &data) == nil {
		return truncate(data, 40)
	}
	return ""
}

func estimateDetail(dec *rpc.Decision) string {
	if dec == nil {
		return ""
	}
	var gas string
	if json.Unmarshal(dec.Value, &gas) == nil {
		return "gas " + gas
	}
	return ""
}

func logsDetail(dec *rpc.Decision) string {
	if dec == nil {
		return ""
	}
	var logs []json.RawMessage
	if json.Unmarshal(dec.Value, &logs) == nil {
		return fmt.Sprintf("%d logs", len(logs))
	}
	return ""
}

func identityDetail(v *big.Int, want uint64) string {
	if v == nil {
		return ""
	}
	if v.Uint64() != want {
		return fmt.Sprintf("IDENTITY MISMATCH: fleet says %d, want %d", v, want)
	}
	return fmt.Sprintf("%d", v)
}

func blockDetail(b *ethapi.Block) string {
	if b == nil {
		return ""
	}
	height, err := rpc.ParseHexQuantity(string(b.Number))
	if err != nil {
		return fmt.Sprintf("height %s", b.Number)
	}
	return fmt.Sprintf("height %d", height)
}

func txDetail(tx *ethapi.Transaction) string {
	if tx == nil {
		return ""
	}
	declared, err := typed.TransactionHash(tx)
	if err != nil {
		return "transaction union unselected"
	}
	return fmt.Sprintf("hash %s", declared)
}

func receiptDetail(r *ethapi.Receipt) string {
	if r == nil {
		return ""
	}
	detail := ""
	if r.Status != nil {
		if status, err := rpc.ParseHexQuantity(string(*r.Status)); err == nil {
			detail = fmt.Sprintf("status %d ", status)
		}
	}
	if gas, err := rpc.ParseHexQuantity(string(r.GasUsed)); err == nil {
		detail += fmt.Sprintf("gas %d", gas)
	}
	return strings.TrimSpace(detail)
}

// blockTransactionHash reads the declared hash of the selected embedded
// transaction branch; a zero-selected or multi-selected union reports false.
func blockTransactionHash(tx ethapi.BlockTransaction) (string, bool) {
	switch {
	case tx.Signed7702Transaction != nil:
		return string(tx.Signed7702Transaction.Hash), true
	case tx.Signed4844Transaction != nil:
		return string(tx.Signed4844Transaction.Hash), true
	case tx.Signed1559Transaction != nil:
		return string(tx.Signed1559Transaction.Hash), true
	case tx.Signed2930Transaction != nil:
		return string(tx.Signed2930Transaction.Hash), true
	case tx.SignedLegacyTransaction != nil:
		return string(tx.SignedLegacyTransaction.Hash), true
	default:
		return "", false
	}
}

// firstHash keeps the sampleTx error message honest when no branch is
// selected: the union's declared members cannot be read without a branch.
func firstHash(tx ethapi.BlockTransaction) ethapi.Hash {
	if hash, ok := blockTransactionHash(tx); ok {
		return ethapi.Hash(hash)
	}
	return ""
}

func syncingDetail(p *ethapi.SyncProgress) string {
	if p == nil {
		return "synced"
	}
	current, highest := "unknown", "unknown"
	if p.CurrentBlock != nil {
		current = string(*p.CurrentBlock)
	}
	if p.HighestBlock != nil {
		highest = string(*p.HighestBlock)
	}
	return fmt.Sprintf("syncing %s/%s", current, highest)
}

func bigDetail(v *big.Int) string {
	if v == nil {
		return ""
	}
	return v.String()
}

func truncate(s string, n int) string {
	if n <= 0 {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n]) + "..."
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "soak: "+format+"\n", args...)
	os.Exit(1)
}
