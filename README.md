# EthGuard

> Status: early-stage design and implementation

EthGuard aims to be a trusted, security-focused wrapper around `ethclient`. It trades a small amount of network performance for relatively reliable data sources.

It is a library you can import into your project—not a standalone RPC proxy. Use it to reduce trust issues that come from network connectivity, such as response hijacking or weak verification.

It does this by aggregating responses from multiple RPC sources and applying several mechanisms to keep connections stable and results trustworthy.

## Layout

```text
ethguard/                   # public Client facade
├── client.go               # composition root: Client, New, Pool, Close
├── call.go                 # generic call pipeline (Call, CallInto[T])
├── eth.go                  # typed convenience methods
├── config/                 # programmatic and YAML configuration
├── pool/                   # upstream lifecycle, health, and eligibility
├── metrics/                # passive observability seam
├── aggregator/             # semantic quorum, conflicts, and evidence
├── rpc/                    # normalized JSON-RPC transport types
│   └── methods/            # one policy file per verified method + forwarding
├── docs/                   # architecture, threat model, and design notes
└── examples/               # small import demos
```

| Package | Role |
|---------|------|
| `ethguard` | Single entry `Client` for application code |
| `ethguard/config` | One configuration model for Go construction and YAML loading |
| `ethguard/pool` | Shared upstream connections, collection, health, and eligibility |
| `ethguard/metrics` | Observability events; never the source of security decisions |
| `ethguard/aggregator` | Semantic quorum, conflicts, and evidence |
| `ethguard/rpc` | JSON-RPC request, response, transport, and error model |
| `ethguard/rpc/methods` | Method-specific normalization and resolution policies |

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

The package currently provides configuration loading, multi-upstream lifecycle,
and bounded concurrent collection of raw RPC results. Semantic aggregation and
verification are design work in progress; the current code does not yet provide
a security guarantee.

EthGuard may assess RPC observations such as gas estimates, fee data, and
transaction counts. It does not optimize gas, allocate nonces, sign
transactions, or manage transaction broadcast and replacement.
