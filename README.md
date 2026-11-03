# EthGuard

[**English**](README.md) · [简体中文](README.zh-CN.md)

> Status: early-stage implementation. The core aggregation model, the
> plan-driven read path, native HTTP, and the raw WSS call path are in place.
> Method-shaped compatibility remains a supported subset of `ethclient`, and
> provider evidence plus implementation quality still need further hardening.

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
├── client.go               # public Client facade, New, Pool, Close
├── call.go                 # generic call entrypoints and compatibility delegates
├── eth.go                  # typed convenience methods
├── anchor.go               # anchoring strategies behind moving-tag reads
├── write.go                # write pipeline (independent decision flow)
├── errors.go               # classified sentinel errors
├── uncertain.go            # opt-in per-invocation capture (incl. vendor-dialect evidence)
├── config/                 # programmatic and YAML configuration
├── pool/                   # upstream lifecycle, Transport Port, health, eligibility
├── observe/                # passive observability seam
│   └── prometheus/         # optional metrics adapter (kept out of core)
├── aggregator/             # Validate → Reference → Collect → Admit → Resolve → Project
├── rpc/                    # public request, decision, plan, policy, and evidence model
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
│   ├── runtime/            # sole Composition Root for plans, projectors, aggregator
│   ├── typed/              # sole production typed projection shared by facades
│   ├── jsonsafe/           # bounded JSON validation (duplicate keys/limits)
│   ├── reference/          # reference algorithm and Head-Reading Port
│   ├── refcontext/         # per-invocation reference propagation
│   ├── uncertain/          # invocation-scoped raw evidence capture
│   ├── chainlist/           # chain-list data and loading
│   ├── rpcsample/           # sampling plan/recording support
│   ├── spec/               # pinned standard reference data (not hand code)
│       ├── VERSION         # execution-apis release pin, single source
│       ├── methods.json    # trimmed per-method result contracts (~100KB)
│       ├── vectors/        # conformance vectors extracted from tests/*.io
│       └── README.md       # provenance, upgrade procedure, audit guide
│   ├── verification/       # cross-module verification through public seams
│   │   └── integration/    # first safe slice; later contracts/scenarios/replay split
│   ├── transport/          # Transport Port adapters and contract tests
│   │   ├── http/            # project-owned native HTTP adapter
│   │   ├── wss/             # project-owned raw WebSocket call adapter
│   │   ├── geth/            # explicit go-ethereum compatibility adapter
│   │   └── contract/        # transport-neutral L1 contract suite
│   └── testkit/wstest/      # server-side WSS test peer
├── ethadapter/             # ethclient-shaped surface delegating to the root facade
├── gethcompat/              # explicit construction seam for go-ethereum transport
├── rpctest/                # in-process scriptable Ethereum nodes for tests
├── cmd/                    # executable entries, including wsscapture and livecanary
├── docs/                   # architecture, threat model, and design notes
│   └── design/             # accepted architecture and aggregation records
└── examples/               # small import demos
```

| Package | Role |
|---------|------|
| `ethguard/ethadapter` | Primary ethclient-shaped client through the root facade |
| `ethguard` | Public `Client` facade, generic calls and evidence |
| `ethguard/config` | One configuration model for Go construction and YAML loading |
| `ethguard/pool` | Shared upstream connections, Transport Port, collection, health, eligibility |
| `ethguard/observe` | Observability events (`Recorder`); never a source of security decisions |
| `ethguard/aggregator` | Semantic quorum, conflicts, and evidence |
| `ethguard/rpc` | Public request, decision, ReadPlan, resolver, observation, and evidence model |
| `ethguard/rpc/projection` | Standard-field projection and extension Carrier assembly |
| `ethguard/rpc/methods` | Concrete Ethereum JSON-RPC methods, policies, and catalog |
| `ethguard/rpc/internal` | Internal wire, object, and admission implementations |
| `ethguard/internal/runtime` | Sole Composition Root binding methods, projectors, and aggregator |
| `ethguard/internal/typed` | Sole production typed decoder/projection |
| `ethguard/internal/reference` | Reference algorithm and Head-Reading Port |
| `ethguard/internal/verification` | Contract, scenario, integration, and replay verification through seams |
| `ethguard/internal/spec` | Pinned execution-apis reference data (version, schemas, vectors) |
| `ethguard/rpctest` | Scriptable multi-source test harness over real HTTP |
| `ethguard/internal/transport` | Transport Port implementations and neutral contract tests |
| `ethguard/gethcompat` | Explicit go-ethereum compatibility construction seam |

Concurrency model: one shared `Client` / `pool.Pool` per process use-case;
sources keep long-lived connections. Callers should not dial per request or
clone pools per goroutine. Owners should close the Client when its lifetime ends.

Layering rules: `internal/runtime` is the sole Composition Root. It compiles the
method inventory into ReadPlans and binds plans, projectors, the aggregator, and
reference seams; `ethadapter` delegates through the root facade and does not
import runtime. `aggregator` consumes ReadPlans and resolver/admission
interfaces and never imports `methods`; `rpc` never imports `methods`. Ethereum
method entrypoints and the MethodSpec inventory live in `rpc/methods`. `pool` owns the minimal Transport Port and
`internal/reference` owns the Head-Reading Port; neither carries method policy
assembly or business adjudication. Shared wire normalization, pruning, object
semantics, and admission helpers live in `rpc/internal`.

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
  there is no generated Go and no freshness gap. Production typed projection
  lives in `internal/typed` and is shared by the root facade and `ethadapter`;
  strict requiredness stays fork-aware and hand-written in the slices.
- **R4 – inventory interlock.** Tests enforce that every registered method in
  `catalog.go` has a schema mapping in `spec_tables.go`.
- **Anchor layering.** The public anchor facade (`ReferenceResolver`,
  `StrictHeadAnchor`, options) lives in root `anchor.go`; the reference
  algorithm and Head-Reading Port live in `internal/reference`, with
  per-invocation propagation in `internal/refcontext`.
- **Two typed surfaces.** `eth.go` (root `Client`) returns values with
  `rpc.Decision` evidence for advanced callers; `ethadapter` returns
  ethclient-shaped values for drop-in use. The adapter may perform
  method-compatibility validation and request binding, while typed decoding
  remains in the shared `internal/typed` module and business adjudication
  remains in the root runtime.

### Execution and coverage

The default runtime compiles one `MethodSpec` per registered read method into a
`rpc.ReadPlan`. The plan owns stage facts such as normalization, reference and
pending handling, admission, resolver selection, and projection binding. The
execution skeleton is:

```text
Validate / Prepare → Reference → Collect → Admit → Resolve → Project
```

The current inventory contains 23 registered read methods. The root `Client`
provides typed methods and generic `Call` access; `ethadapter` provides an
ethclient-shaped wrapper for the supported subset (currently 18 method-shaped
entries). `eth_getProof` is explicitly unsupported. A registered plan does not
promise a provider-wide compatibility result: live provider evidence remains a
separate track.

The old per-method Policy constructors remain as a resolver seam, compatibility
surface, and equivalence-test oracle. They are not the default runtime registry
and must not become a second composition root. Method-level orchestration is
plan-driven; resolver modules such as strict, median, local, call-revert, and
block-specific policies remain deliberately deep modules.

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
required for each logical call; returned values are defensive copies. Native HTTP
and raw WSS preserve missing error data versus JSON `null`; the explicit geth
compatibility WSS path still cannot distinguish them.

### HTTP and WSS sources

Transport selection is explicit per source. Omitted `transport` keeps native
HTTP; `wss` opts into the raw WebSocket adapter with a `ws://` or `wss://` endpoint:

```yaml
upstreams:
  - id: provider-a
    endpoint: https://rpc-a.example
  - id: provider-b
    endpoint: wss://rpc-b.example
    transport: wss
```

Both are ordinary fixed voting members and participate in reads and transaction
broadcasts. Unsupported methods remain protocol evidence, not global health
failures; they contribute no result support and do not lower the threshold.
Rate limits and attributable transport/server failures affect shared read/write
health. Unknown transaction outcomes are never automatically replayed; only a
later independent call may lazily reconnect. Raw WSS retains missing/null/`0x`/
concrete error data without claiming provider-wide compatibility. Same-backend
HTTP and WSS endpoints do not establish independent trust. Explicit per-source
transport cannot be combined with `DialerOption`/`gethcompat.New`.

The raw WSS adapter is an EthGuard call transport, not a general WebSocket
client. Subscriptions, background reconnect, active keepalive, and an internal
queue are outside the current scope and require separate operational evidence.

See the [architecture record](docs/design/architecture-record.md),
[architecture](docs/architecture.md), [aggregation overview (historical)](docs/design/rpc-aggregation.md),
[architecture recovery](docs/design/architecture-recovery.md), [codebase topology](docs/design/codebase-topology.md), the [proxyd comparison](docs/design/proxyd-comparison.md), and [threat model](docs/threat-model.md)
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
  binding. A `passed` state means only that this run satisfied the contract;
  `inconclusive` is not success. A local diagnostic, the first external
  observation (run `39983945116`), and a separate recheck workflow run of the
  same manifest (run `40078416681`, `repeatable=true`, differences classified
  as provider fluctuation) now exist. All of these observations were
  `inconclusive`; they do not establish compatibility, consensus, finality,
  provider independence, or long-term reliability.
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
