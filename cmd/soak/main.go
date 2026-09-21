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
	"math/big"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	ethereum "github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	gethrpc "github.com/ethereum/go-ethereum/rpc"

	"ethguard"
	"ethguard/config"
	"ethguard/ethadapter"
	"ethguard/internal/chainlist"
	"ethguard/pool"
	"ethguard/rpc"
	"ethguard/rpc/methods"
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
	var (
		file       = flag.String("file", "", "local rpcs.json (default: fetch "+registryURL+")")
		chainFlag  = flag.Uint64("chain", 1, "chain id to soak")
		maxSources = flag.Int("max-sources", 12, "endpoint cap, 0 = all")
		timeout    = flag.Duration("timeout", 20*time.Second, "per-step request timeout")
		anchorTTL  = flag.Duration("anchor-ttl", time.Minute, "anchor cache ttl (0 = re-establish per read)")
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
	if *anchorTTL > 0 {
		opts = append(opts, ethguard.WithAnchorCaching(*anchorTTL))
	}
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
		fmt.Printf("sampling: %v\n", sampleErr)
		diveInto(client, step{name: "sampling(getBlockByHash)", method: methods.MethodGetBlockByHash,
			params: []any{anchor, true}}, 2**timeout)
	}

	state := fleetState{client: client, adapter: ethadapter.Wrap(client), anchor: anchor,
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
		encodeJSON(client, steps, results)
		return
	}
	printReport(len(endpoints), *chainFlag, *anchoring, steps, results)
	printSources(client)
}

// fleetState carries the derived references every step shares: one anchor
// established through the corroborated pipeline and one transaction sampled
// from the pinned block.
type fleetState struct {
	client       *ethguard.Client
	adapter      *ethadapter.Client
	anchor       string
	anchorHeight uint64
	txHash       common.Hash
}

func (f fleetState) buildSteps(chainID uint64) []step {
	addr := common.HexToAddress(soakAddress)
	contract := common.HexToAddress(soakContract)
	slot0 := common.Hash{}
	return []step{
		{"chainId", methods.MethodChainID, nil, true, func(ctx context.Context) (stepReport, error) {
			id, dec, err := f.client.ChainID(ctx)
			return classify(dec, err, identityDetail(id, chainID))
		}},
		{"net_version", methods.MethodNetVersion, nil, true, func(ctx context.Context) (stepReport, error) {
			ver, dec, err := f.client.NetVersion(ctx)
			return classify(dec, err, identityDetail(ver, chainID))
		}},
		{"blockNumber(derived)", "", nil, false, func(ctx context.Context) (stepReport, error) {
			height, err := f.client.BlockNumber(ctx)
			if err != nil {
				return stepReport{}, err
			}
			return stepReport{outcome: "derived", detail: fmt.Sprintf("height %d", height)}, nil
		}},
		{"getBlockByNumber(pinned)", methods.MethodGetBlockByHash, []any{f.anchor, true}, true, func(ctx context.Context) (stepReport, error) {
			block, err := f.adapter.BlockByNumber(ctx, nil)
			if err != nil {
				return stepReport{}, err
			}
			return stepReport{outcome: "ok", detail: block.Hash().Hex()}, nil
		}},
		{"getBlockByHash", methods.MethodGetBlockByHash, []any{f.anchor, true}, true, func(ctx context.Context) (stepReport, error) {
			block, dec, err := f.client.BlockByHash(ctx, common.HexToHash(f.anchor), true)
			return classify(dec, err, blockDetail(block))
		}},
		{"getBalance", methods.MethodGetBalance, []any{addr, f.anchor}, true, func(ctx context.Context) (stepReport, error) {
			dec, err := f.client.Call(ctx, methods.MethodGetBalance, addr, f.anchor)
			return classify(dec, err, "")
		}},
		{"getBalance(typed)", "", nil, false, func(ctx context.Context) (stepReport, error) {
			bal, err := f.adapter.BalanceAt(ctx, addr, nil)
			if err != nil {
				return stepReport{}, err
			}
			return stepReport{outcome: "ok", detail: bal.String()}, nil
		}},
		{"getTransactionCount", methods.MethodGetTransactionCount, []any{addr, f.anchor}, true, func(ctx context.Context) (stepReport, error) {
			dec, err := f.client.Call(ctx, methods.MethodGetTransactionCount, addr, f.anchor)
			return classify(dec, err, "")
		}},
		{"getCode", methods.MethodGetCode, []any{contract, f.anchor}, true, func(ctx context.Context) (stepReport, error) {
			dec, err := f.client.Call(ctx, methods.MethodGetCode, contract, f.anchor)
			return classify(dec, err, "")
		}},
		{"getStorageAt", methods.MethodGetStorageAt, []any{contract, slot0, f.anchor}, true, func(ctx context.Context) (stepReport, error) {
			dec, err := f.client.Call(ctx, methods.MethodGetStorageAt, contract, slot0, f.anchor)
			return classify(dec, err, "")
		}},
		{"getTransactionByHash", methods.MethodGetTransactionByHash, []any{f.txHash}, true, func(ctx context.Context) (stepReport, error) {
			if f.txHash == (common.Hash{}) {
				return stepReport{outcome: "skipped", detail: "no sampled transaction"}, nil
			}
			tx, dec, err := f.client.TransactionByHash(ctx, f.txHash)
			return classify(dec, err, txDetail(tx))
		}},
		{"getTransactionReceipt", methods.MethodGetTransactionReceipt, []any{f.txHash}, true, func(ctx context.Context) (stepReport, error) {
			if f.txHash == (common.Hash{}) {
				return stepReport{outcome: "skipped", detail: "no sampled transaction"}, nil
			}
			r, dec, err := f.client.TransactionReceipt(ctx, f.txHash)
			return classify(dec, err, receiptDetail(r))
		}},
		{"getBlockReceipts", methods.MethodGetBlockReceipts, []any{f.anchor}, true, func(ctx context.Context) (stepReport, error) {
			rs, dec, err := f.client.BlockReceipts(ctx, common.HexToHash(f.anchor))
			return classify(dec, err, fmt.Sprintf("%d receipts", len(rs)))
		}},
		{"getBlockReceipts(number)", methods.MethodGetBlockReceipts, []any{f.heightRef()}, true, func(ctx context.Context) (stepReport, error) {
			rs, err := f.adapter.BlockReceipts(ctx, gethrpc.BlockNumberOrHashWithNumber(gethrpc.BlockNumber(f.anchorHeight)))
			if err != nil {
				return stepReport{}, err
			}
			return stepReport{outcome: "ok", detail: fmt.Sprintf("%d receipts", len(rs))}, nil
		}},
		{"eth_call(totalSupply)", methods.MethodCall, []any{f.totalSupplyCall(), f.anchor}, true, func(ctx context.Context) (stepReport, error) {
			dec, err := f.client.Call(ctx, methods.MethodCall, f.totalSupplyCall(), f.anchor)
			return classify(dec, err, callResultDetail(dec))
		}},
		{"estimateGas(totalSupply)", methods.MethodEstimateGas, []any{f.totalSupplyCall(), f.anchor}, true, func(ctx context.Context) (stepReport, error) {
			dec, err := f.client.Call(ctx, methods.MethodEstimateGas, f.totalSupplyCall(), f.anchor)
			return classify(dec, err, estimateDetail(dec))
		}},
		{"getLogs(one-block range)", methods.MethodGetLogs, []any{f.logFilter()}, true, func(ctx context.Context) (stepReport, error) {
			dec, err := f.client.Call(ctx, methods.MethodGetLogs, f.logFilter())
			return classify(dec, err, logsDetail(dec))
		}},
		{"feeHistory(percentiles)", methods.MethodFeeHistory, []any{uint64(2), f.heightRef(), []float64{25, 50, 75}}, true, func(ctx context.Context) (stepReport, error) {
			history, dec, err := f.client.FeeHistory(ctx, 2, new(big.Int).SetUint64(f.anchorHeight), []float64{25, 50, 75})
			if err != nil {
				return stepReport{}, err
			}
			detail := fmt.Sprintf("oldest %d, %d base fees", history.OldestBlock.Uint64(), len(history.BaseFeePerGas))
			return classify(dec, err, detail)
		}},
		{"gasPrice", methods.MethodGasPrice, nil, false, func(ctx context.Context) (stepReport, error) {
			price, dec, err := f.client.GasPrice(ctx)
			return classify(dec, err, bigDetail(price))
		}},
		{"maxPriorityFeePerGas", methods.MethodMaxPriorityFeePerGas, nil, false, func(ctx context.Context) (stepReport, error) {
			tip, dec, err := f.client.MaxPriorityFeePerGas(ctx)
			return classify(dec, err, bigDetail(tip))
		}},
		{"net_peerCount", methods.MethodPeerCount, nil, false, func(ctx context.Context) (stepReport, error) {
			n, dec, err := f.client.PeerCount(ctx)
			return classify(dec, err, fmt.Sprintf("%d peers", n))
		}},
		{"eth_syncing", methods.MethodSyncing, nil, false, func(ctx context.Context) (stepReport, error) {
			progress, dec, err := f.client.Syncing(ctx)
			return classify(dec, err, syncingDetail(progress))
		}},
		{"net_listening", methods.MethodListening, nil, false, func(ctx context.Context) (stepReport, error) {
			listening, dec, err := f.client.Listening(ctx)
			return classify(dec, err, fmt.Sprintf("%v", listening))
		}},
	}
}

// classify turns one decision into a census row. On the conflict path the
// decision still carries the winning cluster, so the counts stay informative.
func classify(dec rpc.Decision, err error, detail string) (stepReport, error) {
	report := stepReport{support: len(dec.Support), reject: len(dec.Reject), faults: len(dec.Faults), detail: detail}
	switch {
	case err == nil:
		report.outcome = dec.Status.String()
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

// diveInto re-collects a step straight from the pool and clusters each
// source's canonical answer. This is a second fan-out and costs one extra
// round of requests; it runs on strict-comparable conflicts and on whole-
// step failures, where "which source said what, and which failed how" is
// the finding itself.
func diveInto(client *ethguard.Client, s step, timeout time.Duration) {
	if s.method == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	emissions, err := client.Pool().Collect(ctx, s.method, s.params, pool.CollectOptions{})
	if err != nil {
		fmt.Printf("  deep-dive %s: collect: %v\n", s.name, err)
		return
	}
	var order []string
	clusters := map[string][]string{}
	faults := map[string]int{}
	faulted := 0
	for _, em := range emissions {
		if em.Err != nil {
			msg := truncate(em.Err.Error(), 90)
			faults[msg]++
			faulted++
			continue
		}
		canonical, err := client.Normalize(s.method, em.Val)
		if err != nil {
			msg := "normalize: " + truncate(err.Error(), 80)
			faults[msg]++
			faulted++
			continue
		}
		if _, seen := clusters[canonical]; !seen {
			order = append(order, canonical)
		}
		clusters[canonical] = append(clusters[canonical], em.Src.Name())
	}
	fmt.Printf("  deep-dive %s: %d clusters, %d faulted\n", s.name, len(order), faulted)
	for _, canonical := range order {
		fmt.Printf("    %2d× %-40s %s\n", len(clusters[canonical]),
			strings.Join(clusters[canonical], ","), truncate(canonical, 120))
	}
	msgs := make([]string, 0, len(faults))
	for msg := range faults {
		msgs = append(msgs, msg)
	}
	sort.Strings(msgs)
	for _, msg := range msgs {
		fmt.Printf("    fault %2d× %s\n", faults[msg], msg)
	}
}

func load(file string, chainID uint64, max int) []chainlist.Endpoint {
	var r io.Reader
	if file != "" {
		f, err := os.Open(file)
		if err != nil {
			fail("open %s: %v", file, err)
		}
		defer f.Close()
		r = f
	} else {
		req, err := http.NewRequest(http.MethodGet, registryURL, nil)
		if err != nil {
			fail("build request: %v", err)
		}
		req.Header.Set("User-Agent", userAgent)
		hc := &http.Client{Timeout: time.Minute}
		resp, err := hc.Do(req)
		if err != nil {
			fail("fetch registry: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			fail("fetch registry: status %s", resp.Status)
		}
		r = resp.Body
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
	anchor, err := client.AnchorBlockHash(ctx)
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
	block, err := ethadapter.Wrap(client).BlockByHash(ctx, common.HexToHash(anchor))
	if err != nil {
		return common.Hash{}, fmt.Errorf("fetch pinned block: %w", err)
	}
	txs := block.Transactions()
	if len(txs) == 0 {
		return common.Hash{}, nil
	}
	return txs[0].Hash(), nil
}

// mustAnchorHeight resolves the pinned reference's height through the
// corroborated pipeline (AnchorHeightProvider); it shares the anchor cache.
func mustAnchorHeight(client *ethguard.Client, timeout time.Duration) uint64 {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	height, err := client.AnchorBlockHeight(ctx)
	if err != nil {
		fail("establish anchor height: %v", err)
	}
	return height
}

func printReport(sources int, chainID uint64, anchoring string, steps []step, results []stepReport) {
	fmt.Printf("== soak: %d sources, chain %d, anchoring=%s ==\n", sources, chainID, anchoring)
	fmt.Printf("%-28s %-30s %s\n", "step", "outcome", "sup/rej/fault  detail")
	for i, s := range steps {
		r := results[i]
		fmt.Printf("%-28s %-30s %d/%d/%d  %s\n", s.name, r.outcome, r.support, r.reject, r.faults, r.detail)
	}
}

func printSources(client *ethguard.Client) {
	sources := client.Pool().Sources()
	sort.Slice(sources, func(i, j int) bool { return sources[i].Name() < sources[j].Name() })
	fmt.Printf("\n-- source health --\n")
	for _, s := range sources {
		h := s.Health()
		fmt.Printf("%-10s %-52s ok=%-5d fail=%-4d 429=%-3d p95=%s\n",
			s.Name(), truncate(s.URL(), 52), h.Successes, h.Failures, h.RateLimited, h.LatencyP95)
	}
}

func encodeJSON(client *ethguard.Client, steps []step, results []stepReport) {
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
	for _, s := range client.Pool().Sources() {
		h := s.Health()
		out.Sources = append(out.Sources, src{Name: s.Name(), URL: s.URL(), Successes: h.Successes,
			Failures: h.Failures, RateLimited: h.RateLimited, LatencyP95: h.LatencyP95.String()})
	}
	json.NewEncoder(os.Stdout).Encode(out)
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

func callResultDetail(dec rpc.Decision) string {
	var result string
	if json.Unmarshal(dec.Value, &result) == nil {
		return truncate(result, 40)
	}
	return ""
}

func estimateDetail(dec rpc.Decision) string {
	var gas string
	if json.Unmarshal(dec.Value, &gas) == nil {
		return "gas " + gas
	}
	return ""
}

func logsDetail(dec rpc.Decision) string {
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

func blockDetail(b *types.Block) string {
	if b == nil {
		return ""
	}
	return fmt.Sprintf("height %d", b.NumberU64())
}

func txDetail(tx *types.Transaction) string {
	if tx == nil {
		return ""
	}
	return fmt.Sprintf("nonce %d type %d", tx.Nonce(), tx.Type())
}

func receiptDetail(r *types.Receipt) string {
	if r == nil {
		return ""
	}
	return fmt.Sprintf("status %d gas %d", r.Status, r.GasUsed)
}

func syncingDetail(p *ethereum.SyncProgress) string {
	if p == nil {
		return "synced"
	}
	return fmt.Sprintf("syncing %d/%d", p.CurrentBlock, p.HighestBlock)
}

func bigDetail(v *big.Int) string {
	if v == nil {
		return ""
	}
	return v.String()
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "soak: "+format+"\n", args...)
	os.Exit(1)
}
