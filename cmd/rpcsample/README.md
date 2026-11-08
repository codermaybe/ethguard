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
