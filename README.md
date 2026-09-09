# EthGuard

> Status: early-stage design and implementation

EthGuard is a multi-source Ethereum RPC client. Its primary entry point is
`ethguard/ethadapter`: supported methods keep the names, arguments, return
types and calling style of `ethclient`. Change construction and dependency
injection; ordinary business calls do not need an anchor/confirm/read workflow.

The library establishes a common reference internally, verifies method-specific
answers, and returns a value or a classified error. Agreement is evidence from
providers, not a consensus proof, and multiple URLs need not be independent.

## Layout

```text
ethguard/                   # public Client facade
├── client.go               # composition root: Client, New, Pool, Close
├── anchor.go               # anchoring strategies behind moving-tag reads
├── call.go                 # generic call pipeline (Call, CallInto[T])
├── eth.go                  # typed convenience methods
├── errors.go               # classified sentinel errors
├── config/                 # programmatic and YAML configuration
├── pool/                   # upstream lifecycle, health, tips, and eligibility
├── observe/                # passive observability seam
│   └── prometheus/         # optional metrics adapter (kept out of core)
├── aggregator/             # semantic quorum, conflicts, and evidence
├── rpc/                    # normalized JSON-RPC transport types
│   └── methods/            # one policy file per verified method + forwarding
├── ethadapter/             # ethclient-shaped surface over the verified core
├── rpctest/                # in-process scriptable Ethereum nodes for tests
├── docs/                   # architecture, threat model, and design notes
└── examples/               # small import demos
```

| Package | Role |
|---------|------|
| `ethguard/ethadapter` | Primary ethclient-shaped client for supported methods |
| `ethguard` | Advanced `Client`, composition root, generic calls and evidence |
| `ethguard/config` | One configuration model for Go construction and YAML loading |
| `ethguard/pool` | Shared upstream connections, collection, health, tips, eligibility |
| `ethguard/observe` | Observability events (`Recorder`); never a source of security decisions |
| `ethguard/aggregator` | Semantic quorum, conflicts, and evidence |
| `ethguard/rpc` | JSON-RPC request, response, transport, and error model |
| `ethguard/rpc/methods` | Method-specific normalization and resolution policies |
| `ethguard/rpctest` | Scriptable multi-source test harness over real HTTP |

Concurrency model: one shared `Client` / `pool.Pool` per process use-case;
sources keep long-lived connections. Callers should not dial per request or
clone pools per goroutine. Owners should close the Client when its lifetime ends.

## Usage

```go
client, err := ethadapter.New(config.Config{
    ChainID: 1,
    Upstreams: []config.Upstream{
        {ID: "a", Endpoint: "https://your-provider-a"},
        {ID: "b", Endpoint: "https://your-provider-b"},
    },
})
if err != nil { return err }
defer client.Close()

balance, err := client.BalanceAt(ctx, account, nil)
```

For a runnable example, supply your own configuration:

```bash
go run ./examples/basic -config /path/to/config.yaml
```

`ethadapter` is the main supported-method interface, not an optional demo.
`Unwrap()` exposes the advanced `ethguard.Client` for generic `Call`, decision
evidence and pool inspection. The adapter retains method-shaped compatibility
for the supported subset; it does not claim the complete `ethclient` interface.

Deterministic adapter reads require corroboration even when `quorum=1`:
insufficient support returns `ErrConflict`, since these methods cannot expose
`Selected` to their callers. Advanced root `Call` still returns `Selected` with
evidence. Median/local methods retain their selection semantics, and unknown
methods remain forwarded. `Wrap` applies the same safety checks as `New`.

With finalized anchoring, divergent provider heights are reconciled at one
common height. Existing matching heads are reused; otherwise one bounded
confirmation round fills in missing responses. Default agreement requires the
fresh first-round views to confirm that candidate; explicit quorum uses the
original first-round participation count. Confirmation failures never shrink
that count or silently lower the requirement. `Decision.Reference` retains the
reference's origin, height, hash, participants, support and faults alongside
business-answer evidence. Published evidence owns independent copies of its
mutable slices.

This can add a header lookup, and insufficient reference support now fails
before the business read. The library never treats knowing a historical block
as proof of finality: finalized coverage remains a provider-reported claim.
Reference establishment is library-owned and preserves the requested moving
tag in its evidence. The former custom resolver injection and cross-call
anchor cache settings are retired; configuring either now returns a migration
error. Provider-reported finalized coverage remains evidence, not independent
consensus finality.

Complete block reads (`eth_getBlockByNumber` and `eth_getBlockByHash` with
full transactions) validate request binding and protocol commitments before a
response can contribute a voting result: header hash, transaction trie root,
applicable withdrawals root, and the uncle commitment. Historical uncle
headers are fetched from the same source and retained in the typed block. This
protects content-to-header consistency; it does not prove canonical chain
membership or on-chain finality. Real provider and fork compatibility samples
remain a separate verification step.

Uncertain data is opt-in and belongs to one invocation. The ordinary method
signatures stay unchanged; pass a context created by `WithUncertain`, then read
the stable defensive snapshot after either success or failure:

```go
callCtx, uncertain := ethguard.WithUncertain(ctx)
block, err := client.BlockByNumber(callCtx, number)
_ = block // the normal result and error keep their usual semantics
snapshot := uncertain.Result()
for _, field := range uncertain.Fields("vendorField") {
	log.Printf("%s %s: %s", field.Source.ID, field.Path, field.Value)
}
for _, response := range uncertain.Responses() {
	log.Printf("%s %s: %s", response.Source.ID, response.Method, response.Error)
}
if snapshot.Truncated {
	log.Printf("uncertain data truncated: retained=%d dropped=%d", snapshot.Bytes, snapshot.Dropped)
}
```

Fields preserve each source's raw value, including equal values and explicit
`null`; malformed responses and transport/protocol errors are retained in the
response records when budget permits. The data is compatibility evidence, not
a reliability guarantee or a recent-client cache. Retention is bounded during
collection and cancellation may leave a partial snapshot. A fresh capture is
required for each logical call; returned values are defensive copies.

The library API and verification policies are still evolving. See the
[architecture](docs/architecture.md), [aggregation design](docs/design/rpc-aggregation.md),
the [proxyd comparison](docs/design/proxyd-comparison.md),
and [threat model](docs/threat-model.md) before relying on security properties.

The library currently provides: configuration loading; multi-upstream
lifecycle with lazy dialing and health-driven recovery; strict-consensus and
quorum verification with deterministic conflict evidence; block-anchored
reads (strict or conservative finalized selection); a freshness gate that
quarantines lagging providers; classified sentinel errors; and a passive
observability seam with an optional Prometheus adapter. Verification covers
the shipped method set; unverified methods are forwarded and labeled
`Forwarded`, never claimed as verified.

EthGuard may assess RPC observations such as gas estimates, fee data, and
transaction counts. It does not optimize gas, allocate nonces, sign
transactions, or manage transaction broadcast and replacement.
