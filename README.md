# EthGuard

**English** · [简体中文](README.zh-CN.md)

EthGuard is a fail-closed, multi-source Ethereum execution RPC client for Go. It validates provider responses against method-specific schemas and applicable cryptographic content commitments, pins moving tags (`latest`, `safe`, `finalized`) to a common per-call reference, and adjudicates answers across a fixed set of HTTP, WebSocket, and local IPC sources.

> **Status & honesty boundary**
>
> Unified construction, inferred configuration, native HTTP / raw WSS / Unix IPC transports, generated `ethapi` models, and the 47-method `ethadapter` (locked to `go-ethereum` v1.17.10) have passed local ordinary and `-race` test suites. Windows named pipe runtime tests also passed in PR #13 CI at commit `3fdb6b7`.
>
> `Corroborated` means **provider-backed cross-source evidence** across your configured endpoints — not Ethereum consensus, canonical-chain membership, cryptographic finality, or proof that your providers are independent operators.
> Complete `SimulateV1` semantics and real-provider evidence remain separate verification tracks; this prerelease is not a complete compatibility or stable-release claim. See the [coverage](docs/design/method-transport-coverage.md).

---

## Install

Requires Go `1.26.11` (see `go.mod`). Examples target `v0.0.1-beta.4`.

```sh
go get github.com/codermaybe/ethguard@v0.0.1-beta.4
```

---

## Quick Start (Recommended: `ethadapter`)

Existing `go-ethereum` / `bind/v2` applications can keep familiar business method signatures through `ethadapter`, with the validation, error, and subscription differences described below. `ethadapter.Client` exposes the exact 47 business methods of `ethclient.Client` (excluding `Client() *rpc.Client`) and satisfies `bind/v2.ContractBackend` and `bind/v2.DeployBackend`.

```go
package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/codermaybe/ethguard/ethadapter"
	"github.com/ethereum/go-ethereum/common"
)

func main() {
	// Pass 2+ distinct endpoints (HTTP, WSS, or IPC) for multi-source corroboration,
	// or 1 endpoint (e.g. "/var/run/geth.ipc") for explicit single-source Forwarded mode.
	client, err := ethadapter.Dial(
		"https://rpc-a.example",
		"wss://rpc-b.example",
		"/var/run/geth.ipc",
	)
	if err != nil {
		log.Fatal(err)
	}
	defer client.Close() // One shared client per app lifetime; connections are lazy and reused.

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Moving tags (nil / latest / safe / finalized) are pinned to a common per-call reference.
	bal, err := client.BalanceAt(ctx, common.HexToAddress("0x000000000000000000000000000000000000dead"), nil)
	if err != nil {
		log.Print(err)
		return
	}
	fmt.Println("balance:", bal)
}
```

### Choose your surface

EthGuard has **one execution owner (`*ethguard.Client`)** and two convenient surfaces on top of it:

| Surface | Import / access | Best for | Return shape |
| --- | --- | --- | --- |
| **`ethadapter.Client`** | `.../ethguard/ethadapter` | Existing `go-ethereum` & `bind/v2` apps | Standard geth types (`*types.Block`, `common.Address`, `ethereum.Subscription`) |
| **`ethguard.Client`** | `.../ethguard` | Full adjudication evidence, atomic reorg streams, zero geth-client dependency | Generated `ethapi` models + `CallResult` + `error` |
| **`ethguard.EthClient`** | `core.EthClient()` | Lightweight callers using `ethapi` models without `CallResult` | Generated `ethapi` models + `error` |

`ethadapter.New(core)` and `adapter.Core()` share the exact same underlying client and `Close()` lifecycle — move freely between them without opening a second pool.

---

## Common Workflows

### 1. Construction: single-source vs. multi-source, Go options, and YAML

Every construction entry follows **one rule determined at startup**:

- **0 endpoints** or **duplicate endpoints** (after canonicalization) → error at startup.
- **1 endpoint** → `Forwarded` mode (method-specific validation and integrity checks remain active; successful read decisions are honestly marked `Forwarded`, never `Corroborated`).
- **2+ distinct endpoints** → `Majority` mode with a fixed voting basis and minimum support floor of 2. **A multi-source client never downgrades to single-source mode if upstreams fail at runtime.**

Construction is **lazy by default**: calls open connections as needed. Explicitly enabling `WithTipTracking` starts periodic background probes. `DialContext` and `OpenContext` only check cancellation at entry and never bind `ctx` to the client lifetime. `Dial` does not pin a chain ID; use `WithChainID` or YAML `chain_id` when required.

```go
// Multi-source + Go options (e.g. chain pinning, structured logging, finalized anchoring):
core, err := ethguard.Open(
	[]string{"https://rpc-a.example", "wss://rpc-b.example"},
	ethguard.WithChainID(1),
	ethguard.WithFinalizedAnchoring(), // requires 2+ sources; rejected on single-source
)
if err != nil {
	return err
}
client := ethadapter.New(core)
defer client.Close()
```

Or load from YAML — **`id` and `transport` are optional**:

```yaml
# config.yaml
chain_id: 1
upstreams:
  - endpoint: https://rpc-a.example    # inferred: native-http, id: rpc-1
  - endpoint: wss://rpc-b.example      # inferred: wss, id: rpc-2
  - endpoint: /var/run/geth.ipc        # inferred: ipc, id: rpc-3 (Unix socket or \\.\pipe\...)
```

```go
core, err := ethguard.LoadFile("config.yaml")
if err != nil {
	return err
}
client := ethadapter.New(core)
defer client.Close()
```

Endpoint syntax rules:

- **HTTP(S) / WS(S)**: full URLs with scheme and host (`http://`, `https://`, `ws://`, `wss://`).
- **IPC**: explicit absolute path (`/var/run/geth.ipc`), explicit relative path (`./geth.ipc`, `../geth.ipc` — frozen against `cwd` at construction time), `ipc:///abs/path`, or Windows named pipe (`\\.\pipe\geth.ipc`). Ambiguous bare names like `geth.ipc` or malformed URLs are rejected.

### 2. Using `bind/v2` contracts and reading revert data

```go
// bind is github.com/ethereum/go-ethereum/accounts/abi/bind/v2.
// mytoken is generated with abigen --v2 for a contract exposing totalSupply().
token := mytoken.NewMyToken()
instance := token.Instance(client, tokenAddr)
supply, err := bind.Call(instance, &bind.CallOpts{Context: ctx}, token.PackTotalSupply(), token.UnpackTotalSupply)
if err != nil {
	// Core RevertError does not fabricate RPC error code 3, so use ethadapter.RevertErrorData
	// (or errors.As with *ethguard.RevertError) instead of ethclient.RevertErrorData.
	if data, ok := ethadapter.RevertErrorData(err); ok {
		log.Printf("contract reverted: 0x%x", data)
	}
	return err
}
fmt.Println("total supply:", supply)
```

### 3. Auditing & inspecting per-call evidence (`WithAudit`, `WithResult`, `Core()`)

Use `ethadapter` signatures while keeping full observability into multi-source votes, faults, and structural conversion omissions:

```go
// A) Global structured logging or custom audit callback (WithAudit wins over WithLogger):
client := ethadapter.New(core, ethadapter.WithLogger(slog.Default()))
// Or:
client = ethadapter.New(core, ethadapter.WithAudit(func(ctx context.Context, ev ethadapter.AuditEvent) {
	// ev.Method, ev.CallResult (core Decision/WriteDecision), ev.ConversionReport, ev.CoreErr, ev.AdapterErr
}))

// B) Per-call core result capture (never shadowed by ethadapter):
base, err := ethguard.WithSourceEvidence(ctx, 64<<10) // optional bounded raw source material
if err != nil {
	return err
}
callCtx, capture := ethguard.WithResult(base)
block, err := client.BlockByNumber(callCtx, nil)
saved, published, captureErr := capture.Result()
if captureErr != nil {
	log.Printf("result capture: %v", captureErr)
} else if published && saved.Decision != nil {
	// saved.Decision reflects the exact core outcome (even if adapter conversion later failed).
	fmt.Printf("status=%s support=%d/%d\n",
		saved.Decision.Status, len(saved.Decision.Support), saved.Decision.Adjudication.Basis)
}
if err != nil {
	return err
}
fmt.Println("block:", block.Hash())
```

The method's error remains the business outcome; capture errors describe evidence delivery. Each capture is single-use. Raw retention has byte, record, and text limits; truncation does not change votes or thresholds.

### 4. Subscriptions: geth channel bridge vs. native atomic reorg streams

Only subscription-capable sources (**WSS** and **IPC**) participate in subscriptions. A multi-source client with only one WSS/IPC source rejects subscription setup rather than silently dropping to single-source mode.

**Option A — Geth channel subscriptions (`ethadapter`)**

Zero policy tuning required. Because geth channels cannot express atomic path retractions, **any native path retraction (including zero-match log blocks) deterministically terminates the subscription with `ethguard.ErrReorgDetected`**. Your handler should roll back / rescan and resubscribe.

The context passed to `Subscribe*` bounds setup only. Use `Unsubscribe` or `Close` to stop the established bridge; the loop below also stops when its application context ends. If shutdown or another failure has already established a terminal cause, later retractions do not replace it.

```go
headers := make(chan *types.Header, 16)
setupCtx, cancelSetup := context.WithTimeout(ctx, 10*time.Second)
sub, err := client.SubscribeNewHead(setupCtx, headers)
cancelSetup()
if err != nil {
	return err
}
defer sub.Unsubscribe() // Also joined by client.Close() / client.Core().Close(), even if headers is unread.

for {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case h := <-headers:
		fmt.Println("new head:", h.Number, h.Hash())
	case err := <-sub.Err():
		if errors.Is(err, ethguard.ErrReorgDetected) {
			// Stopping the stream does not undo already-processed business state.
			return handleReorgAndResync(ctx)
		}
		return err
	}
}
```

**Option B — Native atomic path & reorg streams (`ethguard`)**

Use `Core().SubscribeNewHead`, `SubscribeFilterLogs`, or `SubscribeTransactionReceipts` for continuous atomic `Retracted` + `Appended` deltas and structured evidence. Logs and Receipts streams verify complete content commitments against accepted headers where needed; this proves content relationships, not consensus or finality. Reorgs within the configured recovery and resource bounds can be delivered without terminating the stream:

```go
query, err := ethapi.NewFilterQuery([]byte(`{}`)) // native ethapi query, not ethereum.FilterQuery
if err != nil {
	return err
}
policy := ethguard.DefaultLogsSubscriptionPolicy() // bounded preset; adjust fields as needed
sub, err := client.Core().SubscribeFilterLogs(ctx, query, policy)
if err != nil {
	return err
}
defer sub.Close()

for {
	ev, err := sub.Next(ctx)
	if err != nil {
		return err
	}
	// Apply ev.Path.Retracted and ev.Path.Appended atomically.
	applyDelta(ev.Path.Retracted, ev.Path.Appended)
}
```

These streams start at their birth anchor and do not automatically replay older history. Use historical reads for backfill.

### 5. Native `ethguard.Client` usage & diagnostics

If you do not want any `go-ethereum` client/adapter types in your dependency graph, use the root `ethguard` package directly with generated `ethapi` models:

```go
core, err := ethguard.Dial("https://rpc-a.example", "https://rpc-b.example")
if err != nil {
	return err
}
defer core.Close()

// Root returns (value, CallResult, error); core.EthClient() returns (value, error).
bal, res, err := core.BalanceAt(ctx, ethapi.Address("0x000000000000000000000000000000000000dead"), nil)
_, _, _ = bal, res, err

// Built-in diagnostics (no pool/transport escape hatches):
snapshots := core.Sources()                      // immutable member identities + health snapshots
rawObs, sampleErr := core.Sample(ctx, "eth_chainId") // raw per-source rpc.Response without adjudication
_, _, _ = snapshots, rawObs, sampleErr
```

Runnable examples: `go run ./examples/basic -config ./config/sample.yaml` and `go run ./examples/migration -config ./config/sample.yaml`.

---

## Key Behavioral Guarantees & Differences from `go-ethereum`

Common queries, contract calls, and signed transaction sends follow familiar `go-ethereum` signatures, with explicit fail-closed differences where multi-source integrity requires them:

- **Moving tags & anchoring**: `nil` / `latest` / `safe` / `finalized` establish a per-call reference first and pin the business read to it. On multi-source clients, failed anchoring sends no business request; on single-source clients, the same stage produces an honest `Forwarded` reference. Standalone `AnchorBlockHash*` / `AnchorBlockHeight` always require `Corroborated`. `WithFinalizedAnchoring` routes `default`/`latest` and `finalized` through finalized candidate selection (requires 2+ sources).
- **Content & cryptographic verification**: Full block reads verify header hash and applicable transaction, withdrawals, and uncle commitments; `ethadapter` restores uncle headers into `*types.Block` through `Decision.Provenance`. Ordinary receipt/log reads validate their documented fields and request bindings; they do not prove receipt-trie inclusion. Logs/Receipts content streams additionally verify applicable roots against accepted headers. Receipt fields outside `ReceiptsRoot`, such as `effectiveGasPrice`, remain provider claims subject to the method's adjudication rules. Invalid inputs, failed validation, or insufficient support return conversion or classified core errors; see the [method contracts](docs/design/method-contracts.md).
- **Pending queries**: `PendingBalanceAt`, `PendingNonceAt`, `PendingStorageAt`, `PendingCodeAt`, `PendingCallContract`, and `PendingTransactionCount` execute as node-local observations (`Selected` at best, never `Corroborated`). Pending typed `BlockByNumber(-1)`, `HeaderByNumber(-1)`, `BlockReceipts`, and `FilterLogs` are rejected locally.
- **Writes & sync writes**: `SendTransaction` / `SendRawTransaction` validate the signed payload locally, bind reported hashes to the submitted hash, broadcast to the fixed membership, and never auto-replay on unknown outcomes. `SendTransactionSync` / `SendRawTransactionSync` broadcast once and poll for a receipt on the client side (retaining bounded `CallResult.ReceiptWait` observations) rather than relying on server-side `eth_sendRawTransactionSync`.
- **Lifecycle**: `Close()` on any surface stops the shared client. Calls to a closed core fail before network I/O with `ErrPoolClosed`; local input validation may reject first. In-flight operations converge; `ethadapter` subscriptions join the same close barrier even when caller channels are unread. The library never closes caller-owned subscription channels.

---

## Architecture & Documentation

| Package | Responsibility |
| --- | --- |
| `ethguard` (root) | Public `Client`, `EthClient` view, `Dial`/`Open`/`New`/`LoadFile`, `CallResult`, `ResultCapture`, default subscription policies, diagnostics (`Sources`/`Sample`) |
| `ethadapter` | Peripheral `go-ethereum` v1.17.10 adapter (47 business methods, strict conversions, `RevertErrorData`, optional `WithAudit`/`WithLogger`, channel subscription bridge); core packages never import `ethadapter` |
| `ethapi` | Offline-generated Execution APIs standard models and strict JSON constructors |
| `rpc` / `config` / `observe` | Public request/response/decision contracts, YAML/endpoint config normalization, passive observability & health snapshots |
| `internal/runtime` | Sole production composition root |
| `internal/{rpcengine,aggregator,methods,projection,pool,reference}` | Read plans, canonical processing, adjudication, fixed-membership pool, reference selection |
| `internal/{ethobject,ethcodec,typed,uncertain,endpoint}` | Object integrity & binding tree, strict numeric/JSON primitives, typed projection, bounded raw retention, endpoint parser |
| `internal/transport/{http,streamrpc,wss,ipc}` & `internal/rpcwire` | Native HTTP, shared duplex RPC core, owned RFC6455 WSS wire, Unix/Windows IPC, shared JSON-RPC codec |
| `internal/subscription` | Single execution owner for `Head`, `Logs`, and `Receipts` continuous streams |
| `internal/diag/*`, `cmd/*`, `rpctest`, `internal/verification/*` | Diagnostic tools (`livecanary`, `specsync`, `specdiff`, `soak`, etc.) and verification suites |

Start at the **[Documentation Index](docs/design/README.md)** for full normative contracts:

- [Project Roadmap](docs/design/project-roadmap.md) — current status and open tracks
- [Access Plane, IPC & Adapter Contract](docs/design/access-plane-ipc-adapter.md) — unified construction, IPC, `ethadapter`, and verification records
- [Go-Ethereum Compatibility Policy](docs/design/go-ethereum-compatibility-policy.md) & [Method Coverage](docs/design/method-transport-coverage.md)
- [Codebase Topology](docs/design/codebase-topology.md), [Return Value Design](docs/design/return-value-design.md), [Reference Policy](docs/design/reference-freshness-policy.md), [Subscription Contract](docs/design/subscription-contract.md), [Transport Contract](docs/design/transport-contract.md), [WSS Wire Contract](docs/design/wss-wire-contract.md), [Wire Codegen](docs/design/wire-codegen.md), [Release Contract](docs/design/release-contract.md)

Ordinary tests (`go test ./...`) are fully offline. Anvil (`-tags anvil`), live provider canary, and release gates are explicit commands documented in the [Release Contract](docs/design/release-contract.md).
