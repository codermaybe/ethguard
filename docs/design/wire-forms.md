# Wire-Form Compatibility

Why this exists: the first live soak run (2026-09-05) caught public
endpoints rejecting a 32-byte hash in eth_getBlockByNumber's number
position (`invalid argument 0: hex number > 64 bits`). Parameter shape ×
provider family is an independent divergence axis from response shape, and
it bites before any corroboration happens — a request a source refuses is
a fault, not a dissent. This file records, per method, which request forms
exist, which are exercised by tests, which were accepted by one historical
local fleet, and which remain assumptions.

T10 status (2026-10-23): the entries below are historical local soak
observations and unauthenticated shape captures. They are not external live
evidence, attested artifacts, or compatibility guarantees. The T10 live canary
added a fixed plan, machine report, and artifact verification, but its first
run was local and `inconclusive` because of provider availability faults. No
externally published canary has re-validated any row. Offline regression
(`go test`, `rpctest`, replay) remains the only CI-backed evidence class.

## Matrix

Legend: ✅ accepted by the healthy subset of one historical local soak fleet ·
⚠ caught failing in that local sample · · assumed, unverified.

Evidence rules (T02, binding on every row below):

- A row without a real provider sample is marked `assumed` or `unverified`.
  Local `rpctest` exact-shape tests prove what EthGuard puts on the wire;
  they never prove a provider accepts it, and must not be recorded as
  provider compatibility.
- One soak round proves only that the healthy subset of that fleet accepted
  the form on that day. It is request-shape acceptance, not a compatibility
  guarantee, and it does not cover provider families beyond the endpoints
  in that fleet.
- Provider acceptance of a non-standard form does not make it standard
  compatible. The bare-hash anchor rewrite for state reads is in this
  class: accepted by the soak fleet, but not the EIP-1898 spelling. Rows
  say so explicitly.
- Real samples, when needed, come from the existing `rpcsample` tooling
  (`internal/rpcsample`, `cmd/rpcsample`) with its redaction rules
  (endpoint labels, no credentials on disk). This phase introduces no
  credentials and does not require completing every live sample.

| Method | Param form | Status | Evidence |
| --- | --- | --- | --- |
| eth_getBlockByNumber | number / `latest` / `safe` / `finalized` tags | ✅ | anchor establishment ran in every soak round |
| eth_getBlockByNumber | 32-byte hash | ⚠ | 3 of 4 healthy providers reject; hash references must ride eth_getBlockByHash (`blockAt` dispatches on reference shape) |
| eth_getBlockByHash | hash + fullTx | ✅ | census ran in every soak round |
| eth_getBalance | address + block hash | ✅ | corroborated 3/0 in soak — provider-accepted, non-standard spelling (EIP-1898 prescribes the object form below); acceptance is not standards compliance |
| eth_getTransactionCount | address + block hash | ✅ | corroborated 3/0 in soak — same non-standard-spelling note as eth_getBalance |
| eth_getStorageAt | address + slot + block hash | ✅ | corroborated 3/0 in soak — same non-standard-spelling note as eth_getBalance |
| eth_getCode | address + block hash | ✅ | corroborated 3/0 in soak — same non-standard-spelling note as eth_getBalance |
| eth_getBalance / eth_getTransactionCount / eth_getStorageAt / eth_getCode / eth_call | address/call object + `{blockHash}` or `{blockNumber[, requireCanonical]}` object | · | assumed (T02): rpctest pins the exact wire shape (`TestStateRefEIP1898ObjectPreservedOnWire`, `TestStateRefObjectTagRewritesToHashObject`); no live provider sample yet |
| eth_getBalance / eth_getTransactionCount / eth_getStorageAt / eth_getCode / eth_call | contradictory `{blockHash, blockNumber}` object | · | local contract, not provider evidence: rejected with ErrInvalidRequest before any upstream contact (`TestStateRefContradictoryObjectRejectedLocally`) |
| eth_feeHistory | 32-byte hash in newestBlock | · | local contract, not provider evidence: rejected with ErrInvalidRequest before any upstream contact (`TestFeeHistoryRejectsHashBeforeUpstream`, `TestFeeHistoryRejectsHashInNewestBlock`); newestBlock is a BlockNumber, never a hash position |
| eth_getTransactionByHash | hash | · | step skipped in soak (sampling blocked by the 0016 presence split); request shape is the receipt-class standard |
| eth_getTransactionReceipt | hash | · | same note as above |
| eth_getBlockReceipts | hash | ✅ | corroborated 2/0, 100–340 receipts per round through typed decode + bloom checks |
| eth_gasPrice / eth_maxPriorityFeePerGas | none | ✅ | census, outliers landed in reject evidence |
| eth_getBlockReceipts | number | ✅ | 2026-09-05 soak round: number form returned 345 receipts at the real anchor height, matching the hash form's 345 exactly |
| eth_getBlockReceipts | `latest` / `safe` / `finalized` | ✅ | moving tags are prepared as the corroborated block hash; `pending` is rejected before any upstream call |
| net_version / net_peerCount / net_listening / eth_syncing | none | ✅ | census |
| eth_call | call object + block hash | ✅ | 2026-09-05 soak round: corroborated 2/0/1 at the pinned hash tag (`blockNrOrHash` is hash-portable per geth); call object rides geth's `input`-key spelling — the bare-hash pin is provider-accepted, not the EIP-1898 object spelling |
| eth_estimateGas | call object + block hash | ✅ | same round: selected lower median 0x5445 with the hash tag on the wire; the adapter always sends the tag explicitly so the pin applies |
| eth_getLogs | filter object, height-pinned bounds | ✅ | same round: corroborated 2/0/0 over 70 logs with from/to pinned to the corroborated height — the bounds are BlockNumbers and reject hashes, so the height spelling is the portable one |
| eth_feeHistory | count + height + percentiles | ✅ | same round: corroborated 3/0/0 with newest = the corroborated height (oldest 25911850, 3 base fees) and reward percentiles agreeing canonically — the rounding liveness gate passed on this fleet |

Caveat that keeps this honest: ✅ means the healthy subset of one historical
local soak fleet accepted the form on one day. It is not external live
evidence, attestation, or a compatibility guarantee. Provider families beyond
those endpoints are covered only insofar as the endpoints represented them —
the matrix records observations, not guarantees, and rows grow with every
soak run.

## Checklist for new methods

A method's row lands here before the method does:

1. Every parameter position that can carry a hash: which method does the
   wire ask for? A hash is only portable where the spec gives it a
   dedicated method or an explicitly hash-typed position.
2. Which form do anchored policies actually put on the wire? Moving tags
   are rewritten to hashes, so the hash form — not the tag form — is the
   one that must be portable for every anchored read.
3. One soak run exercising the form, or an rpctest asserting the exact
   method chosen (see `TestAdapterBlockByHashUsesDedicatedMethod` for the
   shape of that assertion).
