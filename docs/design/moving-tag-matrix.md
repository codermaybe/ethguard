# Moving-Tag and Block-Reference Matrix

Status: T02 contract record, 2026-09-21; T03-A pending-log and BlockByNumber(-1) boundaries updated 2026-09-22. This file is the per-method
compatibility contract for block references. It answers, for every
`RegisteredMethod` in `rpc/methods/catalog.go`, three separate questions
that must not be conflated:

1. **Ethereum official request semantics** — what the Execution API
   (`eth_*` methods) and [EIP-1898](https://eips.ethereum.org/EIPS/eip-1898)
   accept for this method's block-reference position.
2. **EthGuard's per-call reference** — what EthGuard actually puts on the
   wire after its anchor decorators run (`EthGuard action` / `Wire form`).
3. **Provider-backed corroboration semantics** — what a returned
   `Corroborated` / `Selected` / `Forwarded` status proves (`Result status` /
   `Evidence meaning`).

`Corroborated` means provider-backed cross-source evidence under the
declared support rule. It is not Ethereum consensus, not a canonical-chain
proof, not cryptographic finality, and not light-client verification.
`finalized` / `safe` below are provider-reported tags resolved through a
tag-aware reference; agreement about them proves that the configured
sources reported consistently, nothing more.

Evidence status (2026-10-23): the `soak` and `rpcsample` entries below are
historical local observations and shape captures, not external live evidence,
attestation, or compatibility guarantees. The only CI-backed evidence class is
offline regression (`go test`, `rpctest`, replay). T10 added a local live
canary diagnostic (state `inconclusive` because of provider availability
faults); no externally published, attested live artifact exists yet.

## Reading the columns

- **Reference position**: zero-based `params` index of the block reference,
  `filter.fromBlock/toBlock` for `eth_getLogs`, or `none`.
- **earliest / number / hash / latest / safe / finalized / pending**:
  `yes` = officially accepted and passed through or pinned as stated in
  `EthGuard action`; `no` = officially inapplicable or locally rejected;
  notes qualify the wire shape.
- **requireCanonical**: whether the EIP-1898 `requireCanonical` flag is
  preserved on the wire where the official form allows it.
- **EthGuard action** (closed set): `corroborated reference rewrite` |
  `height pin` | `hash pin` | `passthrough` | `reject locally` |
  `single-source local observation` | `unsupported`.
- **Result status**: the decision ceiling for this method/reference shape.
- **Evidence meaning**: what the status proves — and explicitly what it
  does not prove.
- **Tests**: exact wire-form / negative tests pinning the row. Each entry is one complete `func Test*` name, comma-separated. Wildcards, `as above`, and prose aliases are not names.
- **Observation evidence**: `soak <date>` = accepted by the healthy subset of
  one historical local soak fleet on one day (request-shape acceptance only,
  never a compatibility guarantee); `assumed` / `unverified` = no real-provider
  sample yet, see `wire-forms.md`. Object-form captures from 2026-09-23 live in
  `internal/rpcsample/testdata/eip1898-object/2026-09-23`. Endpoints are
  redacted. Those records are replayed offline and are shape evidence only,
  not a compatibility guarantee. `eth_getLogs` pending remains a local
  `ErrInvalidRequest`; field-level pending observation is not implemented.

## A. Default-block state reads (T02 first vertical slice)

These five methods take a default block parameter (`latest` when omitted
upstream). Their reference position accepts the full EIP-1898
`BlockNumberOrHash`: hex quantity, tag string, or object
`{blockHash}` / `{blockNumber[, requireCanonical]}`.

| Method | Reference position | earliest | number | hash | latest | safe | finalized | pending | requireCanonical | EthGuard action | Wire form | Result status | Evidence meaning | Tests | Observation evidence |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| eth_getBalance | 1 | yes, passthrough (`earliest` or `0x0`; genesis is immutable, no pin needed) | yes, passthrough (quantity spelling preserved) | yes, two forms: bare hash string (provider-accepted, non-standard) or `{blockHash}` object (standard); both pass through | yes, corroborated reference rewrite (tag-aware; string rewrites to bare anchor hash) | yes, same as latest but anchored at the `safe` reference, never silently latest | yes, same as latest but anchored at the `finalized` reference, never silently latest | yes, single-source local observation (Selected at best; unanimous agreement never Corroborated) | preserved on object forms, including across tag→hash rewrites | corroborated reference rewrite + single-source local observation (pending) | `eth_getBalance [address, hash-or-object]`; moving-tag strings become the anchor hash, `{blockNumber: tag}` objects become `{blockHash: anchor[, requireCanonical]}` | Corroborated (mined) / Selected (pending) | Sources agreed on state at one provider-backed reference. Not a proof the reference is canonical or final. Pending reads resolve as single-source local observations (Selected at best). | TestEIP1898BlockRefSlice, TestPendingLocalSlice, TestPendingAgreementIsSelectedNotCorroborated, TestPendingViaAdapterSucceedsDespiteCorroborationGate, TestPendingDissentSelectsFirst, TestPendingAllFailIsUnknown, TestPendingObjectFormIsLocalObservation, TestStateRefMovingTagsRewriteToAnchorHash, TestStateRefSafeFinalizedKeepTheirLevel, TestStateRefEIP1898ObjectPreservedOnWire, TestStateRefObjectTagRewritesToHashObject, TestStateRefPendingPassesThroughUnrewritten, TestStateRefAnchorFailureSendsNoBusinessRequest, TestStateRefContradictoryObjectRejectedLocally, TestStateRefEarliestAndNumberPassThrough, TestReferenceAnchorFailureKeepsPrepareErrorAndSkipsBusinessFanout, TestReferenceEstablishedFreshPerCall, TestReferenceFailureEvidenceIsUnknownWithObservedAt , TestEIP1898ObjectBatchMatchesExpect | soak 2026-09-05 bare-hash form ✅ (3/0); object form assumed (new in T02) |
| eth_getTransactionCount | 1 | yes, passthrough | yes, passthrough | yes, bare or `{blockHash}` (as above) | yes, rewrite (tag-aware) | yes, anchored at `safe` | yes, anchored at `finalized` | yes, single-source local observation (Selected at best; unanimous agreement never Corroborated) | preserved | corroborated reference rewrite + single-source local observation (pending) | `eth_getTransactionCount [address, hash-or-object]` | Corroborated (mined) / Selected (pending) | As above; pending is a single-source local observation. | TestEIP1898BlockRefSlice, TestPendingLocalSlice, TestPendingAgreementIsSelectedNotCorroborated, TestPendingViaAdapterSucceedsDespiteCorroborationGate, TestPendingDissentSelectsFirst, TestPendingAllFailIsUnknown, TestPendingObjectFormIsLocalObservation, TestStateRefMovingTagsRewriteToAnchorHash, TestStateRefSafeFinalizedKeepTheirLevel, TestStateRefEIP1898ObjectPreservedOnWire, TestStateRefObjectTagRewritesToHashObject, TestStateRefPendingPassesThroughUnrewritten, TestStateRefAnchorFailureSendsNoBusinessRequest, TestStateRefContradictoryObjectRejectedLocally, TestStateRefEarliestAndNumberPassThrough, TestReferenceAnchorFailureKeepsPrepareErrorAndSkipsBusinessFanout, TestReferenceEstablishedFreshPerCall, TestReferenceFailureEvidenceIsUnknownWithObservedAt , TestEIP1898ObjectBatchMatchesExpect | soak 2026-09-05 bare-hash ✅ (3/0); object form assumed |
| eth_getStorageAt | 2 | yes, passthrough | yes, passthrough | yes, bare or `{blockHash}` (as above) | yes, rewrite (tag-aware) | yes, anchored at `safe` | yes, anchored at `finalized` | yes, single-source local observation (Selected at best; unanimous agreement never Corroborated) | preserved | corroborated reference rewrite + single-source local observation (pending) | `eth_getStorageAt [address, slot, hash-or-object]`; slot (pos 1) never rewritten | Corroborated (mined) / Selected (pending) | As above; pending is a single-source local observation. | TestEIP1898BlockRefSlice, TestPendingLocalSlice, TestPendingAgreementIsSelectedNotCorroborated, TestPendingViaAdapterSucceedsDespiteCorroborationGate, TestPendingDissentSelectsFirst, TestPendingAllFailIsUnknown, TestPendingObjectFormIsLocalObservation, TestStateRefMovingTagsRewriteToAnchorHash, TestStateRefSafeFinalizedKeepTheirLevel, TestStateRefEIP1898ObjectPreservedOnWire, TestStateRefObjectTagRewritesToHashObject, TestStateRefPendingPassesThroughUnrewritten, TestStateRefAnchorFailureSendsNoBusinessRequest, TestStateRefContradictoryObjectRejectedLocally, TestStateRefEarliestAndNumberPassThrough, TestReferenceAnchorFailureKeepsPrepareErrorAndSkipsBusinessFanout, TestReferenceEstablishedFreshPerCall, TestReferenceFailureEvidenceIsUnknownWithObservedAt , TestEIP1898ObjectBatchMatchesExpect | soak 2026-09-05 bare-hash ✅ (3/0); object form assumed |
| eth_getCode | 1 | yes, passthrough | yes, passthrough | yes, bare or `{blockHash}` (as above) | yes, rewrite (tag-aware) | yes, anchored at `safe` | yes, anchored at `finalized` | yes, single-source local observation (Selected at best; unanimous agreement never Corroborated) | preserved | corroborated reference rewrite + single-source local observation (pending) | `eth_getCode [address, hash-or-object]` | Corroborated (mined) / Selected (pending) | As above; pending is a single-source local observation. | TestEIP1898BlockRefSlice, TestPendingLocalSlice, TestPendingAgreementIsSelectedNotCorroborated, TestPendingViaAdapterSucceedsDespiteCorroborationGate, TestPendingDissentSelectsFirst, TestPendingAllFailIsUnknown, TestPendingObjectFormIsLocalObservation, TestStateRefMovingTagsRewriteToAnchorHash, TestStateRefSafeFinalizedKeepTheirLevel, TestStateRefEIP1898ObjectPreservedOnWire, TestStateRefObjectTagRewritesToHashObject, TestStateRefPendingPassesThroughUnrewritten, TestStateRefAnchorFailureSendsNoBusinessRequest, TestStateRefContradictoryObjectRejectedLocally, TestStateRefEarliestAndNumberPassThrough, TestReferenceAnchorFailureKeepsPrepareErrorAndSkipsBusinessFanout, TestReferenceEstablishedFreshPerCall, TestReferenceFailureEvidenceIsUnknownWithObservedAt , TestEIP1898ObjectBatchMatchesExpect | soak 2026-09-05 bare-hash ✅ (3/0); object form assumed |
| eth_call | 1 | yes, passthrough | yes, passthrough | yes, bare or `{blockHash}` (as above) | yes, rewrite (tag-aware) | yes, anchored at `safe` | yes, anchored at `finalized` | yes, single-source local observation (Selected at best; unanimous agreement never Corroborated) | preserved | corroborated reference rewrite + single-source local observation (pending) | `eth_call [callObject, hash-or-object]`; call object (pos 0) never rewritten; revert-class answers separate execution from data. Missing and JSON null abstain on data and are not empty bytes; empty (0x), non-empty hex, and non-hex JSON stay distinct data assertions. A short or unanimity-split data cluster keeps a corroborated revert and deletes data (decision 0039 rule 4e). Vendor code and message text are not the key (decisions 0017, 0023, and 0039) | Corroborated (mined) / Selected (pending) | Sources agreed on the data-aware execution key at one provider-backed reference. Message text and vendor code are not the key. Pending reads (results and revert observations) resolve as single-source local observations. | TestNormalizeCallOutcomeClustersResultsAndReverts, TestNormalizeCallOutcomeSeparatesRevertDataStates, TestNormalizeCallOutcomeSameWordingDifferentDataDisagrees, TestCallRevertDataConflictIsNotSwallowed, TestCallProviderErrorDoesNotEnterRevertKey, TestRevertSamplesUseDistinctDataKeys, TestRevertDataSamplesReplayWithoutEndpoints, TestRevertDataBatchMatchesExpect, TestCallRevertRule4eMatrix , TestEIP1898ObjectBatchMatchesExpect | internal/rpcsample/testdata/revert-data/2026-09-23: code 3, same message with missing data, 0x, and 0xdeadbeef; Error(string) data also present. Not a census. code -32000 and structured data were not in this capture. Prior soak 2026-09-05 bare-hash ✅ (2/0/1) |

Local rejections for all five (no upstream contact): `{blockHash, blockNumber}`
in one object; non-boolean `requireCanonical`; malformed `blockHash`;
`{blockNumber}` object with a moving tag mixed with an explicit hash
elsewhere is rewritten, never silently downgraded. String-form mixed moving
tags across positions are rejected by the shared anchor layer.

## B. Blocks, receipts, transactions (immutable content)

| Method | Reference position | earliest | number | hash | latest | safe | finalized | pending | requireCanonical | EthGuard action | Wire form | Result status | Evidence meaning | Tests | Observation evidence |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| eth_getBlockByNumber | 0 | yes, passthrough (`earliest` / `0x0`) | yes, passthrough | no: a 32-byte hash in the number position is rejected by providers (`hex number > 64 bits`); the adapter dispatches hash lookups to `eth_getBlockByHash` instead | `nil` and explicit `-2` pin via the latest anchor, then fetch by hash | explicit `-4` pins via the safe anchor, then fetches by hash (never downgraded to latest) | explicit `-3` pins via the finalized anchor, then fetches by hash (never downgraded to latest) | policy: single-source local observation for the pending tag (Selected at best; never rewritten into an anchor); adapter BlockByNumber(-1) rejects locally before any anchor or business request (pending block typed projection is not supported). That rejection is ErrInvalidRequest, not a provider failure and not a business Unknown adjudication | n/a (number position takes no object) | hash pin via the tag-aware anchor seam (`AnchorBlockHashForTag` + `blockAt`) | `eth_getBlockByNumber [quantity-or-tag, fullTx]` for explicit tags only at the policy level; the adapter never sends a moving tag on a business call | Corroborated (mined) / Selected (pending) | Head agreement over number and hash. `nil` and `-2`/`-3`/`-4` carry a pinned-reference claim at their own level. Body content beyond the head is outside the comparison. Full-body checks (uncle root, empty uncle list legal for an empty root) are admission, not a canonicity claim. Tests include TestEmptyBlockUnclesAreLegalAndForeignBlockIsRejected. | TestAdapterBlockByNumberNeverFetchesAMovingTag, TestAdapterBlockByHashUsesDedicatedMethod, TestAdapterBlockByNumberNegativeTagsAnchorFirst, TestAdapterBlockByNumberRejectsUnknownNegativesLocally, TestAdapterBlockByNumberTagAnchorFailureSendsNoBusinessRequest, TestAnchorBlockHashForTagKeepsTagLevels, TestPendingLocalExtendedMethods, TestPendingExtendedMethodsAreSelectedNotCorroborated, TestBlockByNumberRejectsPendingLocally, TestBlockByNumberPendingSendsNoBusinessRequest, TestBlockByNumberPreservesKnownNegativeTagMeaning , TestBlockReceiptTxBatchMatchesExpect | soak: anchor establishment every round ✅; hash-in-number-position ⚠ (3/4 reject) |
| eth_getBlockByHash | 0 (hash) + fullTx | n/a | n/a | yes, hash-addressed immutable content, no rewrite | n/a | n/a | n/a | n/a | n/a | passthrough | `eth_getBlockByHash [hash, fullTx]` | Corroborated | Sources hold identical content for the hash. `null` is a comparable answer (→ `ethereum.NotFound` at the typed boundary). | TestAdapterBlockByHashUsesDedicatedMethod, TestAdapterBlockByHashRejectsAnotherSelfConsistentBlock, TestTypedRealHeaderRejectsAnotherBlocksContent, TestEmptyBlockUnclesAreLegalAndForeignBlockIsRejected, TestNullAnswersStayDistinct , TestBlockReceiptTxBatchMatchesExpect | internal/rpcsample/testdata/block-receipt-tx/2026-09-23: header shapes, empty block, null block. Not a census |
| eth_getTransactionReceipt | 0 (tx hash) | n/a | n/a | yes, immutable content, no rewrite | n/a | n/a | n/a | n/a | n/a | passthrough | `eth_getTransactionReceipt [hash]` | Corroborated | As above; mixed null/object is conflict under default Strict. | TestAdapterTransactionReceiptReturnsCorroboratedReceipt, TestAdapterTransactionReceiptMapsCorroboratedAbsenceToNotFound, TestRealShapesAgreeAfterPruneAndCaseFold, TestReceiptFromAnotherTransactionIsRejected, TestTypedReceiptFromAnotherHashIsMalformed, TestTypedNullAndEmptyReceiptsStayDistinct , TestBlockReceiptTxBatchMatchesExpect | internal/rpcsample/testdata/block-receipt-tx/2026-09-23: one receipt, null receipt. Not a census |
| eth_getTransactionByHash | 0 (tx hash) | n/a | n/a | yes, immutable content, no rewrite | n/a | n/a | n/a | corroborated pending tx is an ordinary corroborated object (adapter reports `isPending` from wire context) | n/a | passthrough | `eth_getTransactionByHash [hash]` | Corroborated | As above. | TestAdapterTransactionByHashReportsPendingFlag, TestAdapterTransactionByHashRejectsAnotherTransaction, TestRealShapesAgreeAfterPruneAndCaseFold, TestMissingRequiredFieldsStayOutOfAdmission , TestBlockReceiptTxBatchMatchesExpect | internal/rpcsample/testdata/block-receipt-tx/2026-09-23: one transaction including blockTimestamp, null transaction. Not a census |
| eth_getBlockReceipts | 0 | yes, passthrough (`0x0`) | yes, passthrough | yes, bare hash passthrough | yes, corroborated reference rewrite (tag-aware) | yes, anchored at `safe` | yes, anchored at `finalized` | no: reject locally before anchor or provider contact (case-insensitive string, surrounding space, JSON string, and {blockNumber: pending}). ErrInvalidRequest, not a provider failure and not a business Unknown adjudication | preserved: `{blockNumber, requireCanonical}` / `{blockHash, requireCanonical}` objects pass through; `requireCanonical` with a moving tag is rejected, not silently dropped | corroborated reference rewrite (+ reject locally for pending) | `eth_getBlockReceipts [hash-or-number-or-tag]`; moving tags become the anchor hash | Corroborated | Sources agreed on the full receipts array at one provider-backed reference. Typed boundary binds every receipt to the requested hash/height/pin. | TestAdapterBlockReceiptsAcceptsNumberTagAndHash, TestAdapterBlockReceiptsRejectsAnotherBlockForHashRequest, TestAdapterBlockReceiptsNumberFormBindsRequestedHeight, TestAdapterBlockReceiptsNumberFormRejectsTruncatedHeight, TestAdapterBlockReceiptsNumberFormBindsEveryReceipt, TestAdapterBlockReceiptsNumberFormRejectsMissingHeight, TestAdapterBlockReceiptsTagFormBindsToThePinnedReference, TestAdapterBlockReceiptsNumberFormAcceptsEmptyBlock, TestRejectPendingObjectFailsBeforeAnchor, TestBlockReceiptsPendingObjectSendsNoUpstreamRequest, TestReceiptFromAnotherTransactionIsRejected, TestTypedNullAndEmptyReceiptsStayDistinct , TestBlockReceiptTxBatchMatchesExpect | internal/rpcsample/testdata/block-receipt-tx/2026-09-23: empty receipt array and nulls. Prior soak 2026-09-05 ✅. Not a census |

## C. Execution, ranges, fee history

| Method | Reference position | earliest | number | hash | latest | safe | finalized | pending | requireCanonical | EthGuard action | Wire form | Result status | Evidence meaning | Tests | Observation evidence |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| eth_estimateGas | 1 | yes, passthrough | yes, passthrough | yes, bare hash or `{blockHash}` object, both passthrough | yes, string rewrites to the bare anchor hash; `{blockNumber: latest}` rewrites to `{blockHash: anchor[, requireCanonical]}` | yes, anchored at `safe` | yes, anchored at `finalized` | yes, string or `{blockNumber: pending}` passthrough; `PendingLocal`, first healthy source, Selected ceiling (never Corroborated). Non-pending stays the anchored lower median | preserved on object forms, including across tag→hash rewrites. The adapter still sends a string tag. A 2026-09-23 chainlist sample both accepted and rejected `requireCanonical`; that split is not a reason to drop the flag | corroborated reference rewrite; malformed objects are local `ErrInvalidRequest` before anchor | `eth_estimateGas [callObject, hash-or-object]` | Selected (at best) | Anchored estimates are the lower median at one provider-backed reference. Agreement is coincidence, never corroboration. Pending support is the first healthy source, not the agreeing fleet. | TestAdapterEstimateGasPinsTagAndTakesMedian, TestEstimateGasObjectForms, TestEstimateAndAccessListObjectsOnTheWire , TestEIP1898ObjectBatchMatchesExpect | soak 2026-09-05 string hash ✅; object sample 2026-09-23 (`internal/rpcsample/testdata/eip1898-object/2026-09-23`) shape evidence only |
| eth_createAccessList | 1 (optional) | yes, passthrough | yes, passthrough | yes, bare hash or `{blockHash}` object, both passthrough | yes, string rewrites to the bare anchor hash; object moving tags rewrite to `{blockHash: anchor[, requireCanonical]}` | yes, anchored at `safe` | yes, anchored at `finalized` | yes, string or `{blockNumber: pending}` is a single-source local observation (Selected at best; unanimous agreement never Corroborated) | preserved on object forms. No adapter channel. Sample 2026-09-23 shows providers disagree on `requireCanonical`; the flag is still preserved | corroborated reference rewrite + single-source local observation (pending); malformed objects are local `ErrInvalidRequest` before anchor | `eth_createAccessList [callObject, hash-or-object?]` | Corroborated (mined) / Selected (pending) | Sources agreed on the access-list payload at one provider-backed reference. Pending resolves as a single-source local observation. | TestCreateAccessListPolicyAnchoringAndNormalization, TestPendingLocalExtendedMethods, TestCreateAccessListObjectForms, TestEstimateAndAccessListObjectsOnTheWire , TestEIP1898ObjectBatchMatchesExpect | object sample 2026-09-23 (`internal/rpcsample/testdata/eip1898-object/2026-09-23`) shape evidence only, not a compatibility verdict |
| eth_getLogs | filter.fromBlock/toBlock; blockHash pin | yes (`0x0` passthrough) | yes, passthrough | fromBlock/toBlock: no (a hash dies with `hex number > 64 bits`); blockHash: yes, dedicated mutually-exclusive pin, passthrough | yes, height pin (tag-aware) + absent bounds defaulted to the pin (an omitted bound is a moving tag in field clothing) | yes, height pin at `safe` | yes, height pin at `finalized` | no: reject locally before anchor or provider contact (pending range has no stable corroboration semantics). Field-level pending observation is later T03 work and is not implemented. This rejection is ErrInvalidRequest, not a provider failure and not a business Unknown adjudication | n/a | height pin | `eth_getLogs [{fromBlock: height, toBlock: height, …}]` or `[{blockHash}]`; `blockHash` mixed with range fields is rejected locally | Corroborated | Sources agreed on the full log array over a pinned height range. Pruned-node gaps fail closed as dissent. | TestAdapterFilterLogsPinsTheFilterRange, TestAnchoredHeightFieldsRewriteFilterBounds, TestLogsPolicyRejectsMixedBlockSelectors, TestLogsHistoricalRangePreparesWithoutPendingRejection, TestLogsRejectPendingRangeLocally, TestLogsPendingDoesNotConsultAnchor, TestLogsRejectAbnormalPendingBoundsLocally, TestLogsPendingRangeSendsNoProviderRequest , TestRegisteredMethodSampleStatus | soak 2026-09-05 ✅ (70 logs, 2/0/0) |
| eth_feeHistory | 1 (newestBlock: BlockNumber, not BlockNumberOrHash) | yes, passthrough | yes, passthrough | no: reject locally (a 32-byte value exceeds uint64 and fails request validation; a hash must never ride a BlockNumber position) | yes, height pin (tag-aware) | yes, height pin at `safe` | yes, height pin at `finalized` | yes, single-source local observation (Selected at best; unanimous agreement never Corroborated) | n/a | height pin + single-source local observation (pending) | `eth_feeHistory [count, height, percentiles]` | Corroborated (mined) / Selected (pending) | Sources agreed on the pinned range. Reward-percentile rounding is the known liveness gate (fails closed as conflict until measured). Pending resolves as a single-source local observation. | TestFeeHistoryRequestValidatesTypedAndRawInputs, TestFeeHistoryAdmissionRejectsMalformedResponse, TestFeeHistoryAdmissionRequiresRequestedBlockCount, TestFeeHistoryRejectsHashInNewestBlock, TestPendingLocalExtendedMethods, TestPendingExtendedMethodsAreSelectedNotCorroborated, TestFeeHistoryPinsNewestToTheCorroboratedHeight , TestRegisteredMethodSampleStatus | soak 2026-09-05 ✅ (3/0/0) |

## D. No block reference (identity, variables, local observations)

| Method | Reference position | earliest | number | hash | latest | safe | finalized | pending | requireCanonical | EthGuard action | Wire form | Result status | Evidence meaning | Tests | Observation evidence |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| eth_chainId | none | n/a | n/a | n/a | n/a | n/a | n/a | n/a | n/a | passthrough | `eth_chainId []` | Corroborated | Same chain identity across sources. | TestAdapterChainIDIsCorroborated , TestRegisteredMethodSampleStatus | soak census ✅ |
| net_version | none | n/a | n/a | n/a | n/a | n/a | n/a | n/a | n/a | passthrough | `net_version []` | Corroborated | Same legacy decimal identity. Not cross-checked against `eth_chainId`. | TestNormalizeDecimalStringComparesNumerically, TestClientCorroboratesNetVersionAcrossSpellings , TestRegisteredMethodSampleStatus | soak census ✅ |
| eth_gasPrice | none | n/a | n/a | n/a | n/a | n/a | n/a | n/a | n/a | passthrough | `eth_gasPrice []` | Selected (at best) | Lower-median variable result with spread evidence. | TestAdapterSuggestGasPriceTakesMedian , TestRegisteredMethodSampleStatus | soak census ✅ |
| eth_maxPriorityFeePerGas | none | n/a | n/a | n/a | n/a | n/a | n/a | n/a | n/a | passthrough | `eth_maxPriorityFeePerGas []` | Selected (at best) | As above. | TestAdapterSuggestGasTipCapTakesMedian , TestRegisteredMethodSampleStatus | soak census ✅ |
| eth_blobBaseFee | none | n/a | n/a | n/a | n/a | n/a | n/a | n/a | n/a | passthrough | `eth_blobBaseFee []` | Selected (at best) | As above. | TestBlockNumberAndBlobBaseFeeLowerMedian , TestRegisteredMethodSampleStatus | unverified |
| eth_blockNumber | none | n/a | n/a | n/a | n/a | n/a | n/a | n/a | n/a | passthrough | `eth_blockNumber []` (Median variables read); the typed `BlockNumber()` derivation instead reports the corroborated reference height, never one provider's tip | Selected (at best) | Median of provider tips. The derivation's height is the only height with a corroboration claim behind it. | TestAdapterBlockNumberMatchesCorroboratedReference , TestRegisteredMethodSampleStatus | soak ✅ |
| net_peerCount | none | n/a | n/a | n/a | n/a | n/a | n/a | n/a | n/a | single-source local observation | `net_peerCount []` | Selected (at best) | First healthy source's own peer count. Not comparable, not dissent. | TestAdapterLocalObservations , TestRegisteredMethodSampleStatus | soak census ✅ |
| eth_syncing | none | n/a | n/a | n/a | n/a | n/a | n/a | n/a | n/a | single-source local observation | `eth_syncing []` | Selected (at best) | Consulted source's own sync state. | TestAdapterLocalObservations , TestRegisteredMethodSampleStatus | soak census ✅ |
| net_listening | none | n/a | n/a | n/a | n/a | n/a | n/a | n/a | n/a | single-source local observation | `net_listening []` | Selected (at best) | Consulted source's own listener flag. | TestAdapterLocalObservations , TestRegisteredMethodSampleStatus | soak census ✅ |

`eth_sendRawTransaction` is a `WriteMethod` (broadcast + relative trust, no
read-registry policy) and `eth_getProof` is `UnsupportedMethod`; neither
takes a moving-tag reference contract and neither appears in this matrix.

## Known deviations and risks (T02 exit)

- **R1 (pending vs 0035): resolved.** The five slice methods plus
  `eth_createAccessList`, `eth_feeHistory`, `eth_getBlockByNumber`, and
  `eth_estimateGas` serve pending as a node-local observation
  (`PendingLocal`, aggregator `resolveWithPending`): no anchor, no
  rewrite, ceiling Selected, winner Support and admission evidence
  preserved, no usable source fails Unknown. Non-pending
  `eth_estimateGas` stays the anchored lower median and never claims
  Corroborated. `eth_getBlockReceipts` keeps its local reject. Choice (a) extends that reject to the same pending spellings as `pendingTag`: case-insensitive strings (surrounding space included), JSON strings, and EIP-1898 objects whose `blockNumber` is pending. Non-pending objects stay in contract for the inner policy. `WithAnchor` still rewrites only string moving tags. The rejection is `ErrInvalidRequest` before any anchor or provider request, not a provider failure and not a business Unknown adjudication.
  T03-A closed the corroboration hole for `eth_getLogs`: a pending bound
  or a pending `blockHash` is rejected in Prepare before any anchor or
  provider request (`ErrInvalidRequest`, no stable corroboration
  semantics). That is not a provider failure and not a business Unknown
  adjudication. Field-level pending observation is still not implemented.
- **R2 (bare-hash anchor spelling).** The anchor rewrite for section-A
  methods emits a bare hash string. Public providers accept it (soak
  2026-09-05), but it is not the EIP-1898 standard spelling; the standard
  spelling is the `{blockHash}` object, which these methods now also
  accept and emit (for object inputs). Do not cite soak acceptance as
  standards compliance.
- **R3 (adapter has no requireCanonical channel for section A).**
  `BalanceAt`/`NonceAt`/`StorageAt`/`CodeAt`/`CallContract` take
  `*big.Int`, so `requireCanonical` can only arrive via root `Call` with a
  hand-built object. No adapter change is made in T02; the policy contract
  preserves the flag so a future typed channel cannot silently drop it.
- **R4 (`eth_call` revert data).** Resolved for the comparison key and for
  decision 0039 rule 4e. Missing and JSON null abstain on data; empty
  (`0x`), non-empty hex, and non-hex JSON remain different assertions and
  are not one comparison value. A short or unanimity-split data cluster
  keeps the corroborated revert and deletes `data`. Vendor code and message
  text are not in the key. Agreement is provider-backed evidence of the
  returned dimensions. Samples:
  `internal/rpcsample/testdata/revert-data/2026-09-23`. Tests:
  `TestNormalizeCallOutcomeSeparatesRevertDataStates`,
  `TestCallRevertDataConflictIsNotSwallowed`,
  `TestRevertDataSamplesReplayWithoutEndpoints`,
  `TestRevertDataBatchMatchesExpect`,
  `TestCallRevertRule4eMatrix`.
- **D5 (`BlockByNumber` explicit negative tags): resolved.** `nil` and
  `-2`/`-3`/`-4` all anchor first through the tag-aware seam
  (`Client.AnchorBlockHashForTag`, which validates the tag, establishes
  that level's reference, records the original tag in the evidence, and
  fails on uncorroborated references) and fetch by hash via `blockAt`.
  `-1` is a local rejection at the typed adapter (`pending block typed
  projection is not supported`): no anchor, no business request, no
  `-0x1` wire form, and no promotion into an ordinary block. The policy
  tag `pending` stays a node-local observation and is not rewritten into
  an anchor. `-5` is an explicit height `0x0`; unknown or out-of-range
  negatives fail locally before any request. `-2`/`-3`/`-4` are not
  collapsed into pending. Tests:
  `TestAdapterBlockByNumberNegativeTagsAnchorFirst`,
  `TestAdapterBlockByNumberRejectsUnknownNegativesLocally`,
  `TestAdapterBlockByNumberTagAnchorFailureSendsNoBusinessRequest`,
  `TestAnchorBlockHashForTagKeepsTagLevels`.

## Interlock

`rpc/methods/moving_tag_matrix_test.go` (`TestCatalogMatrixInterlock`)
parses this file: every `RegisteredMethod` must have exactly one row, no
row may name an unregistered method, and every row's support cells must be
explicitly filled (no blanks that could hide an undecided contract).
