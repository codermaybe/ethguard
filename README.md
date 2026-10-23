# EthGuard

[**English**](README.md) · [简体中文](README.zh-CN.md)

> Status: early-stage implementation. The core aggregation model and main
> execution paths are in place, while method coverage, API boundaries, and
> implementation quality still need further hardening.

EthGuard is a multi-source Ethereum JSON-RPC aggregation client. It queries
multiple upstreams, validates responses against pinned Ethereum execution-apis
contracts, normalizes official standard fields, and aggregates supported
extension fields. Official fields form the protected core result; feature and
vendor fields remain extension data under their method contract. Cross-source
agreement evaluates response reliability, and the client exposes the result
with status and diagnostic evidence fields.

EthGuard favors reliability over latency. For reads that need a moving
reference, it establishes one provider-backed reference before evaluating the
business response. The `ethguard/ethadapter` surface keeps the method names,
arguments, return types, and calling style of `go-ethereum/ethclient`, so
ordinary calls use the same shape without an explicit anchor/confirm/read
workflow. Cross-source agreement is provider evidence; it is not Ethereum
consensus, canonical proof, cryptographic finality, or light-client
verification. Multiple URLs need not be independent.

## Layout

```text
ethguard/                   # public Client facade
├── client.go               # composition root: Client, New, Pool, Close
├── call.go                 # generic call pipeline + typed decode (Call, CallInto[T])
├── eth.go                  # typed convenience methods
├── anchor.go               # anchoring strategies behind moving-tag reads
├── write.go                # write pipeline (independent decision flow)
├── errors.go               # classified sentinel errors
├── uncertain.go            # opt-in per-invocation capture (incl. vendor-dialect evidence)
├── config/                 # programmatic and YAML configuration
├── pool/                   # upstream lifecycle, health, tips, and eligibility
├── observe/                # passive observability seam
│   └── prometheus/         # optional metrics adapter (kept out of core)
├── aggregator/             # orchestration only: Collect → Admit → Resolve
├── rpc/                    # public request, decision, policy, and evidence model
│   ├── projection/         # public standard-field projection and extension Carrier
│   ├── methods/            # concrete Ethereum JSON-RPC methods and catalog
│   │   ├── eth_*.go        # per-method constants, policies, and wiring
│   │   ├── block.go        # block method policy and method-specific admission
│   │   ├── logs.go         # log request/response binding and assembly
│   │   └── catalog.go      # method coverage inventory
│   └── internal/           # implementation packages used by rpc and methods
│       ├── wire/           # JSON normalization and spec-driven pruning
│       ├── objects/        # block, receipt, transaction, log, feeHistory rules
│       └── policy/         # shared admission decorator
├── internal/
│   ├── jsonsafe/           # bounded JSON validation (duplicate keys/limits)
│   ├── reference/          # internal finalized/reference establishment
│   ├── refcontext/         # per-invocation reference propagation
│   ├── uncertain/          # invocation-scoped raw evidence capture
│   ├── chainlist/           # chain-list data and loading
│   ├── rpcsample/           # sampling plan/recording support
│   └── spec/               # pinned standard reference data (not hand code)
│       ├── VERSION         # execution-apis release pin, single source
│       ├── methods.json    # trimmed per-method result contracts (~100KB)
│       ├── vectors/        # conformance vectors extracted from tests/*.io
│       └── README.md       # provenance, upgrade procedure, audit guide
├── ethadapter/             # ethclient-shaped surface over the corroborated core
├── rpctest/                # in-process scriptable Ethereum nodes for tests
├── cmd/                    # executable entries (soak, rpcsample, shapeprobe, specbundle, specdiff)
├── docs/                   # architecture, threat model, and design notes
│   └── design/             # accepted architecture and aggregation records
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
| `ethguard/rpc` | Public request, decision, policy, observation, and evidence model |
| `ethguard/rpc/projection` | Standard-field projection and extension Carrier assembly |
| `ethguard/rpc/methods` | Concrete Ethereum JSON-RPC methods, policies, and catalog |
| `ethguard/rpc/internal` | Internal wire, object, and admission implementations |
| `ethguard/internal/spec` | Pinned execution-apis reference data (version, schemas, vectors) |
| `ethguard/rpctest` | Scriptable multi-source test harness over real HTTP |

Concurrency model: one shared `Client` / `pool.Pool` per process use-case;
sources keep long-lived connections. Callers should not dial per request or
clone pools per goroutine. Owners should close the Client when its lifetime ends.

Layering rules: `aggregator` only knows the `rpc.Policy` / `AdmissionPolicy`
interfaces and never imports `methods`; `rpc` never imports `methods`
(corroborated: no import cycle, `go build ./...` passes). Ethereum method entrypoints and method-specific binding live in `rpc/methods`.
Shared wire normalization, pruning, object semantics, and admission helpers live
in `rpc/internal`; standard projection is exposed through `rpc/projection`.

- **R1 – slice admission.** Shapes used by one method only (feeHistory,
  syncing, accessList, call/estimateGas, quantity reads) stay in `eth_*.go`;
  slices exist only for objects shared by ≥2 methods or carrying cryptographic
  commitments.
- **R2 – table-level ownership.** The defining slice solely owns its field
  table; callers reuse it, never copy it (`logs.go` owns the `Log` table used
  by `receipt.go`). Implementation may split within the same package once a
  slice proves unwieldy — the table stays in exactly one file; no pre-splitting.
- **R3 – data, not generated code.** `internal/spec` holds the pinned
  execution-apis reference data (VERSION, trimmed `methods.json`, conformance
  vectors) and contains no hand-written logic beyond the embed accessor.
  Whitelist trees compile at init from the same bundle tests validate against —
  there is no generated Go and no freshness gap. Decode (`call.go`) and
  admission share the compiled masks; strict requiredness stays fork-aware and
  hand-written in the slices.
- **R4 – inventory interlock.** Tests enforce that every registered method in
  `catalog.go` has a schema mapping in `spec_tables.go`.
- **Anchor layering.** Public anchor surface (`ReferenceResolver`,
  `StrictHeadAnchor`, options) lives in the root `anchor.go`; mechanics live in
  `internal/reference` and `internal/refcontext`.
- **Two typed surfaces.** `eth.go` (root `Client`) returns values with
  `rpc.Decision` evidence for advanced callers; `ethadapter` returns
  ethclient-shaped values for drop-in use. The adapter translates shape only
  and must delegate to root capabilities — never a second decoder.

## Usage

```go
cfg, err := config.LoadFile("config.yaml")
if err != nil { return err }
client, err := ethadapter.New(cfg)
if err != nil { return err }
defer client.Close()

balance, err := client.BalanceAt(ctx, account, nil)
```

`config.yaml` can be a normal YAML configuration file:

```yaml
chain_id: 1
upstreams:
  - id: primary
    endpoint: https://your-provider-a
  - id: secondary
    endpoint: https://your-provider-b
```

For a runnable example, save that file and run:

```bash
go run ./examples/basic -config ./config.yaml
```

`ethadapter` is the main supported-method interface, not an optional demo.
`Unwrap()` exposes the advanced `ethguard.Client` for generic `Call`, decision
evidence and pool inspection. The adapter retains method-shaped compatibility
for the supported subset; it does not claim the complete `ethclient` interface.

Deterministic adapter reads require the library's dynamically computed
corroboration threshold: insufficient support returns `ErrConflict`, since
these methods cannot expose `Selected` to their callers. Advanced root `Call`
still returns the selected status where the method contract allows it.
Median/local methods retain their selection semantics, and unknown methods
remain forwarded. `Wrap` applies the same safety checks as `New`.

With finalized anchoring, divergent provider heights are reconciled at one
common height. Existing matching heads are reused; otherwise one bounded
confirmation round fills in missing responses. Default agreement requires the
fresh first-round views to confirm that candidate. The internally computed
support threshold keeps the original first-round participation count through
confirmation; failures never shrink that count or silently lower the
requirement. `Decision.Reference` retains the reference's origin, height, hash,
participants, support and faults alongside business-answer evidence. Published
evidence owns independent copies of its mutable slices.

This can add a header lookup, and insufficient reference support now fails
before the business read. Reference establishment is library-owned and
preserves the requested moving tag in its evidence. Finalized coverage remains
a provider-reported claim, not independent Ethereum consensus finality.

Complete block reads (`eth_getBlockByNumber` and `eth_getBlockByHash` with
full transactions) validate request binding and protocol commitments before a
response can contribute a voting result: header hash, transaction trie root,
applicable withdrawals root, and the uncle commitment. Historical uncle
headers are fetched from the same source and retained in the typed block. This
protects content-to-header consistency; it does not prove canonical chain
membership or on-chain finality. Real provider and fork compatibility samples
remain a separate corroboration step.

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
required for each logical call; returned values are defensive copies. HTTP
captures preserve the distinction between missing error data and JSON `null`;
the WSS path does not distinguish those two cases.

See the [architecture record](docs/design/architecture-record.md),
[architecture](docs/architecture.md), [aggregation overview (historical)](docs/design/rpc-aggregation.md),
the [proxyd comparison](docs/design/proxyd-comparison.md), and [threat model](docs/threat-model.md)
for the security properties and method boundaries.

Sources are treated as equal voting members. EthGuard does not model provider,
region, client-family, or weight-based independence, and a matching result is
provider-fault-tolerance evidence rather than a proof of Ethereum chain consensus,
canonical membership, or independent finality.

## Verification evidence

- **Offline regression** — `go test`, in-process `rpctest` fleets, and replay
  prove deterministic behavior under fixed inputs. They do not prove provider
  behavior, dialect coverage, or long-term availability.
- **Shape observations** — historical captures and `cmd/soak` probes are
  unauthenticated observations of one fleet on one day. They are not external
  live evidence, attestation, or a compatibility guarantee.
- **Live canary** — `cmd/livecanary` fixes a Chainlist snapshot, a voting basis,
  a request plan, and failure classes, and emits a machine report with digest
  binding. A `passed` result means only that this run satisfied the contract;
  `inconclusive` is not success. The first run (2026-10-23) was local and
  `inconclusive` because of availability faults; no externally published,
  attested live artifact exists yet.
- `latest` / `safe` / `finalized` are provider-reported tags, not Ethereum
  finality. `Corroborated` is provider-backed cross-source evidence, not
  consensus or a canonical-chain proof.

The library provides: configuration loading; multi-upstream
lifecycle with lazy dialing and health-driven recovery; dynamically computed
fixed-basis corroboration with deterministic conflict evidence; block-anchored
reads (strict or conservative finalized selection); a freshness gate that
quarantines lagging providers; classified sentinel errors; and a passive
observability seam with an optional Prometheus adapter. Corroboration covers
the shipped method set; uncorroborated methods are forwarded and labeled
`Forwarded`, never claimed as corroborated.

EthGuard may assess RPC observations such as gas estimates, fee data, and
transaction counts. It does not optimize gas, allocate nonces, sign
transactions, or manage transaction broadcast and replacement.
