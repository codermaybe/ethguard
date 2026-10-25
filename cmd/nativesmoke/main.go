// Command nativesmoke runs a local, read-only compatibility smoke test of the
// native HTTP adapter against real public providers, side by side with the
// Geth compatibility adapter. It is a diagnostic tool for the Slice 2
// verification flow: its output is a local observation, not attested
// evidence, and it never signs or broadcasts anything.
//
// Per provider and adapter it issues, on one long-lived transport:
//
//	eth_chainId               (envelope, ID/version echo, quantity decode)
//	eth_blockNumber           (envelope on a second call, connection reuse)
//	eth_getBlockByNumber latest=false (plain state read)
//	eth_call USDT.transfer    (deterministic revert; error.data presence)
//
// The native adapter validates the response contract strictly, so a native
// success is itself evidence that the provider echoes the request ID, sends
// jsonrpc 2.0, and produces well-formed errors. Where Geth succeeds and the
// native adapter fails, the tool records the conflict class for adjudication.
//
// Usage:
//
//	go run ./cmd/nativesmoke -out report.json [-timeout 10s] \
//	    [-providers https://a,https://b] [-adapters native,geth]
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"runtime/debug"
	"strings"
	"time"

	"github.com/codermaybe/ethguard/internal/rpcwire"
	"github.com/codermaybe/ethguard/internal/transport"
	nativehttp "github.com/codermaybe/ethguard/internal/transport/http"
	"github.com/codermaybe/ethguard/rpc"
)

var defaultProviders = []string{
	"https://ethereum-rpc.publicnode.com",
	"https://eth.llamarpc.com",
	"https://eth.drpc.org",
	"https://cloudflare-eth.com",
	"https://eth-mainnet.public.blastapi.io",
	"https://1rpc.io/eth",
}

// usdtTransferRevertCallData is transfer(address,uint256) on the mainnet USDT
// proxy with a dead recipient and a max amount: a deterministic revert whose
// data carries the string reason on most nodes. eth_call persists nothing.
const usdtTransferRevertCallData = "0xa9059cbb" +
	"000000000000000000000000000000000000000000000000000000000000dead" +
	"ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"

type callResult struct {
	Call        string `json:"call"`
	OK          bool   `json:"ok"`
	Value       string `json:"value,omitempty"`
	ErrClass    string `json:"err_class,omitempty"`
	ErrCode     *int   `json:"err_code,omitempty"`
	ErrMsg      string `json:"err_msg,omitempty"`
	DataPresent bool   `json:"data_present,omitempty"`
	DataRaw     string `json:"data_raw,omitempty"`
	MS          int64  `json:"ms"`
}

type adapterReport struct {
	Calls      []callResult `json:"calls"`
	ChainID    string       `json:"chain_id,omitempty"`
	EnvelopeOK bool         `json:"envelope_ok"`
	DialErr    string       `json:"dial_err,omitempty"`
}

type conflictReport struct {
	Call   string `json:"call"`
	Geth   string `json:"geth"`
	Native string `json:"native"`
	Class  string `json:"class"`
}

type providerReport struct {
	URL       string                    `json:"url"`
	Adapters  map[string]*adapterReport `json:"adapters"`
	Conflicts []conflictReport          `json:"conflicts"`
	Verdict   string                    `json:"verdict"`
}

type smokeReport struct {
	GeneratedAt string           `json:"generated_at"`
	Commit      string           `json:"commit,omitempty"`
	Providers   []providerReport `json:"providers"`
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "nativesmoke: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	fs := flag.NewFlagSet("nativesmoke", flag.ContinueOnError)
	providers := fs.String("providers", strings.Join(defaultProviders, ","), "comma-separated provider URLs")
	adapters := fs.String("adapters", "native,geth", "comma-separated adapters to exercise")
	timeout := fs.Duration("timeout", 10*time.Second, "per-call timeout")
	out := fs.String("out", "", "optional JSON report output path")
	if err := fs.Parse(args); err != nil {
		return err
	}
	urls := strings.Split(strings.TrimSpace(*providers), ",")
	want := strings.Split(strings.TrimSpace(*adapters), ",")

	report := smokeReport{GeneratedAt: time.Now().UTC().Format(time.RFC3339)}
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, s := range info.Settings {
			if s.Key == "vcs.revision" {
				report.Commit = s.Value
			}
		}
	}
	for _, rawURL := range urls {
		u := strings.TrimSpace(rawURL)
		if u == "" {
			continue
		}
		report.Providers = append(report.Providers, smokeProvider(u, want, *timeout))
	}
	encoded, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	if *out != "" {
		if err := os.WriteFile(*out, append(encoded, '\n'), 0o644); err != nil {
			return err
		}
		fmt.Printf("report=%s\n", *out)
	}
	printSummary(report)
	return nil
}

func smokeProvider(rawURL string, adapters []string, timeout time.Duration) providerReport {
	pr := providerReport{URL: rawURL, Adapters: map[string]*adapterReport{}}
	for _, a := range adapters {
		pr.Adapters[a] = smokeAdapter(rawURL, a, timeout)
	}
	pr.Conflicts, pr.Verdict = compare(pr)
	return pr
}

func smokeAdapter(rawURL, adapter string, timeout time.Duration) *adapterReport {
	dialer := dialerFor(adapter)
	if dialer == nil {
		return &adapterReport{DialErr: "unknown adapter " + adapter}
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout*8)
	defer cancel()
	tr, err := dialer.DialContext(ctx, rawURL)
	ar := &adapterReport{}
	if err != nil {
		ar.DialErr = err.Error()
		return ar
	}
	defer tr.Close()

	run := func(name string, method string, args ...any) callResult {
		cctx, ccancel := context.WithTimeout(ctx, timeout)
		defer ccancel()
		var raw json.RawMessage
		start := time.Now()
		err := tr.CallContext(cctx, &raw, method, args...)
		res := callResult{Call: name, MS: time.Since(start).Milliseconds()}
		switch {
		case err == nil:
			res.OK = true
			res.Value = truncate(string(raw), 240)
		default:
			var pe rpc.ProtocolError
			if errors.As(err, &pe) {
				res.ErrClass = "jsonrpc_error"
				code := pe.ErrorCode()
				res.ErrCode = &code
				res.ErrMsg = truncate(pe.Error(), 240)
				var presence rpc.ErrorDataPresence
				if errors.As(err, &presence) {
					if d, ok := presence.ErrorDataJSON(); ok {
						res.DataPresent = true
						res.DataRaw = truncate(string(d), 240)
					}
				}
			} else {
				res.ErrClass = classify(err)
				res.ErrMsg = truncate(err.Error(), 240)
			}
		}
		return res
	}

	chain := run("eth_chainId", "eth_chainId")
	ar.Calls = append(ar.Calls, chain)
	block := run("eth_blockNumber", "eth_blockNumber")
	ar.Calls = append(ar.Calls, block)
	ar.Calls = append(ar.Calls, run("eth_getBlockByNumber", "eth_getBlockByNumber", "latest", false))
	ar.Calls = append(ar.Calls, run("eth_call_revert_probe", "eth_call",
		map[string]any{"to": "0xdAC17F958D2ee523a2206206994597C13D831ec7", "data": usdtTransferRevertCallData},
		"latest"))

	ar.ChainID = chain.Value
	ar.EnvelopeOK = chain.OK && block.OK
	return ar
}

func dialerFor(adapter string) transport.Dialer {
	switch adapter {
	case "native":
		return nativehttp.NewDialer()
	case "geth":
		return transport.StandardDialer{}
	}
	return nil
}

// classify maps a transport-level failure to the native contract's sentinel
// classes when applicable; anything else is reported verbatim.
func classify(err error) string {
	switch {
	case errors.Is(err, rpcwire.ErrIDMismatch):
		return "id_mismatch"
	case errors.Is(err, rpcwire.ErrMalformedResponse):
		return "malformed_response"
	case errors.Is(err, rpcwire.ErrEmptyResponse):
		return "empty_response"
	case errors.Is(err, transport.ErrBodyTooLarge):
		return "body_too_large"
	default:
		return "transport_error"
	}
}

func compare(pr providerReport) ([]conflictReport, string) {
	native, geth := pr.Adapters["native"], pr.Adapters["geth"]
	if native == nil || geth == nil {
		return nil, "single_adapter"
	}
	var conflicts []conflictReport
	byName := func(ar *adapterReport) map[string]callResult {
		m := map[string]callResult{}
		for _, c := range ar.Calls {
			m[c.Call] = c
		}
		return m
	}
	nm, gm := byName(native), byName(geth)
	for _, name := range []string{"eth_chainId", "eth_blockNumber", "eth_getBlockByNumber", "eth_call_revert_probe"} {
		n, g := nm[name], gm[name]
		switch {
		case g.OK && n.OK:
			// consistent
		case g.OK && !n.OK:
			conflicts = append(conflicts, conflictReport{
				Call: name, Geth: "ok", Native: n.ErrClass + ": " + n.ErrMsg, Class: n.ErrClass,
			})
		case !g.OK && n.OK:
			conflicts = append(conflicts, conflictReport{
				Call: name, Geth: "fail: " + g.ErrMsg, Native: "ok", Class: "geth_only_failure",
			})
		case g.ErrClass != n.ErrClass:
			// Both adapters failed the call, but they classified it
			// differently — for example a 200 body carrying a JSON-RPC error
			// with "id":null reads as a protocol error under Geth and as an
			// ID mismatch under the native contract. This is provider
			// evidence for adjudication, not an availability fault.
			conflicts = append(conflicts, conflictReport{
				Call:   name,
				Geth:   g.ErrClass + ": " + g.ErrMsg,
				Native: n.ErrClass + ": " + n.ErrMsg,
				Class:  "divergent_failure",
			})
		default:
			// both failed with the same classification: availability
		}
	}
	verdict := "compatible"
	unreachable := 0
	for _, ar := range []*adapterReport{native, geth} {
		if ar.DialErr != "" || len(ar.Calls) == 0 || (!ar.Calls[0].OK && !ar.Calls[1].OK) {
			unreachable++
		}
	}
	switch {
	case len(conflicts) > 0 && allContractConflicts(conflicts):
		verdict = "contract_conflict"
	case len(conflicts) > 0:
		verdict = "mixed"
	case unreachable == 2:
		verdict = "unreachable"
	case unreachable == 1:
		verdict = "partially_unreachable"
	}
	return conflicts, verdict
}

func allContractConflicts(cs []conflictReport) bool {
	for _, c := range cs {
		if c.Class == "geth_only_failure" {
			return false
		}
	}
	return true
}

func printSummary(r smokeReport) {
	fmt.Println("provider\tverdict\tconflicts\tnative envelope\tchain id")
	for _, p := range r.Providers {
		env := "?"
		if ar := p.Adapters["native"]; ar != nil {
			env = fmt.Sprintf("%v", ar.EnvelopeOK)
		}
		chain := p.Adapters["native"].ChainID
		fmt.Printf("%s\t%s\t%d\t%s\t%s\n", p.URL, p.Verdict, len(p.Conflicts), env, chain)
		for _, c := range p.Conflicts {
			fmt.Printf("  conflict %s: class=%s geth=%s native=%s\n", c.Call, c.Class, truncate(c.Geth, 90), truncate(c.Native, 90))
		}
	}
}

func truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	s = strings.Map(func(r rune) rune {
		if r < 0x20 {
			return ' '
		}
		return r
	}, s)
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}
