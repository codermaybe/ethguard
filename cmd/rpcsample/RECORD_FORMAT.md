# rpcsample record format

The collector writes one JSON record per `(request_id, source_id)` pair and a
single `index.json` manifest. All files are UTF-8 JSON with a trailing newline,
written with mode `0600`.

Each record uses `ethguard.rpc-sample-record/v1` and contains:

- `observed_at`: the UTC time immediately before the request is made;
- `network`: the plan's chain ID and fork label;
- `source`: the stable source ID and a scheme/host-only endpoint;
- `request_id`, `method`, and `request`: the exact JSON-RPC request after
  redaction;
- `response`: HTTP status, content type, response body, and `truncated`; and
- `errors`: zero or more classified `configuration`, `transport`, `http`,
  `malformed_json`, `protocol`, `jsonrpc`, or `truncated` entries.

`index.json` uses `ethguard.rpc-sample-index/v1`. Its `records` array points to
each record with request ID, method, source ID, observation time, relative file
name, SHA-256 of the exact record bytes, and error kinds. The collector writes
records in plan order (request order, then source order), so the index is stable
for a fixed plan and run order.

The plan is not itself copied into the output. Preserve the reviewed plan beside
the output bundle when sharing it, after checking that it contains no endpoint
or header credentials. The index and records are observations only: they do not
normalize responses, compare providers, or establish compatibility.

Before sharing a bundle, search every output file for the configured endpoint,
header values, URL query values, and any values listed in `redact_env`. The
collector redacts those values from request JSON, response body/content type,
endpoint, and all persisted error text. It also redacts long URL path segments.
This check remains required because credentials must never be placed in a plan,
and a provider may echo an unexpected secret in a response.
