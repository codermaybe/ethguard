# EthGuard

**English** · [简体中文](README.zh-CN.md)

EthGuard queries third-party Ethereum execution RPC sources and returns method-aware results with explicit status and source evidence. It provides fixed-membership corroboration, common references for moving-state reads, native HTTP/raw WSS, writes and subscriptions.

Status: the main framework, the 47-method name target for go-ethereum v1.17.10, and the public generated-model migration are in place. Per-method semantics, provider observations and lifecycle evidence remain under review. This is not a complete compatibility or stable-release claim. See the [roadmap](docs/design/project-roadmap.md) and [coverage](docs/design/method-transport-coverage.md).

`Corroborated` means provider-backed cross-source evidence. It is not Ethereum consensus, canonical-chain membership, cryptographic finality or proof that endpoints are independent. Selected and Forwarded retain their weaker guarantees.

## Usage

Use one shared Client for its intended lifetime and close it when finished. Connections are lazy and reused; do not dial per request or clone pools per goroutine.

```go
client, err := ethguard.Dial("https://your-provider-a", "https://your-provider-b")
if err != nil { return err }
defer client.Close()

address := ethapi.Address("0x000000000000000000000000000000000000dead")
balance, result, err := client.BalanceAt(ctx, address, nil)
```

The root method returns its business value, a CallResult and the final business error. CallResult carries read or broadcast evidence, bounded receipt observations for synchronous writes, and any retained source material; its Err matches the trailing error. The EthClient view uses the same execution and omits CallResult:

```go
view := client.EthClient()
balance, err := view.BalanceAt(ctx, address, nil)
```

Public business models are generated `ethapi` types: Address/Hash, Block/Header/Transaction/Receipt/Log, CallMsg, query types and Simulate models. They do not have geth Go nominal type identity. Existing bind/v2 interfaces require an explicit adapter. Writes accept BinaryTransaction, an interface with MarshalBinary; a geth transaction can supply the bytes without becoming the public business model.

Exactly one endpoint requires explicit Forwarded mode:

```go
client, err := ethguard.DialUncorroborated("https://your-provider")
```

Dial refuses a single endpoint and duplicate endpoint identities. Multiple URLs still do not prove independent operators. Construction makes no network requests; DialContext bounds construction only. Chain identity is not implicitly pinned; configure WithChainID or chain_id explicitly when required.

## SDK boundary and diagnostics

The public SDK exposes Client and its EthClient view, generated ethapi models, rpc data contracts, configuration and observation values. Execution owners live under internal: pool, aggregator, method plans, resolvers, references, transports and subscription state machines. Public subscription types alias the single internal owner. The former public execution packages and Client.Pool() have no forwarding replacements.

Use client.Sources() for member identities and health snapshots in construction order. Use client.Sample(ctx, method, params...) for raw observations from currently eligible sources. Sampling freezes parameters once, follows the client lifecycle and collection timeout, applies cooldown and latest-tip gates, and updates transport health. It performs no reference establishment, method admission or adjudication. Returned response bytes are complete and caller-owned; observations can accompany a collection error. Normalize remains available separately for diagnostic comparison.

## Configuration

```go
client, err := ethguard.LoadFile("config.yaml")
```

```yaml
chain_id: 1
upstreams:
  - id: provider-a
    endpoint: https://rpc-a.example
  - id: provider-b
    endpoint: wss://rpc-b.example
    transport: wss
```

Omitted transport selects native HTTP. Explicit wss selects the raw WebSocket adapter; explicit source transport cannot be combined with DialerOption. Both source kinds participate in the fixed membership for reads and writes. Configuration changes to membership require a new Client.

Programmatic configuration uses config.FromEndpoints and ethguard.New; options such as WithChainID and WithFinalizedAnchoring apply to the same configuration model. The Go chain option wins over the file value. A runnable example is `go run ./examples/basic -config ./config.yaml`.

## Results and evidence

Deterministic typed reads require their declared corroboration threshold; insufficient support returns a classified failure. Median/local methods remain Selected. Generic read-only Call uses the registered method contract and rejects unknown methods locally. It does not provide an escape around validation or subscription setup.

With the default anchor strategy, moving latest/safe/finalized reads establish the requested level's per-call provider-backed reference. The explicit WithFinalizedAnchoring preset instead uses finalized selection for default/latest reads; safe retains its own level. Explicit pins remain explicit. Failed reference establishment sends no business request, and failures never shrink the frozen voting basis. Pending-local observations do not become Corroborated merely because several sources agree.

Complete block reads validate request identity and applicable header/body commitments, including transaction and withdrawals roots and uncle commitment. Header-only reads do not prove complete body contents. These checks establish the stated content relationships, not canonical-chain membership or finality.

Enable raw source retention and an optional completion capture per invocation:

```go
base, err := ethguard.WithSourceEvidence(ctx, 64<<10)
if err != nil { return err }
callCtx, capture := ethguard.WithResult(base)
block, result, err := client.BlockByNumber(callCtx, nil)
_ = block
_ = result
saved, published, captureErr := capture.Result()
```

Inspect saved only when published and captureErr is nil. The direct result remains the business return channel; capture errors describe delivery. A capture is single-use. Raw retention has byte/record/text limits and truncation flags; exhausting its budget does not change requests, votes or thresholds. Protected fields cannot be demoted to optional raw retention simply because a private algorithm type cannot express them. See the [result contract](docs/design/return-value-design.md).

## Writes, subscriptions and lifetime

Writes validate frozen transaction bytes and bind reported hashes to the local submitted hash. WriteDecision records acceptance separately from its reason and each source's exchange, answer and method judgment. Insufficient support, differing hashes, protocol rejection and a foreign hash remain distinguishable. Unconfirmed outcomes are not automatically replayed.

Synchronous writes broadcast once, then poll for a receipt. CallResult.ReceiptWait keeps the last actual query and the latest formed receipt observation, each with its own sequence, Decision and error. An interrupted final query preserves an earlier null or conflict observation; the operation's Err describes final termination. These facts do not require raw source-evidence capture and retain at most two queries. EthGuard does not sign, choose gas, allocate nonces or manage replacements.

Root and view provide SubscribeNewHead(ctx, policy), returning a HeadSubscription with Next, Basis and Close. Each event carries a complete continuous path change, including retractions for a reorg, frozen path witnesses and audit records. Fixed WSS members supply majority support; missing objects are recovered by hash. Memory, notification queues and request concurrency have explicit limits. A multi-source client with only one WSS source is rejected; an explicit single-source client remains Forwarded.

SubscribeFilterLogs(ctx, query, policy) returns a LogsSubscription with Next/Basis/CommittedTip/Close. Its complete batches follow the Head birth path, include zero-match blocks and full block retractions, and prove matching log contents using transaction and receipt commitments. Numeric/hash filters select the live path without historical replay. The Logs implementation passed local acceptance on November 24, 2026.

SubscribeTransactionReceipts(ctx, query, policy) uses ReceiptsSubscriptionPolicy and returns a ReceiptsSubscription with the same delivery operations. A nil or empty query selects all live receipts from the Head birth block. Complete transaction and receipt commitments prove coverage; a fixed quorum compares the delivered official fields, including declarations not covered by receipt roots. BlockSupplier and ReceiptSupport record these different forms of evidence. No chain configuration or independent execution proof is implied. The receipts stream passed local acceptance on November 25, 2026. See the [subscription contract](docs/design/subscription-contract.md) for policy fields, audit export and guarantee limits.

Raw WSS supports request ID demux and bounded notifications. The adapter does not automatically reconnect/replay; root subscription workers may rebuild individual sessions. There is no general keepalive or unlimited recovery promise. HTTP/WSS error.data preservation, synthetic tests and real-provider observations are separate evidence tracks.

Close on either facade stops the shared Client. New operations fail before network contact; in-flight public operations converge with the owner lifecycle. Client.Close triggers asynchronous subscription cleanup. Call HeadSubscription.Close, LogsSubscription.Close or ReceiptsSubscription.Close to wait for that stream's tasks and owner watcher to exit. Explicit stream close or Next cancellation discards unread events; producer failure drains committed events before returning a terminal error with remaining audit. Slow cleanup can continue after the terminal becomes visible; the stream's Close still waits for it.

## Code and documentation

| Module | Responsibility |
| --- | --- |
| root Client / EthClient | Public methods grouped by business, CallResult, lifecycle, diagnostics and one shared execution view |
| internal/runtime | The sole production composition root |
| rpc | Public request, response, decision and evidence contracts; pure value helpers |
| internal/rpcengine | Resolvers, read plans, canonical processing and adjudication |
| internal/methods / internal/projection | Method semantics and plans / official-field projection assembly |
| internal/aggregator / internal/pool | Invocation orchestration and evidence / fixed members, collection and health |
| internal/reference | Head selection algorithms behind injected read and decode capabilities |
| ethapi / internal/ethcodec | Generated business models / shared numeric and JSON primitives |
| internal/ethobject | One shared tree for object integrity, canonical representation and request binding |
| internal/transport / internal/subscription | Native adapters / stream aggregation and sessions |
| internal/rpcwire | Shared JSON-RPC envelopes, request IDs and protocol errors |
| internal/typed | The sole production typed projection |
| internal/uncertain | Bounded source material retention and capture budgets |
| internal/diag/{livecanary,shapeprobe,rpcsample,chainlist} | Diagnostic consumers, capture/replay tools and their fixtures |
| internal/verification / internal/testkit / rpctest | Public-seam tests and local peers |
| internal/spec / internal/executionapi / internal/wiregen | Pinned sources, effective schemas and offline generation |
| config / observe | Public configuration data / passive events and health snapshots |
| cmd / examples | Explicit tools and runnable examples |

Root reads are organized in state_reads.go, block_reads.go and transaction_reads.go; contract_execution.go owns calls, gas, fees and access lists, while simulate.go keeps simulation together. read_helpers.go holds shared typed-read and input helpers. ethclient_view.go calls the same private implementations; block and header projections retain their distinct contracts. Diagnostics consume the core through internal/diag; core production imports cannot depend on that tree.

Start at the [documentation index](docs/design/README.md). It links current contracts, pending choices and dated evidence. Historical decisions remain an archive, not a second implementation plan. The response model uses two fact domains, exchange and answer meaning, across transport, methods and public decisions. The roadmap records migration acceptance and the remaining independent work.

Ordinary tests are offline. Anvil and provider capture are explicit commands; synthetic/local-chain evidence does not close real-provider gaps. Live canary passed, workflow success and release approval are different facts. Formal gates and fixed tools are maintained only in the [release contract](docs/design/release-contract.md).
