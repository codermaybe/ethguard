# EthGuard

[**English**](README.md) · [简体中文](README.zh-CN.md)

> Status: early-stage implementation. The compatibility target is the complete
> `ethclient` method set for the go-ethereum version locked in `go.mod`. The
> current tree is still converging on that target and must not be advertised as
> a complete compatibility release.

EthGuard is a multi-source Ethereum JSON-RPC aggregation client. It queries
multiple upstreams, validates responses against pinned Ethereum execution-apis
contracts, normalizes official standard fields, and aggregates supported
extension fields. Official fields form the protected core result; feature and
vendor fields remain extension data under their method contract. Cross-source
agreement evaluates response reliability, and the client exposes the result
with status and diagnostic evidence fields.

EthGuard favors reliability over latency. For reads that need a moving
reference, it establishes one provider-backed reference before evaluating the
business response. The `ethguard.Client` typed methods keep the method names,
arguments, return types, and calling style of `go-ethereum/ethclient`, so
ordinary calls use the same shape without an explicit anchor/confirm/read
workflow. Cross-source agreement is provider evidence; it is not Ethereum
consensus, canonical proof, cryptographic finality, or light-client
verification. Multiple URLs need not be independent.

## Layout

```text
ethguard/                   # public Client facade
├── client.go               # public Client facade, New, Pool, Close (owner lifecycle)
├── call.go                 # generic read-only Call entry and decode delegates
├── eth.go                  # typed methods and their shared execution bodies
├── ethclient_view.go       # the ethclient-shaped view (private owner, exact surface)
├── call_result.go          # CallResult: decision, write decision, material, business error
├── result_capture.go       # WithResult capture state machine and WithSourceEvidence budget
├── execution.go            # the shared public-operation entry (bind, lifecycle, publish)
├── anchor.go               # anchoring strategies behind moving-tag reads
├── write.go                # write pipeline (relative-trust broadcast)
├── errors.go               # classified sentinel errors
├── uncertain.go            # UncertainResult material types (delivered on CallResult)
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
│   │   ├── facade/         # black-box root Client/EthClient verification
│   │   └── integration/    # cross-module integration verification
│   ├── transport/          # Transport Port adapters and contract tests
│   │   ├── http/            # project-owned native HTTP adapter
│   │   ├── wss/             # project-owned raw WebSocket call adapter
│   │   ├── geth/            # explicit go-ethereum compatibility adapter
│   │   └── contract/        # transport-neutral L1 contract suite
│   └── testkit/wstest/      # server-side WSS test peer
├── gethcompat/              # explicit construction seam for go-ethereum transport
├── rpctest/                # in-process scriptable Ethereum nodes for tests
├── cmd/                    # executable entries, including wsscapture and livecanary
├── docs/                   # architecture, threat model, and design notes
│   └── design/             # accepted architecture and aggregation records
└── examples/               # small import demos
```

| Package | Role |
|---------|------|
| `ethguard` | The public `Client`: Geth-shaped typed reads, generic calls, evidence, writes |
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
reference seams; the root `Client` is the only public surface over it.
`aggregator` consumes ReadPlans and resolver/admission
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
  lives in `internal/typed` behind the root facade's typed reads;
  strict requiredness stays fork-aware and hand-written in the slices.
- **R4 – inventory interlock.** Tests enforce that every registered method in
  `catalog.go` has a schema mapping in `spec_tables.go`.
- **Anchor layering.** The anchor facade is query-only: a `Client` owns its
  runtime-built resolver and exposes `AnchorBlockHash`/`AnchorBlockHashForTag`/
  `AnchorBlockHeight` from root `anchor.go`; the reference algorithm and
  Head-Reading Port live in `internal/reference`, with per-invocation evidence
  propagation in `internal/refcontext` (`Capture`).
- **Two facades, one execution, four evidence layers.** `ethguard.Client`
  owns every business method and returns a `CallResult` per operation: the
  business value rides the method's own return, the decision (or write
  decision) and collected material ride the result, and `CallResult.Err` is
  the call's final business conclusion. The ethclient-shaped `EthClient`
  view projects the same execution with official signatures. The four
  evidence channels stay separate: (1) typed classified errors plus the
  result's decision evidence, (2) generic read-only `Call` whose decision
  value carries the JSON, (3) opt-in `WithResult`/`WithSourceEvidence` raw
  source material with a per-call budget, (4) `observe.Recorder` events for
  audit and operations. The recorder is never a synchronous business return
  channel.

### Execution and coverage

The default runtime compiles one `MethodSpec` per registered read method into a
`rpc.ReadPlan`. The plan owns stage facts such as normalization, reference and
pending handling, admission, resolver selection, and projection binding. The
execution skeleton is:

```text
Validate / Prepare → Reference → Collect → Admit → Resolve → Project
```

The current inventory contains 26 registered read methods. The root `Client`
provides the Geth-shaped typed methods and generic `Call` access over that
inventory. `eth_getProof` is explicitly unsupported. A registered plan does not
promise a provider-wide compatibility result: live provider evidence remains a
separate track.

The old per-method Policy constructors remain as a resolver seam, compatibility
surface, and equivalence-test oracle. They are not the default runtime registry
and must not become a second composition root. Method-level orchestration is
plan-driven; resolver modules such as strict, median, local, call-revert, and
block-specific policies remain deliberately deep modules.

## Usage

The supported way in: two or more distinct endpoints, default configuration:

```go
client, err := ethguard.Dial("https://your-provider-a", "https://your-provider-b")
if err != nil { return err }
defer client.Close()

balance, result, err := client.BalanceAt(ctx, account, nil)
```

Every operation returns its business value plus a `CallResult` — the decision
evidence (or write decision), the collected material when enabled, and `Err`,
the call's final business conclusion (identical to the trailing error):

Exactly one endpoint must opt into Forwarded mode explicitly — `Dial` refuses
a single URL, and a repeated URL fails (a duplicate shrinks the voting basis
without adding a source):

```go
client, err := ethguard.DialUncorroborated("https://your-provider")
```

Operators who configure EthGuard in YAML load the file directly:

```go
client, err := ethguard.LoadFile("config.yaml")
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

Advanced control (anchoring presets, tip tracking, collect window) builds the
config programmatically and constructs with `New`:

```go
cfg, err := config.FromEndpoints("https://a.example:8545", "https://b.example:8545")
if err != nil { return err }
client, err := ethguard.New(cfg, ethguard.WithFinalizedAnchoring())
```

`Dial` never sets a chain identity implicitly — pin one with
`ethguard.WithChainID(chainID)` (the Go option wins over `chain_id` in the
file). Construction is lazy: no endpoint is contacted until the first call,
and `DialContext`'s context bounds construction only.

For a runnable example, save that file and run:

```bash
go run ./examples/basic -config ./config.yaml
```

`ethguard.Client` is the evidence-rich root interface, not an optional demo.
Generic `Call`, the full decision evidence and pool inspection ride the same
client. The external compatibility target is the complete `ethclient` method
set for the go-ethereum version locked in `go.mod`; the current tree remains
an implementation checkpoint until that matrix is closed.

Deterministic typed reads require the library's dynamically computed
corroboration threshold: insufficient support returns `ErrConflict`, since
these methods cannot expose `Selected` to their callers. Advanced root `Call`
still returns the selected status where the method contract allows it.
Median/local methods retain their selection semantics. `Call` rejects unknown
methods locally with `ErrInvalidRequest`.

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

Result delivery and raw source evidence are opt-in and belong to one
invocation. `WithResult` puts a fresh capture on the context — the bound call
publishes its completion result there exactly once — and `WithSourceEvidence`
configures the retention budget for the raw upstream material of each call
made on the derived context:

```go
base, err := ethguard.WithSourceEvidence(ctx, 64<<10) // total bytes one call may retain
if err != nil { return err }                          // non-positive budget fails locally
callCtx, capture := ethguard.WithResult(base)
block, _, err := client.BlockByNumber(callCtx, nil)
_ = block // the normal result and error keep their usual semantics
result, published, err := capture.Result()
if published {
	for _, field := range result.Uncertain.FieldsFor("vendorField") {
		log.Printf("%s %s: %s", field.Source.ID, field.Path, field.Value)
	}
	for _, response := range result.Uncertain.Raw {
		log.Printf("%s %s: %s", response.Source.ID, response.Method, response.Error)
	}
	if result.Uncertain.Truncated {
		log.Printf("material truncated: retained=%d dropped=%d", result.Uncertain.Bytes, result.Uncertain.Dropped)
	}
}
```

`result.Err` equals the trailing error; `result.Decision` carries the
aggregation facts the call actually formed. A failed aggregation clears the
public value while the source tables stay; a corroborated receipt-null or a
corroborated revert keeps its payload with the classified business error.
Retention bounds are fixed for this release: the parameter-configured total
byte budget, at most 64 KiB per raw response, at most 256 records across raw
and fields, and at most 4 KiB per variable metadata text. A spent budget stops
retention only — never a request, a quorum, or a vote — and the transport
response limits stay independent. A capture handle that enters a second call
is permanently invalidated (`ErrCaptureReused`); a call that exits through a
panic marks the capture aborted (`ErrCaptureAborted`). Capture errors are
delivery facts and never enter the business error.

The ethclient-shaped view rides the same execution: `client.EthClient()`
exposes the official read signatures, `SendTransaction`,
`SendTransactionSync`, `SendRawTransactionSync`, the three subscription
methods, and `Close`. It satisfies `bind/v2.ContractCaller` and
`bind/v2.DeployBackend`; it is not `*ethclient.Client`. A view call with no
capture bound omits raw-material collection (nothing would consume it);
business behavior never changes. One `Close` on either surface stops both:
in-flight operations converge with a recognizable `ErrPoolClosed` cause, and
new operations fail before any network contact.

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

The raw WSS adapter is an EthGuard call transport with an opt-in bounded
notification capability, not a general WebSocket client. Root subscriptions
perform per-source resubscription with generation checks; transport-level
keepalive and general queue/replay policy remain deferred and require separate
operational evidence.
Early notifications are buffered by server subscription ID until binding.
The buffer retains at most 256 never-bound IDs and 256 notifications per ID;
at the ID cap, a new ID retires the oldest never-bound buffer rather than
being silently excluded forever. Binding drains only that ID's buffer;
closing or aborting the transport clears pending buffers and eviction order.

See the [architecture hub](docs/architecture.md),
[codebase topology](docs/design/codebase-topology.md), the
[go-ethereum compatibility policy](docs/design/go-ethereum-compatibility-policy.md),
and [threat model](docs/threat-model.md) for the security properties and
method boundaries.

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
