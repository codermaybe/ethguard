# EthGuard

> Status: early-stage design and implementation

EthGuard aims to be a trusted, security-focused wrapper around `ethclient`. It trades a small amount of network performance for relatively reliable data sources.

It is a library you can import into your project—not a standalone RPC proxy. Use it to reduce trust issues that come from network connectivity, such as response hijacking or weak verification.

It does this by aggregating responses from multiple RPC sources and applying several mechanisms to keep connections stable and results trustworthy.

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
| `ethguard` | Single entry `Client`; composition root and typed wrappers |
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

```bash
go run ./examples/basic
```

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
