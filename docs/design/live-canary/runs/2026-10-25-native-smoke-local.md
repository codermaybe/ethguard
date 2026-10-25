# Native HTTP adapter local provider smoke diagnostic

Status: **local live observation only** (2026-10-25). It is not an external
live observation or attested evidence: the runner was the author's machine,
the artifact bundle was not published, and the native HTTP adapter is not yet
the default adapter. This record exists to answer one question early: do real
public providers conflict with the EthGuard native HTTP contract (strict
response ID/version validation, result/error mutual exclusion, degenerate
error rejection)?

## Tooling

- Code: this working tree (Slice 2 + review fix + adapter selection entry),
  native adapter selected explicitly through
  `internal/transport/http.NewDialer()`; the Geth compatibility adapter ran
  side by side from `internal/transport.StandardDialer{}`.
- Tool: `cmd/nativesmoke` — per provider and adapter, one long-lived
  transport, read-only calls only: `eth_chainId`, `eth_blockNumber`,
  `eth_getBlockByNumber("latest", false)`, and a deterministic `eth_call`
  revert probe (mainnet USDT `transfer` to a dead recipient; `eth_call`
  persists nothing).
- Run:
  - `go run ./cmd/nativesmoke -out report1.json` (default provider set)
  - `go run ./cmd/nativesmoke -out report2.json -providers "https://eth.meowrpc.com,https://rpc.mevblocker.io,https://rpc.ankr.com/eth"`

## Result summary (two runs, 2026-10-25)

| Provider | Verdict | Native envelope OK | Chain ID | Notes |
| --- | --- | --- | --- | --- |
| ethereum-rpc.publicnode.com | compatible | true | 0x1 | revert probe: code 3, data absent |
| eth.drpc.org | compatible | true | 0x1 | revert probe: code 3, data absent |
| eth-mainnet.public.blastapi.io | compatible | true | 0x1 | revert probe: code 3, data absent |
| 1rpc.io/eth | compatible | true | 0x1 | revert probe: code 3, `data:"0x"` present (three-state rule exercised on real wire) |
| cloudflare-eth.com | compatible (run 2) | true | 0x1 | run 1 throttled 429 on both adapters identically; run 2 fully clean |
| rpc.mevblocker.io | compatible | true | 0x1 | clean |
| eth.meowrpc.com | mixed | chainId true | 0x1 | transient 429s; run 2 saw geth throttled where native passed (timing, not contract) |
| eth.llamarpc.com | unreachable | false | — | Cloudflare 525 origin error on every call, both adapters identical |
| rpc.ankr.com/eth (no key) | **contract_conflict** | false | — | see finding below |

Report digests (files kept outside git, T10.6 convention):

- report1: `84291e8b5da1e08fda2400ca66bc658d4d51d1cfbff7ff56f9242fb8f3aa34f6`
- report2: `e7463db73fa04b73cc9374501cc5917594f425cc3092fec997fa7ae987a74078`

## Findings

1. **No ID rewriting, no missing `jsonrpc`, no degenerate errors, no
   non-canonical quantities on any healthy provider.** Six distinct providers
   passed the native adapter's strict validation end to end, including the
   response-ID match and `jsonrpc == "2.0"` checks that Geth does not perform.
2. **error.data three-state rule observed on real wire**: absent data on three
   providers' revert probes and a present-but-empty `"0x"` on 1rpc — both
   preserved exactly by the native adapter and classified identically to the
   Geth baseline.
3. **Ankr (unauthenticated) returns HTTP 200 with
   `{"jsonrpc":"2.0","error":{"code":-32000,...},"id":null}`** — raw body
   captured by curl. Geth decodes this as a `-32000` protocol error (no ID
   check); the native contract rejects it as `ErrIDMismatch`. This is the
   first real provider evidence touching strict ID validation and is an
   **adjudication item**, recorded verbatim in the smoke report
   (`divergent_failure` class); the contract is not loosened by this run.
   Note the member would be unavailable under either adapter; only the
   failure classification and health accounting differ.
4. **HTTP status behavior on real 4xx/5xx matches the frozen precedence**:
   Cloudflare 525 and 429 rate-limit bodies entered the error chain with
   identical classification on both adapters; pool-equivalent rate-limit
   signals are carried in the retained body text.
5. **Upstream quirk observed (not adapter-caused)**: 1rpc once delivered an
   HTTP-level `{"code":503,"message":"server unavailable"}` object as a 200
   *result* value. Both adapters consistently surfaced it as a success value;
   downstream corroboration, not the transport, is the right place to catch
   such content.
6. **Connection reuse and RTTs looked normal** on all compatible providers;
   per-call latencies were in the same band for both adapters.

## Limitations

- Six to nine providers attempted per run; two to three were availability-
  limited. This is a smoke sample, not a fleet observation.
- The ankr observation covers unauthenticated error bodies only; its success
  path (with a key) was not exercised.
- No write calls, no subscriptions, no authentication credentials involved.

## Next steps per the verification plan

- Phase 3 (external evidence): push the branch, dispatch `live-canary.yml`
  with `http_adapter=native`, verify and recheck the artifact bundle per
  `live-canary-contract.md`; `inconclusive` remains not-success.
- The ankr `id:null` case goes to the maintainer for a compatibility-mode
  ruling before any default-switch decision.
