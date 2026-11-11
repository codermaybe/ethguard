# rpcsample

`rpcsample` captures repeatable JSON-RPC observations for compatibility
investigation. It does not adjudicate responses and never reports provider
compatibility as passed.

Start from [`plan.example.json`](plan.example.json) and create a versioned plan.
Keep credentials out of the file; use `endpoint_env` and `header_env` instead.

```json
{
  "schema": "ethguard.rpc-sample-plan/v1",
  "network": {"chain_id": 1, "fork": "cancun"},
  "redact_env": ["RPC_API_TOKEN"],
  "sources": [
    {"id": "provider-a", "endpoint_env": "RPC_A_URL",
     "header_env": {"Authorization": "RPC_A_AUTH"}}
  ],
  "requests": [
    {"id": "head", "method": "eth_blockNumber", "params": []},
    {"id": "block", "method": "eth_getBlockByNumber",
     "params": ["latest", true]}
  ]
}
```

Run one request for every source and write an indexed record set:

```sh
RPC_A_URL=https://rpc.example RPC_A_AUTH='Bearer ...' \
  go run ./cmd/rpcsample -plan plan.json -out samples/2026-09-09
```

The output contains `index.json` and one `records/<request>--<source>.json`
file per observation. Records include UTC observation time, chain/fork, method,
the exact JSON-RPC request, HTTP status, response body, and classified errors.
The source is reduced to scheme and host. Endpoint credentials, configured
secret values, sensitive URL query values, and long path tokens are redacted
before any record is written. A response limit can be requested with
`-max-response-bytes`; the default limit is 32 MiB (`limits.MaxBodyBytes`).
Responses exceeding the limit are marked `truncated` and get a `truncated`
error.

The format intentionally preserves observations rather than normalizing block
or transaction content. Real provider, fork, and method coverage must be
reviewed separately; collecting a record is not a compatibility result.

The plan is expanded as a Cartesian product: every request is sent once to
every source, and the resulting file name is
`records/<request-id>--<source-id>.json`. `index.json` is the machine-readable
manifest; its SHA-256 is over the exact newline-terminated record bytes.
`observed_at` is UTC and is recorded independently for each request. A plan
should pin the chain and fork name used by the collection run and list the
method/tag cases needed by the review; it must not claim that a local mock is a
provider compatibility sample.

Redaction is applied before any record is serialized. It covers the complete
endpoint, URL credentials/sensitive query values and long path tokens, every
configured header value, and values named in `redact_env`. The redactor is also
applied to request JSON, response content type/body, and configuration, transport, HTTP,
and JSON-RPC error text. A response limit marks the record `truncated` and
skips JSON-RPC classification because the body is incomplete. Reviewers should
inspect records and run a credential search before sharing a sample bundle;
redaction is a defence-in-depth measure, not a reason to place secrets in a
plan.

Query names `apikey`, `api_key`, `key`, and `token` (case-insensitive) are
treated as credential-bearing, matching the livecanary classification. Their
values are redacted from request, response, and error text even when short,
such as `key=x7`. Other query values, including `chainId=1` and `x=1`, are not
automatically added to the redactor or credential scans, so ordinary query
parameters cannot corrupt numeric JSON-RPC IDs or `network.chain_id`. For
custom credential query names, explicitly list an environment variable
containing the secret value in `redact_env`.

Before writing, records are scanned for configured secrets and sensitive URL
tokens; the output directory is scanned again after publication. Query
credentials shorter than eight bytes are not global substring-scan inputs,
because they may also occur in unrelated metadata. Treat short credentials
as sensitive despite this scan limitation. Files use same-directory temporary
files with mode `0600`, sync and close before atomic rename, and remove
temporary files on failure.

## Frozen historical transaction scenario

This section records a local implementation checkpoint. Its command changes,
plan and fixtures are not included in the documentation-only commit; it is not
a guarantee that the committed CLI already exposes this scenario.

The reviewed `plan.transaction.json` uses the existing
v1 plan schema and mainnet Cancun block **20,000,000 (`0x1312d00`)**. Discovery
does not read local samples. Set both endpoints before running; source order is
the reviewed plan order, not an availability-based preselection:

```sh
export RPC_A_URL='https://ethereum-rpc.publicnode.com'
export RPC_B_URL='https://eth.llamarpc.com'
go run ./cmd/rpcsample -scenario transaction \
  -plan cmd/rpcsample/plan.transaction.json \
  -out /tmp/ethguard-transaction-evidence-20261111
```

The output directory must not exist, including an empty directory; the parent
directory must exist. Re-running requires a new output path. HTTP request
timeouts and response limits use the ordinary CLI flags. No compatibility
golden is generated and compatibility is always **unassessed**.

All sources receive discovery. The first successful valid JSON-RPC block at
the requested height containing a transaction hash selects transaction index
zero. The transaction hash, block hash, height and index are frozen before
capture. Every configured source, including failed discovery sources, then
receives chain identity, network identity and client-version requests, and
the frozen transaction-by-hash, transaction-by-block-hash-and-index, block and
receipt requests. Endpoints and header values are resolved once for the run.
When no target is found, discovery and identity observations are retained and
the command fails rather than manufacturing a transaction.

The bundle contains:

- `plan.json`: reviewed plan after the existing redaction policy;
- `batch.json`: frozen target and concrete capture requests;
- `index.json`, `records/*.json`: all original classified observations,
  including failures, with digests of persisted record bytes;
- `provenance.json`: tool/build identity, VCS revision when embedded by Go
  (otherwise explicitly unknown), pinned spec version/digest and timestamps;
- `report.json`: pinned-schema conformance errors, missing official keys,
  raw top-level official/external values and differences, identity and frozen
  reference findings. Membership comes from the shared pinned-schema projection
  API, not a scenario-specific official field list. The two transaction methods
  share a raw-comparison group. Differences are not equivalence decisions.

Records also include SHA-256 digests of exact request bytes and response bytes
read **before redaction**. For a truncated response the latter covers only the
retained prefix, not the unread original response. Raw persisted bodies remain
redacted; credentials are never deliberately stored. An interrupted run may
leave an incomplete reserved directory; inspect its index and batch rather
than retrying into it. Missing required fields, inconsistent identities and
provider failures are findings, never repaired or omitted from membership.

Narrow deterministic checks (no provider contact):

```sh
go test ./internal/rpcsample -run '^TestTransaction'
```

Provenance also hashes the actual running executable and persisted redacted
`plan.json`. The VCS revision and modified flag, when embedded, are descriptive
metadata rather than a complete source snapshot; the executable digest pins
the implementation that produced the run, including uncommitted changes.
