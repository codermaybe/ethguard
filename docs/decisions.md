# Decisions

This file records decisions that are stable enough to guide implementation.
Unresolved proposals belong in `docs/design/` instead. A Status line marked
Implemented names the implementing code; anything still marked pending is
the remaining work list — do not maintain a second status ledger elsewhere.

## 0001. Method-aware aggregation

Status: Accepted

EthGuard will classify RPC methods and apply method-specific normalization and
resolution policies. It will not use raw JSON equality or one universal quorum
algorithm.

Reason: Ethereum RPC methods differ in determinism, block dependence, and
provider-local behaviour.

## 0002. Transparent fallback is not corroboration

Status: Accepted

Unknown or unsupported methods may use a default low-level RPC forwarding path,
but their results are marked `Forwarded` rather than `Corroborated`.

Reason: broad method coverage is useful, but silent claims of cross-source
corroboration would be misleading.

## 0003. Transaction management is out of scope

Status: Accepted

EthGuard may aggregate and classify RPC observations used when preparing a
transaction, including gas estimates, fee data, and transaction counts. It
does not optimize fees, allocate nonces, sign transactions, manage transaction
queues, retry broadcasts, or create replacement transactions.

Reason: those behaviours require account-local mutable state and application
policy. Mixing them with RPC response corroboration would make the security
interface larger and its guarantees harder to explain.

## 0004. One configuration model

Status: Accepted

Programmatic construction and YAML loading produce the same `config.Config` and
use the same defaults and validation before client construction.

Reason: separate configuration paths can silently produce different security
behaviour.

## 0005. Thin facade, per-method policy files

Status: Accepted

The root package stays a fixed three-file facade: `client.go` for construction
and lifecycle, `call.go` for the generic call pipeline, and `eth.go` for typed
convenience wrappers. Method semantics live in `rpc/methods`, with one file per
corroborated method named after the method (`eth_chainid.go`,
`eth_getblockbyhash.go`). Typed wrappers decode results through the owning
policy's normalization instead of private parsers.

Reason: per-method files at the root would grow without bound and duplicate
semantic decoding already owned by policies. Naming files after methods keeps
navigation direct as the policy set grows.

Amendment: the root facade gains `anchor.go` (the replaceable anchoring
strategies behind moving-tag reads, see decision 0007) beside the four files
above; construction, calling, typed wrappers, and error re-exports remain
where they were.

## 0006. Quorum semantics and participation-shrinking defense

Status: Accepted

Strict corroboration comes in two modes. The default requires every valid
response to agree, with at least two respondents: a single uncorroborated
response is marked `Selected`, never `Corroborated`. `WithQuorum(n)` relaxes the
first clause only: the winning cluster must hold at least `n` sources AND a
majority of the whole fan-out — measured against every attempted source, not
just survivors.

Reason: measuring the majority against survivors lets an attacker knock
honest sources offline until only colluding voices remain; measuring against
the full fan-out makes knocking honest sources out self-defeating, so forging
agreement requires corrupting more than half of the fleet. The floor `n` must
be sized above the number of sources that could be compromised together.
Ties and unmet quorums always return conflict evidence, never a guessed
winner; the winner inside evidence is chosen deterministically (cluster size,
then canonical key). Normalization failures ride on `Decision.Faults` instead
of vanishing.

Deliberately open: weighted voting, provider-independence scoring (same-
operator endpoints count as separate votes today), adaptive quorum.

## 0007. Anchored reads establish a corroborated block reference first

Status: Accepted (spike implementation shipped; refinements open)

Methods whose answers depend on chain state at a moving tag (`latest`,
`safe`, `finalized`) must not be compared across sources at their natural
heads. Their policies rewrite the moving tag to a corroborated block hash before
fan-out (`rpc.Anchored`), so all sources answer for identical state and
disagreement becomes meaningful evidence.

The anchor itself is established through the ordinary corroboration pipeline:
a latest-block query is fanned out and accepted only under the same strict
corroboration as any other read. The anchor policy is deliberately unanchored, so
establishment cannot recurse. Explicit numbers and hashes pass through
untouched; `pending` passes through because anchoring an unfinalized tag
would silently change its meaning.

Reason: without a shared reference, multi-source agreement is noise and
multi-source disagreement is false alarm — proxyd's production tag-rewrite
list confirms which methods need this treatment.

Deliberately deferred: conservative selection over divergent heads (common
ancestor, lowest safe/finalized) — today divergent heads fail the call with
conflict evidence instead of picking a winner; anchor caching across calls;
per-call anchor freshness bounds.

Amendment (anchor strategy seam): how a block reference is chosen is a
replaceable strategy, `ethguard.AnchorResolver`, injected with
`WithAnchorResolver`. The default implementation (`StrictHeadAnchor`) keeps
strict latest-head corroboration and rides the ordinary aggregation flow —
collect, resolve, record one event under the fixed `anchor` label — through a
dedicated aggregator whose registry holds only the unanchored head policy.
The deliberately deferred refinements above are future implementations of
this same interface, not edits scattered through client orchestration.

Amendment (conservative selection, opt-in): `WithFinalizedAnchoring()`
selects `FinalizedHeadAnchor`, pinning the reference at the lowest finalized
height that stays within the freshness bound (default 32 slots behind the
fleet's lower-median reported finalized height — a median-class reference,
because the heights are provider-reported and a minority lying about height
must not vote honest views out of the selection). The reference is what
providers report, not an Ethereum-consensus fact: safety rests on source
trustworthiness and cross-source consistency, never on a proof of on-chain
finality. The lower-median reference prevents an inflated outlier from excluding
honest views. A low-reporting source can still drag the pin backward within
the freshness bound; a pin supported by only that source remains `Selected`.
These rules do not make a colluding majority harmless. A fork
at the pinned height (two hashes for one height) fails closed with conflict
evidence; one uncorroborated pin is `Selected` class — bounded by the
freshness bound, and safe only under the source-trust assumption above. The
strict latest-head resolver remains the default until a consumer validates
the conservative path; both are interchangeable without touching call
orchestration. Still deferred: anchor caching / freshness TTL (same seam),
pool-side tip tracking as a freshness eligibility gate for all reads.

Amendment (freshness eligibility gate): `WithTipTracking` enables low-rate
block-tip sampling inside the pool (default 10s, riding the ordinary
collection path so probes share connections, chain-ID checks, and health
accounting) and a freshness gate that skips fan-out sources trailing the
fleet's reference height by more than the bound (default 32 blocks). The
reference is the lower median of sampled tips, not the maximum: a minority
of providers lying about their height cannot move it, extending the
participation-shrinking defense of decision 0006 to eligibility — a height
lie cannot vote honest sources out of the fan-out. Probes bypass the gate so
a fully stale fleet can always refresh its own picture; per-source cooldowns
still apply, and recovery is readmission by the next probe after catching up.
Tips ride `Health().TipHeight` for observability; sampling is opt-in and the
zero value keeps prior behavior.

Amendment (anchor reference caching): `WithAnchorCaching(ttl)` wraps any
resolver in `CachedAnchor`, sharing one establishment across every
moving-tag read inside the window — an anchored call costs one fan-out
instead of two. Failures are never cached; concurrent misses serialize on a
single establishment. Safety note: over conservative finalized selection the
pinned block is irreversible, so caching cannot regress safety; over strict
latest-head selection the TTL bounds how far behind the chain reads may
land, making freshness sizing a caller decision rather than a library
secret.

## 0008. One event per decision over a dependency-free seam

Status: Accepted

Observability enters through `ethguard/observe`: a `Recorder` receiving
exactly one `DecisionEvent` per aggregate decision — method, status class
(`corroborated` / `selected` / `forwarded` / `failed`), conflict flag, fault
count, per-source attempts with RTTs, total duration. Per-source attempts are
folded into the decision rather than streamed separately, so log and metric
volume scales with decisions, not fleet size. The core package imports
nothing outside the module; the Prometheus client lives behind
`ethguard/observe/prometheus`, whose collector applications register into
their own registry (or serve via its built-in handler). Hosts wire a sink at
construction: `WithRecorder` for custom or Prometheus sinks, `WithLogger`
for a one-record-per-decision slog adapter (Debug for clean successes, Info
when faults rode along, Warn on conflicts and failures).

Reason: a library must not dictate logging or metrics infrastructure, but an
aggregation layer without per-decision evidence is unauditable. A single
neutral event type keeps every sink equivalent; anchoring establishment is
emitted under the fixed literal `anchor`, keeping label cardinality bounded
by configuration. Source labels use configured display names, never
response data.

This seam supersedes the previously reserved `metrics/` placeholder and
inherits its two standing constraints: the recorder is passive — receiving
copies, never pool-owned health state or authority over eligibility and
aggregation decisions — and disabling it must not change any security
decision (the default sink discards everything).

## 0009. Classified sentinel errors at the public boundary

Status: Accepted

Every failure of an aggregate call wraps exactly one exported sentinel,
matchable with `errors.Is`, re-exported from the root package in
`errors.go`: `ErrNoEligibleSource` (fan-out impossible: nothing registered,
or all sources quarantined/cooling down), `ErrAllSourcesDown` (fleet
contacted, no transport-usable response), `ErrChainIDMismatch` (poisoned
identity), `ErrPoolClosed` (lifecycle), `ErrConflict` (disagreement or
unmet quorum), `ErrNoValidResponse` (responses arrived but none survived
normalization, including fleets of JSON-RPC error objects),
`ErrMalformedResponse` (a resolved value cannot be shaped at a typed
boundary such as `CallInto` or the anchor head). New classes may appear;
existing ones will not be removed or merged. Per-source causes stay
available on emissions and `Decision.Faults` alongside the class.

Reason: callers must branch on failure modes (retry a dead fleet? alarm on
conflict?) without parsing strings, and retrofitting classes after more
methods ship would break every consumer's error surface. The classification
is deliberately coarse — seven stable classes over the full pipeline — so
producers can adopt it without reworking their local error taxonomies.

Amendment to 0005: the root facade gains a fixed fourth file, `errors.go`,
holding only these re-exports; construction, calling, and typed wrappers
remain where they were.

## 0010. Health observations: windowed percentiles, throttle accounting, probe-driven recovery

Status: Accepted

The per-source monitor keeps, besides availability counters and the latency
EWMA: a bounded count-based window of recent successful RTTs (128 samples)
with nearest-rank P95/P99 estimates exposed on `HealthSnapshot`, and a
distinct counter for HTTP 429 responses, which still count as failures for
circuit purposes but are operationally different from being broken or
unreachable. Health probes and tip probes are the same mechanism: the
low-rate sampling loop observes established connections only (it never
dials — first contact stays caller-driven), bypasses cooldown circuits so a
recovering source is readmitted by the next probe instead of waiting out the
full cooldown against user traffic, and its successes reset the failure
circuit through the ordinary health path.

Reason: tail latency and throttling are the two signals operators ask for
first, and both are nearly free once responses are already flowing. Merging
probes with tip sampling keeps exactly one background loop whose rate is
bounded and whose side effects (health feedback) flow through the same
audited path as user traffic. Count-based windows keep memory bounded and
preserve the last known distribution for quiet sources.

Deliberately deferred: time-weighted decay of the window, agreement feedback
into scoring (designed in rpc-aggregation.md, not yet wired), per-source
request budgets derived from 429 rates.

## 0011. An ethclient-shaped adapter over the corroborated core

Status: Accepted

`ethguard/ethadapter` offers go-ethereum's client surface for the subset
backed by corroborated policies: `ChainID`, `BalanceAt`, `BlockByHash`,
`BlockByNumber`, `BlockNumber`, plus lifecycle passthroughs. Compatibility
is method-shaped, never interface-claimed — callers keep defining small
interfaces, and everything beyond the subset stays reachable via `Unwrap`
and the generic `Call`.

Semantics pinned by this decision:

- Moving references resolve through the configured anchoring strategy and
  its cache before fan-out; no adapter method ever fetches a moving tag.
- `BlockNumber` reports the height of the corroborated reference — a source-agreement
  override in the proxyd tradition, not one provider's tip.
- Typed decoding goes through exactly one owner, root `DecodeBlock`: it
  reconstructs header plus body like go-ethereum's own client (types.Block
  deliberately lacks a JSON unmarshaller), rejects hash-only transaction
  lists instead of silently dropping them, rejects uncle lists that contradict
  an uncle-free header, and — free with every response — rejects payloads
  whose wire hash disagrees with the computed header hash, so a consistently
  fabricated fleet cannot smuggle a self-inconsistent block past quorum.
  Responses upstream returns as JSON null map to `ethereum.NotFound`.
- Malformed-but-unanimous payloads die at this typed boundary as
  `ErrMalformedResponse`; malformed payloads that fail normalization earlier
  stay conflict/no-valid-response classes. Both are observable distinctions,
  not accidents of ordering.

Reason: switching cost is the adoption bottleneck for a corroboration layer;
the adapter removes it for the common read path while keeping every claim
honest. It also surfaced and fixed a latent bug: the previous
`Client.BlockByHash` decoded into `types.Block` directly, which silently
yields an empty block (no unmarshaller, unexported fields).

## 0012. Variable results resolve by robust selection, never Corroborated

Status: Accepted

Methods whose answers have no single true value at call time (gas prices,
fee estimates) resolve through `rpc.Median`: the fleet's lower median of
normalized integer values. The decision ceiling is `Selected` regardless of
how many sources agree — unanimity on a moving value is coincidence, not
corroboration of a deterministic truth, so it must never upgrade the class.
Outliers ride on `Reject` as spread evidence rather than vanishing or
triggering conflict; unparseable payloads ride as faults; a fleet with no
valid response fails as `ErrNoValidResponse`.

Reason: the lower median shares its manipulation bound with the tip gate's
lower-median reference — moving the answer requires buying more than half of
the fleet, and a lying minority only drags the result within its own rank.
Preferring the lower side keeps the bias conservative: when in doubt, the
cheaper estimate wins for fee observation, never an inflated one.

Deliberately deferred: weighted medians by source trust, spread thresholds
that fail closed when dispersion looks adversarial, trimmed-means variants.
The honest Selected ceiling matters more than any refinement until a fee-
sensitive consumer exists.

## 0013. Local observations stand for their source alone

Status: Accepted

Methods whose answers describe one provider's own state (`net_peerCount`,
`eth_syncing`) resolve through `rpc.Local`: the first healthy source's
answer stands, deterministically in pool order, ceiling `Selected`, never
Corroborated. Support names only the consulted source — local answers are not
comparable, so no cluster semantics apply; differing answers across sources
are not dissent and must not read as conflict. Unparseable payloads ride as
faults while selection continues to the next healthy source.

Reason: inventing a fleet truth for provider-local state would manufacture
confidence that does not exist; selecting without comparing keeps the claim
honest. The known cost is inherited trust: health gates keep failing sources
out, but a live liar's local view cannot be cross-checked by definition.
Consumers needing conservatism over local state should consult several
sources themselves. Deliberately deferred: derivation strategies over local
answers (for example any-syncing conservatism), rotation across sources to
spread first-answer trust.

## 0014. Receipts corroborate as immutable content by full agreement

Status: Accepted

`eth_getTransactionReceipt` joins the deterministic-read group: strict
corroboration over the whole receipt object through the shared canonical-object
normalizer. The transaction hash addresses immutable content — no moving
tags, no anchoring, no freshness machinery; honest sources either hold the
identical receipt or the dissent is evidence. A null answer is an ordinary
answer: unanimous absence (the transaction has no receipt yet) corroborates like
any other value and maps to `ethereum.NotFound` at the typed boundary, the
convention `DecodeBlock` already applies to blocks and go-ethereum's own
client applies to receipts. A fleet split between null and a receipt is
conflict — mixed absence is the divergent-tip problem in receipt clothing;
excluding stale reporters stays a deferred liveness knob.

Typed decoding rides go-ethereum's generated `Receipt.UnmarshalJSON`, whose
required-field set (`cumulativeGasUsed`, `logsBloom`, `logs`,
`transactionHash`, `gasUsed`) therefore binds after corroboration: a
unanimous payload missing one dies at the typed boundary as
`ErrMalformedResponse` (decision 0011's malformed-but-unanimous class),
while a source omitting a field others include conflicts before any decode.
Root `DecodeReceipt` also gains the receipt-side analogue of DecodeBlock's
wire-hash check: logsBloom is derived from a receipt's logs by consensus
rule, so a payload whose bloom contradicts its own logs cannot be honest,
however unanimous the fleet.

Considered and rejected in the normalizer: folding JSON-null `logs` to an
empty array (the schema types logs as required-and-array; erasing the
difference would let a schema-violating source corroborate an honest one),
and dropping null-valued fields wholesale (a source nulling a required
field would then corroborate a populated one, and the raw winner's quirk
would decide whether the caller hits a decode error — comparison and decode
must not disagree that way).

The landing also pinned the normalizer placement line: `rpc` keeps the one
normalizer true of any JSON (`CanonicalJSON`); every Ethereum wire shape is
canonicalized in `rpc/internal/wire/normalize.go`, and the 0x/hex conventions
never leave that directory.

Reason: receipt watching is the deposit-monitoring scenario's core read,
and it needs no new strategy — exactly why it lands first in the
method-batch queue. Deliberately deferred: `eth_getBlockReceipts` batching,
receipt-presence liveness exclusion, and reading the bloom check as more
than payload integrity (it catches self-inconsistent fabrication, not a
fleet that fabricates coherently — that remains quorum's problem).

## 0015. Method batch: the remaining template-shaped reads

Status: Accepted

Eight methods land in one complete batch (decision 0005's three touchpoints
each), chosen because none needs a new resolution semantic:

- Immutable content, strict corroboration over the whole payload:
  `eth_getTransactionByHash` (null → `ethereum.NotFound`, matching the
  receipt convention; a fleet split between null and an object is
  conflict; a corroborated pending transaction is an ordinary corroborated object),
  and `eth_getBlockReceipts` over the whole receipts array (a block hash
  determines its receipts fully; unknown block → NotFound; per-receipt
  required fields and bloom checks bind in `DecodeReceipts`).
- Anchored state reads — numbers and hashes pass through untouched, moving
  tags rewritten: `eth_getTransactionCount` (quantity),
  `eth_getStorageAt` (hex data; the storage key passes through, the tag at
  position 2 is rewritten), `eth_getCode` (hex data; "0x" is valid data —
  an EOA's code — which is why data reads do not share the quantity
  normalizer: `normalizeHexData` is a new class).
- Variable: `eth_maxPriorityFeePerGas` rides the Median strategy exactly
  like `eth_gasPrice`.
- Local: `net_listening` rides `rpc.Local` exactly like `eth_syncing`.
- Strict, decimal: `net_version` spells the chain identity in its legacy
  decimal encoder, numerically compared and cross-spelling tolerant —
  deliberately NOT reconciled with `eth_chainId`'s hex form. They are
  separate wire encoders; a divergence between the two methods is a
  provider's bug worth exposing, and cross-checking them would be
  derivation over corroborated answers, which stays unwired.

`DecodeTransaction` adds the transaction-side analogue of DecodeBlock's
wire-hash check (the recomputed signature hash must equal the wire hash)
and rejects unsigned payloads; the adapter reports `isPending` from the
wire block number's presence, keeping go-ethereum's `(tx, pending, err)`
shape. Deliberate deviation from ethclient: the sender is not cached from
the server's `from` field — callers derive it with `types.Sender` when
needed; the server-provided `from` is unverified context, and caching it
would lend it the authority of a derived fact.

The adapter decomposes `rpc.BlockNumberOrHash` into a plain string
reference before fan-out: the object wire form would carry a moving tag
past the anchored rewrite unrewritten, turning the read into inevitable
dissent.

Reason: these eight complete the cheap half of proxyd's rewritten-method
table. Every remaining candidate needs a design pass first —
`eth_call` (revert-as-answer semantics), `eth_getLogs` (set comparison over
anchored ranges), `eth_estimateGas` (execution errors meet anchoring),
`eth_feeHistory` (variable arrays over moving ranges), `eth_blockNumber`
(moving-tag head semantics). Deliberately deferred: the `*AtHash` adapter
variants (trivial once wanted), `eth_blobBaseFee` (same Median shape, no
demand yet).

## 0016. Presence splits fail closed until staleness is provable

Status: Accepted

The live soak fleet (2026-09-05, chainlist mainnet) produced a stable
specimen of a divergence class the error taxonomy had only theorized: a
major public endpoint answers null for a minutes-old finalized block that
the rest of the fleet holds. Every immutable-content read — blocks,
transactions, receipts, block receipts, and the blockNumber derivation —
therefore shows the same shape in the wild: N-1 sources return the object,
one returns null, and strict corroboration fails closed as conflict.

The ruling has two halves:

- The comparison layer is unchanged: null stays an ordinary answer, and
  mixed presence remains conflict. A null meaning "not yet" is
  indistinguishable from a null meaning "never" at comparison time, and no
  fleet-internal evidence separates them without adding a trust assumption.
- The accepted liveness mechanism, when wired, is a provable-stale
  classification: a source answering null for content at height H while
  its sampled tip height exceeds H cannot honestly lack that content — it
  is stale, with evidence, in the same sense FinalizedHeadAnchor already
  excludes lagging reporters. The predicate trusts the majority cluster's
  claimed height only where quorum already trusts the majority, so the
  threat model does not move. Until that mechanism lands, fail-closed
  stands.

Reason: five call sites repeating one fail-closed behavior is one design,
not five — the soak turned the deferred liveness knob from a philosophical
choice into a computable predicate. Deliberately deferred: the wiring
itself (one shared policy option across the immutable-content methods),
and any timeout- or age-based staleness heuristics, which would trade
verifiability for a clock.

## 0017. Method batch: execution, logs, and fee history

Status: Accepted

Four methods land in one complete batch (decision 0005's three touchpoints
each), implementing the 2026-09-04 rulings on top of the anchor decorators:

- `eth_call` corroborates as behavior, not content: a unanimous execution
  failure is a corroborated observation — the decision value carries the
  revert envelope — a split between results and reverts is conflict, and
  the content inside errors stays out of scope, realized by collapsing
  every revert-class payload to one comparison key so benign
  client-to-client wording differences cannot fake dissent. The revert
  classifier accepts code 3 (legacy parity) and -32000 with a revert
  message; geth's own client-side codes (-32002 timeout, -32003 response
  too large, -32603 panic) and every other protocol error ride as faults,
  never as execution observations. The block tag at position 1 is
  rewritten to the corroborated reference. The adapter boundary surfaces a
  corroborated revert as `RevertError` carrying message and data as
  unverified provider context — the standing decision 0015 gives the
  server-provided `from` — while the corroboration claim stays on the
  decision evidence.
- `eth_estimateGas` is anchored + Median and never claims Corroborated
  (ruling 2026-09-04). Sources whose estimation fails ride as faults; a
  fleet that unanimously fails fails as no-valid-response, so a revert is
  never laundered into a number. The adapter always sends the block tag
  explicitly so the pin applies, a deliberate deviation from geth's own
  client, which omits it.
- `eth_getLogs` corroborates by full agreement over the whole log array once
  the range is pinned (analysis 2026-09-04): logs are chain-ordered, so
  the canonical-object normalizer erases benign encoding differences and
  whatever still differs is dissent. The filter object carries the range
  in fromBlock/toBlock fields, not parameter positions, so the rewrite is
  height-field-shaped (`rpc.WithAnchorHeightFields`): moving tags become the
  corroborated height and absent bounds are defaulted to it — a provider's
  implicit latest is a moving tag in field clothing. blockHash pins pass
  through; blockHash mixed with a range field is rejected in Prepare.
  Deliberately deferred: payload cost (singleflight territory), and
  pruned-node gaps, which fail closed as dissent pending an eligibility
  ruling — the same shape as decision 0016.
- `eth_feeHistory` corroborates by full agreement over the pinned range. The
  wire form forced a new anchor shape: newestBlock is a BlockNumber, not
  a BlockNumberOrHash, so a 32-byte hash cannot ride the wire — the tag
  is rewritten to the corroborated reference's height
  (`rpc.WithAnchorHeight` over `AnchorHeightProvider`), with the height
  derived from the same establishment response as the hash at no extra
  fan-out. Cross-client rounding of the reward percentiles is the batch's
  only liveness gate; until the soak measures it, rounding differences
  fail closed as conflict.

Reason: this batch completes the corroborated surface relative to proxyd's
rewritten-method table and the local/variable/identity groups — every
remaining unregistered method is deliberate (`eth_blockNumber` derivation,
P13). The open gates are soak-shaped, not design-shaped: provider error
shapes for eth_call's classifier, getLogs payload cost and pruned-node
eligibility, feeHistory percentile rounding, and first soak rows for the
new wire forms in `wire-forms.md`.


## 0018. Common references behind an unchanged ethclient-shaped interface

Status: Accepted (2026-09-06)

`ethadapter` is the primary client for supported methods. No public method
signature or caller-managed anchor/confirm/read sequence is introduced.
Deterministic adapter reads must be corroborated before returning values;
advanced root Call retains Selected decisions for diagnostics and explicit use.

Finalized reference establishment separates candidate selection from
confirmation. Observe the initial eligible fleet once, filter excessive lag
against the lower median, and select one common height. Default mode requires
all initially fresh valid views (at least two); configured quorum additionally
uses the original participation count, including failures, as the majority
denominator. In quorum mode choose the highest candidate covered by enough
fresh views. Same-height observations can be reused. Query higher-reporting
sources at the candidate height only when needed; reject wrong-height answers.
No retry loop chooses successively older candidates or shrinks support after
confirmation failures. Per-collection timeouts cover both rounds.

Reference evidence records origin, number, hash, original participants,
supporters, rejects and faults. A finalized supporter must both report finalized
coverage of the candidate height and agree on its hash. This is provider-backed
evidence, never a proof of on-chain finality. Custom legacy resolvers remain
usable through automatic fleet confirmation; their origin is custom-reference.
Cache full evidence, copy mutable slices, and honor each waiter's context.

The internal reference module owns this workflow. Data decisions retain their
own evidence alongside Decision.Reference. No user must consume evidence to
avoid an implicit downgrade in an ethclient-shaped deterministic read.


## 0019. Complete block content is bound to the corroborated block identity

Status: Implemented (2026-09-20; `rpc/internal/objects/block.AdmitBlock` checks header hash, transaction trie root, withdrawals root, and uncle commitments; `DecodeBlockWithEvidence` re-checks at the typed boundary).

An interface returning a complete block must protect its required business
content, not only corroborate number and hash while returning an unchecked
body. Sources corroborate block identity; the returned content must match the
commitments in that block's header.

Decode RPC representations into protocol objects before computing commitments.
Do not hash raw JSON. Compute the header hash using Ethereum's header encoding;
check the ordered transaction trie root, withdrawals root where applicable,
and the uncle hash using complete uncle headers for historical blocks.
Required content that cannot be obtained or validated must not be silently
omitted from a successful complete-block response. Supplier-added metadata is
not automatically covered by these commitments; its treatment remains a
separate method-contract question.

Validate compatibility against real RPC responses for the same block via both
number and hash, covering transaction types, empty blocks, withdrawals and
historical uncles. Record missing/null fields and encoding differences.
Separately test replacements, omissions and reordering against unchanged
headers, and accept representation-only differences after protocol decoding.
Measure cost rather than assuming the checks have negligible overhead.

The current DecodeBlock path does not yet implement this contract: transaction
roots are not checked and withdrawals and complete uncle bodies are not
preserved. Existing method tables describe current behavior until updated by
implementation evidence. Pre-adjudication validation and invalid-answer handling are specified by
decision 0020; their implementation remains pending.

This decision does not introduce a light client, full block re-execution or a
new quorum threshold. Content-to-header consistency does not prove canonical
chain membership or finality, and does not remove the risk of sources jointly
fabricating block identity. Previously accepted cross-source comparison rules
remain in place until their replacement is explicitly specified.


## 0020. Method admission rules precede adjudication

Status: Implemented (2026-09-20; `aggregator.Aggregate` runs `AdmissionPolicy.Admit` before normalization and clustering; inadmissible answers keep their source in the voting denominator).

Each method defines request validation and response admission independently of
voting. Reject invalid request types, ranges, mutually exclusive arguments and
unsupported tags locally. Before an answer contributes support, validate its
required fields, types, lengths, ranges, request binding and applicable content
commitments. Distinguish missing, null and empty values explicitly.

Normalize only documented equivalent representations. Do not infer missing
fields, coerce types or loosen admission merely because multiple providers
return the same unsupported form. Live RPC compatibility evidence informs
explicit method-specific rules; it does not override them automatically.

An invalid answer contributes no support but its selected source remains in
that collection's quorum denominator. Adjudicate among admissible answers;
reject when the required support is unavailable. Record concrete failures;
future source-health or membership consequences are a separate decision.

Fields outside the protected method contract must not silently enter the
protected business result. Their preservation as evidence is covered by 0021.
Protocol conformance, content consistency and required cross-source
corroboration are separate conditions for methods claiming that guarantee.

## 0021. Uncertain preserves untrusted data from an individual invocation

Status: Implemented (2026-09-20; `ethguard.WithUncertain` context capture plus `internal/uncertain` collector; see 0034 for the API shape).

Uncertain is a byproduct of one invocation, attached to its outcome whether it
succeeds or fails. It is not another upstream RPC query and not mutable
client-wide "last response" state. It provides upstream data for compatibility
without interpreting its business meaning or guaranteeing its reliability.
Reading it does not change the main result's admission rules, status or error.

Group retained fields by field path. Each path holds an array of independently
wrapped source/value entries. Preserve separate entries even when values agree;
array order is stable and expresses no trust ranking. Do not normalize values,
coerce numbers or merge equal values. Explicit null has an entry with a null
value; a missing field has no entry for that source. No presence or reason
property is required. Request/reference context belongs to the invocation;
source outcomes and validation reasons remain in error/diagnostic evidence.

Also retain whole unaccepted responses that cannot be parsed or grouped by
field, in a separate source-associated raw-response collection. Preserve the
available raw data without repairing or interpreting it; malformed data must
not prevent access to Uncertain. The concrete representation of arbitrary raw
bytes remains an API choice. Existing transport/decode paths will need to be
checked for whether they expose the original data.

Uncertain does not promise complete collection: absence of an entry does not
prove that a field is absent at every provider. Preserve provider-specific
extensions and unaccepted/uncorroborated data that is available for retention.
A required field failing admission invalidates that source's answer; moving
it into Uncertain cannot turn the remaining object into a successful complete
result. Independent acceptance of a subset requires an explicit method
contract. Dissent is not proof of falsity and agreement within Uncertain is
not proof of reliability.

Exact Go types, field-path syntax, retention bounds and access through the
unchanged ethclient-shaped adapter remain to be designed. The suggested
Uncertain(paths ...string) spelling is not an accepted API signature.


## 0022. Preserve standard null semantics and adjudicate mixed answers normally

Status: Accepted design boundary (2026-09-07); implementation alignment pending.

For object-query methods whose response contract permits null, retain null as
a valid response. Block queries expose it as JSON null at the RPC boundary and
map an accepted null result to ethereum.NotFound at the ethclient-shaped
boundary, following Geth. Do not introduce a new absence classification, infer
why the provider returned null, repair it into an empty object or treat the
provider as unhealthy solely for this response. This is not a proof of absence.

The meaning of null is separate from selecting among multiple responses.
Default Strict rejects a mix of valid objects and null. Explicit quorum may
accept either cluster when the existing threshold is satisfied. A single
valid null remains Selected at best, and cannot bypass a typed read's required
corroboration. A null response neither ends collection early nor disappears
from comparison simply because another source supplied an object.

Retained unaccepted answers remain available through the Uncertain semantics
of 0021. Missing fields, malformed payloads and null where the method contract
disallows it remain admission failures, not equivalent not-found responses.

Current byNumber normalization rejects null; aligning it with this decision is
pending. ByHash already compares null but documentation must qualify conflict
behavior by Strict versus explicit quorum. No quorum threshold or default mode
changes in this decision. For permitted null/object splits, this decision
clarifies 0016: default Strict remains fail-closed, while configured quorum
uses its existing acceptance rule.

Reference: Geth v1.17.5 internal/ethapi/api.go GetBlockByNumber/GetBlockByHash
and ethclient/ethclient.go getBlock; execution-apis src/eth/block.yaml.


## 0023. Revert data protection precedes provider-error normalization

Status: Accepted design boundary (2026-09-07); equivalence rules, live validation and implementation pending.

Preserve upstream JSON-RPC code, message and data without arbitrary rewriting.
Separate transport/protocol failures from method execution errors. Only error
classes explicitly admitted by a method contract may participate in
adjudication; other retained raw errors may be provided through Uncertain.

For eth_call, compare execution outcomes at the same fixed block reference and
with the same execution parameters. Protect the revert bytes available for
business decoding. Equal messages must not hide different revert data, and
all revert-class answers must not be considered equivalent solely because they
indicate a revert. Do not preemptively equate missing data, null and empty
revert bytes.

Provider-specific code, message and envelope differences require real samples
before an explicit compatibility rule is accepted. Validate standard
Error(string), Panic(uint256), custom errors and empty reverts, alongside
upstream timeout/rate-limit errors that might otherwise be misclassified.
Establish whether identical bytes can be extracted reliably from different
envelopes and how differing or unavailable bytes affect admission and dissent.
No specific envelope equivalence or missing-data fallback is accepted here.

This revises the target boundary of 0017's behavior-only eth_call comparison.
The current implementation still collapses revert-class answers to one key;
that remains an implementation fact, not evidence that their data agrees.
Concrete normalization rules and the outward error representation remain to
be settled after validation. No code changes or live validation have occurred
as part of this decision.


## 0024. Standard method fields define the protected result

Status: Accepted design boundary (2026-09-07); per-method schemas and implementation pending.

Define protected method fields from applicable Ethereum execution API and
protocol rules, with explicit method, parameter and fork/version scope.
Missing required fields make a source's answer inadmissible. Protocol-optional
fields follow their specified conditions; optional does not mean universally
ignorable. Provider extensions outside the protected contract are retained
through Uncertain and do not create main-result conflicts solely because they
differ across providers.

Do not fill missing fields from other sources to manufacture a complete
admissible answer. This does not prohibit method-defined dependent requests,
such as retrieving uncle headers and validating them against commitments;
such acquisition is explicit and must satisfy the content contract.

Per-method field sets and compatibility rules remain to be enumerated and
validated. Current whole-object comparisons may still conflict on extensions;
this decision records the target behavior, not a completed implementation.

Required closing review: after all boundaries have been settled, compare the
complete design against the applicable Ethereum light-client rules. Pin the
specification version and relevant fork, and document applicable requirements,
conflicts, non-applicable rules and unresolved items with references. Include
trusted roots/checkpoints, header/content commitment binding, finality versus
optimistic state, freshness and missing-data behavior where relevant. Do not
infer light-client compliance merely from RPC compatibility or cross-source
agreement. The review is pending and does not authorize light-client adoption.


## 0025. Freeze the voting basis within one logical invocation

Status: Implemented (2026-09-20; `Client` snapshots corroboration mode, floor, and `pool.VotingMembers` via `internal/refcontext` at invocation start; finalized confirmation preserves the first-round denominator).

For a corroborated read, establish the voting membership and configured
admission thresholds at invocation start, before reference establishment.
Keep this basis through reference establishment, any confirmation, and the
business read. Timeouts, unavailable sources, invalid answers and cooldown or
freshness changes during the invocation do not shrink its voting denominator
or relax its support requirement. Refuse a result if the required support is
not obtained.

Voting membership is distinct from which members actually need to be queried
at a particular stage. Reusing evidence or requesting a subset for confirmation
does not create a smaller voting basis. A member cannot acquire extra votes
through repeated requests, and replacing an unavailable member cannot silently
change the invocation's voting set. Stage-specific evidence remains distinct;
support for a reference is not automatically support for a business value.

This governs the lifetime of the voting basis, not a new universal response
policy: Strict, explicit quorum, estimates and local observations retain their
method semantics. It does not require all configured sources to become voters,
select how membership changes between invocations, or introduce a fixed global
fleet. Those choices and the admissibility of cached reference evidence from
another invocation remain open.

Current Collect preserves its own selected emissions, and finalized
confirmation preserves the initial reference-round denominator. The full
reference-to-business invocation snapshot required here is not yet implemented.
Update and test these paths before describing the guarantee as available.


## 0026. Establish moving references afresh for each invocation

Status: Implemented (2026-09-20; no cross-invocation caching exists; `WithAnchorCaching` and a non-empty `anchor_cache_ttl` return migration errors citing this decision).

For the current design scope, do not reuse block-reference or business-result
caches across logical invocations. When a method needs a moving reference,
establish it through fresh upstream requests for that invocation, then hold
it fixed through that invocation's dependent reads. Explicit historical
references retain their method semantics. Connection reuse remains permitted.

Fixing a reference within an invocation prevents comparing different states;
caching it across invocations is a separate cost optimization and is not
required for that consistency. Fresh requests do not guarantee a globally
current head: upstream freshness and latest/safe/finalized semantics remain
separate contract concerns.

The existing optional WithAnchorCaching/AnchorCacheTTL/CachedAnchor paths have
not been removed or changed. Cross-invocation caching is outside the current
target scope; handling existing options requires an explicit migration choice,
not silent configuration changes. Permanent deletion is not decided. Revisit
an explicit optional cache only if measured request cost or rate limiting
justifies its freshness and evidence-lifecycle complexity.

This supersedes the open cache-reuse question in 0025 for the current scope;
old reference evidence cannot supply this invocation's fresh establishment.
No implementation or live validation was performed in recording this decision.


## 0027. Caller-managed membership is independent of health scheduling

Status: Implemented (2026-09-20; `pool.Remove`/`Join` affect later invocations only; an in-flight invocation keeps its captured members in the denominator; health and tip gates only skip requests).

The caller explicitly supplies voting members through configuration or active
runtime membership operations, including add. Health, rate limiting,
unavailability and freshness gates may pause or resume requests to a member;
they do not automatically remove it from the voting basis. A selected member
that cannot contribute an admissible answer still occupies its place in the
invocation's majority denominator.

An explicit membership update becomes visible to subsequent invocations. An
in-flight invocation retains the membership and thresholds captured at its
start under 0025; adding, removing or replacing a source cannot rewrite that
snapshot. Lifecycle/resource handling must preserve that basis even if a
source can no longer be contacted. No provider grouping, reputation weights
or distributed membership protocol is introduced.

Source selection and admission are the caller's responsibility. The library
does not prove that supplied URLs have independent owners or defend against
a caller populating the membership with many malicious identities. Runtime
add does not establish honesty; it explicitly changes the configured trust
assumptions. Keep ordinary identity deduplication and method admission checks,
and do not let upstream response data silently add voting members.

With five voting members and explicit quorum floor three, three matching
admissible answers can succeed even if two members are cooling. Two answers
cannot succeed merely because the other three became unavailable. Persistent
loss of sufficient members requires caller maintenance rather than automatic
denominator shrinkage. This example does not select a default quorum floor.

Default Strict versus explicit quorum and their overall support requirements
remain the next decision; fixing membership alone does not give the current
valid-answer-only Strict policy a majority requirement. Current collection
selects eligible sources and does not yet implement the target voting basis.


## 0028. Default corroboration uses fixed-membership majority with unit votes

Status: Implemented (2026-09-20; `rpc.Corroboration` with `Q = max(H, floor(N/2)+1)`, unanimity as explicit mode; see 0029 for the default floor).

For methods requiring cross-source corroboration, default to a strict majority
of the invocation's fixed voting membership and a configurable support floor:
Q = max(H, floor(N / 2) + 1). A Corroborated outcome additionally requires at least
two supporting sources. Each explicitly admitted member has one vote; health,
latency and provider identity do not multiply its voting power. The default H
and minimum supported membership remain to be settled.

Retain valid-answer unanimity as an explicit mode, with a minimum support
requirement so that unanimity alone cannot admit too few responders. Its exact
threshold configuration remains to be specified. Method admission,
normalization and content protection do not change between modes. Estimates,
provider-local observations and forwarding retain their separate semantics.

Isolate corroboration adjudication in a module with replaceable modes. Method
contracts own response meaning and admission; pool scheduling owns transport
and availability. The adjudicator consumes a fixed voting basis and admissible
support evidence, preserves non-contributing members in the basis and returns
reproducible decisions. The precise package name and interface are not decided.
Current rpc.Policy/Strict provide an existing seam to evaluate, not a completed
implementation of this separation.

Equal votes do not make arbitrary distributed consensus protocols compatible
with ordinary RPC providers. Any additional mode must state its membership,
fault and interaction assumptions; only protocols realizable with the available
upstream behavior can be claimed. No additional algorithm, validator protocol,
reputation weighting or on-chain finality guarantee is accepted here.

This revises the default-mode direction of 0006. Existing defaults and eligible
collection remain unchanged in code; compatibility and migration must be
specified before implementation is described as complete.


## 0029. Default support floor is two for corroborated reads

Status: Implemented (2026-09-20; `config.DefaultSupportFloor` returns 2, pinned by `TestDefaultSupportFloorIsTwo`).

Use H=2 by default for corroborated reads, retaining fixed-membership majority:
Q = max(H, floor(N / 2) + 1). For N=1 corroboration cannot succeed; for
N=2,3,4,5 the default required support is 2,2,3,3 respectively. H=2 prevents
single-source Corroborated outcomes; it is not a budget of two Byzantine members.
Callers can raise H and must relate the effective Q to their fault assumptions.

The explicit valid-answer unanimity mode requires this same support threshold
and additionally rejects disagreement among admissible answers. It does not
admit a smaller surviving set solely because those survivors agree.

The user also requested intentional single-source use. Its forwarding-mode
selection and explicit transition rules are specified by 0030; automatic
degradation of corroborated calls to forwarding remains prohibited.


## 0030. Intentional single-source mode and trusted configuration control

Status: Implemented (2026-09-20; one configured upstream with no explicit mode selects forwarding; explicit quorum with one member is a configuration error; runtime shrinkage never auto-switches modes).

At initialization, exactly one explicitly configured voting source with no
explicit mode selects single-source forwarding. An explicit quorum mode with
insufficient configured membership is a configuration error, not permission to
forward. Runtime health loss or member removal must not automatically switch
modes: a corroborated client reduced to one member retains its mode and refuses
insufficient support. An explicit caller mode change takes effect on subsequent
invocations; in-flight invocations retain their captured mode and voting basis.

Single-source mode retains applicable supported-method format, request-binding
and content checks. Its result is Forwarded, never Corroborated, and may carry
per-invocation Uncertain. The ethclient-shaped adapter may return locally
validated results in this intentional mode; its existing corroboration-only
gates require implementation alignment. Unknown-method forwarding remains
subject to its existing lack of a specialized method contract.

Authorized configuration and runtime member/mode operations are trusted control
inputs; RPC provider responses are untrusted data inputs. The caller owns
configuration and management-interface access control, source admission and
process/runtime protection. If an attacker changes configuration and restarts
the program with one source, the library cannot infer whether that configuration
is malicious or intentional. Protecting that deployment boundary is outside
RPC adjudication, while configuration validation and faithful mode/threshold
execution remain the library's responsibility.

Expose mode and guarantees accurately, and record selected mode, voting-member
count and effective support requirement at startup. Forwarding has no
corroboration requirement and must be represented accordingly. Observability
helps audit configuration but does not authenticate it. See threat-model.md.

This decision does not implement mode switching or change existing defaults.


## 0031. Health scheduling includes adaptive per-source congestion control

Status: Accepted direction (2026-09-07); controller, parameters and implementation pending.

Use temporary backoff and controlled recovery for identified rate limiting and
transport/availability failures. Legal null, admitted execution reverts and
valid dissent are not health punishments. Invalid content cannot vote and is
retained in diagnostics/Uncertain as applicable; permanent membership removal
is not an automatic consequence. Cooldown recovery admits controlled trial
traffic rather than releasing all concurrent callers at once. The proposed
first scope avoids repeated business-request retries inside one invocation.

The user additionally requested per-RPC adaptive congestion control: observe
rate-limit timing/frequency and adjust request scheduling to operate with
headroom below observed capacity, aiming to reduce recurring throttling after
adaptation. This is a scheduling objective, not a guarantee of eventual stable
capacity. Fixed waits alone are insufficient for the requested direction.

Accepted clarification: operate with headroom below estimated sustainable
capacity, not a mandatory minimum business-request frequency under congestion.
Start conservatively; increase cautiously when actual demand yields sustained
successful exchanges, and reduce promptly or pause on explicit throttling.
Resume from the reduced rate rather than immediately restoring the previous
peak. Low demand without throttling is not evidence of high capacity.

Evaluate proportional headroom, slow-increase/fast-decrease pacing and a
concurrency bound together; exact algorithms, margins and default numbers
remain to be validated. Illustrative rates are not configuration commitments.
All per-source traffic, including reference establishment, confirmation,
business reads and probes, must consume the source's scheduling budget.

Waiting for permission to send is bounded by the invocation deadline. If no
slot is obtained, record a local unattempted member, without a source-health
penalty or a smaller voting denominator. Membership, vote weights, mode and
acceptance thresholds do not change in response to congestion feedback.
Historical scheduling observations are permitted; they are not business-result
or block-reference caches.

Cross-endpoint/shared-account quota scope, concrete queue bounds and recovery
parameters remain implementation-design questions. Test changing capacity,
mixed method costs and concurrent traffic before claiming controller stability.


## 0032. Preserve block-tag meaning while choosing a stable common reference

Status: Accepted semantic direction (2026-09-07); freshness bounds and implementation alignment pending.

Keep caller-requested reference semantics. Explicit hashes/heights remain
explicit; latest must not silently become finalized to obtain success.
Safe/finalized reads must preserve their requested level and fail if adequate
reference support cannot be established. Pending must not be rewritten into a
mined-state read or claimed as cross-source Corroborated merely through agreement.
Exact per-method pending support and exposure remain to be enumerated.

Within a requested tag's semantics, a small loss of freshness in selecting a
common reference is acceptable to obtain stable corroboration. The user
explicitly prioritizes confirmed, stable observations over chasing the newest
reported finalized height, including for asset monitoring and bridge consumers.
Permitted lag limits and their units remain to be specified; this does not
permit unbounded fallback to older candidates or cross-invocation caching.

Discussion example: finalized reports A=10000, B=9999, C=10001, D=9999.
With four fixed members and default H=2, Q=3. Height 9999 is a common candidate;
10000 has only two reporters claiming finalized coverage. Higher reporters
must corroborate the candidate's block identity, not merely its height. Values
from different heights are not interchangeable support votes. Record finalized
coverage and candidate hash agreement separately; reject conflicting or
insufficient evidence rather than silently relaxing requirements.

Accepted candidate rule (2026-09-07): choose the highest finalized height
covered by the required number of admissible reporters under the selected
mode. In fixed-majority mode, if the required support is Q, sort admissible
reported heights descending and select the Q-th height; reject if fewer than
Q reports are available. Reports below the candidate cannot contribute
finalized coverage. Missing or ineligible reports do not shrink the fixed
membership denominator or reduce Q. Freshness admission limits remain open.

Confirm this one candidate's block identity. Reuse already admissible
same-height evidence; where needed, query higher reporters at the candidate
height once. Require both finalized coverage and matching candidate hash from
each supporter. Apply the selected mode's acceptance rule: quorum may tolerate
minority dissent, whereas unanimity applies its stricter agreement rule. If
confirmation fails that rule or lacks support, reject this invocation rather
than retrying successively older candidates. Do not confuse minority dissent
with automatic quorum failure, or coverage reports with hash confirmations.

This remains provider-backed reference evidence. Reported finalized coverage
and matching block hashes do not constitute independently corroborated Ethereum
finality. Do not label the result as cryptographic finality or a complete bridge
security guarantee. The required closing light-client review remains pending.


## 0033. Separate freshness from agreement without prescribing a fixed lag

Status: Accepted semantic boundary (2026-09-07); freshness policy deferred.

Cross-source agreement and freshness are separate properties. One provider's
highest reported height cannot alone establish that the others are stale, and
majority agreement does not establish that the agreed state is sufficiently
recent. Freshness controls must not silently change requested tag semantics,
voting membership or acceptance thresholds.

The user explicitly deferred allowable lag and its determination. Do not
require a fixed block-count limit, choose a default number, or make a static
lag option a prerequisite for the general adjudication design. Static or
adaptive criteria may be evaluated separately with chain/method semantics and
observations. Once a freshness requirement is defined and applicable, failure
to meet it means rejection rather than a tag change or weaker corroboration.

This principle applies where freshness is meaningful for the method and
reference; explicit historical reads must not be rejected merely for requesting
an old block. The highest sufficiently covered finalized-candidate rule in
0032 is retained, with no new lag value imposed here. Existing tip/finalized
filters remain implementation facts pending alignment, not newly approved
freshness defaults. This decision does not disable or modify them in code.


## 0034. Opt-in Uncertain capture uses the invocation context

Status: Implemented (2026-09-20; `ethguard.WithUncertain(ctx)` returns the call context plus capture handle; README documents the opt-in flow with a compiling example).

Use a package-level context collector entry point with the intended call shape:

```go
callCtx, capture := ethguard.WithUncertain(ctx)
block, err := client.BlockByNumber(callCtx, number)
// After return, inspect this invocation's retained Uncertain via capture.
```

Ordinary callers keep their existing context, method signatures and Geth return
types. Users who need Uncertain explicitly create a collector for that logical
invocation; this is the accepted extra code at the advanced-use boundary.
A wrapped context enables capture without a second global boolean switch.
Initialization callbacks, Result wrappers and client-wide last-response state
are not the selected business-access mechanism. Concrete collector accessors
and data types remain to be designed; the external pseudocode is not an
implementation specification.

The capture belongs to one top-level invocation, including reference,
confirmation and business-read stages. Preserve stage/request/source identity;
multiple raw responses from one source must not overwrite one another simply
because a map is keyed only by source. Each independent invocation uses a fresh
collector. Contexts can be shared across goroutines, so context.WithValue alone
does not provide concurrency isolation. Implementation must prevent collector
reuse from silently mixing invocations and synchronize internal fan-out writes;
the exact reuse-error contract remains to be specified.

Before the public call returns, publish a stable view of retained data on both
success and failure, including cancellation paths where data was collected.
Do not mutate that published view through late completions. No data is invented
for an unanswered request, and existing retention/completeness limits still
apply. A mutable exported map filled without synchronization does not satisfy
this boundary. No collector means no additional full Uncertain payload
retention, while normal admission checks and diagnostics still run.

Accepted result ownership and collection direction (2026-09-07): Uncertain is
read-only result data. The collector handle does not expose public mutation
operations. Keep mutable construction private; collect independently per source
and stage, then aggregate and publish once at invocation completion rather than
contending on a shared map for every field. Publication, cancellation and
retention-budget coordination still require correct synchronization; this is
not a claim of lock-free construction.

After publication, concurrent readers observe the same stable result. Accessors
must not expose mutable aliases to owned maps, nested slices, raw byte buffers
or pointer-based values. Use immutable representations or defensive copies as
appropriate; even if a caller mutates a returned copy, the stored result and
other readers remain unaffected. Reused transport buffers and late tasks must
not modify published data. Concrete read-only accessor types remain open.

Bound Uncertain retention during collection, not only after allocating all raw
responses. Make truncation/retention omission discoverable through read-only
result metadata; this is completeness information, not per-field presence or
reason classification. Exact limits and retention selection policy require
measurement. Exhausting the auxiliary retention budget does not relax main
validation, change adjudication or convert an otherwise valid result to an
Uncertain-induced failure; preserve and report the available bounded evidence.

Validate these properties using concurrent readers, attempted mutation of
returned buffers, late completion/cancellation and large block/log/error
payloads. Measure allocation and contention with capture enabled and disabled;
no efficiency claim is established by the design alone.

Delivery requirement: after development and validation, document Uncertain in
README as an opt-in compatibility feature. Show ordinary ethclient-shaped use,
WithUncertain context capture, field/raw-response access after success or
failure, fresh collectors per invocation and the lack of reliability guarantee.
Use examples that compile against the actual API; do not advertise the feature
as implemented before then. Test concurrency, stage separation, failure paths,
post-return stability and ordinary-call compatibility before that documentation.

## 0035. Pending reads retain method-specific local semantics

Status: Accepted direction (2026-09-07); per-method support and source selection pending.

Support pending reads method by method, preserving the requested pending state
or pending block meaning. Supported reads may return node-local observations
through the compatible typed entry point; agreement never promotes them to
Corroborated. Unsupported method/tag combinations return an explicit error instead
of reading a mined block. Blanket rejection of all typed pending reads is not
the selected target behavior.

This route is selected by the request's semantics before execution. A failed
quorum read must never fall back to pending or a node-local read. This does not
change configured membership or enable automatic single-source mode changes.
Applicable local admission checks still apply; mined-block commitments must
not be imposed on pending shapes without a method-specific basis.

The exact supported-method matrix, source-selection rule, visible result
classification and unsupported-error contract remain to be specified and
tested in the relevant method tasks. Preserve existing Geth method signatures;
this decision does not require adding every Pending* convenience method.

## 0036. Library-owned tag-aware references replace custom resolvers

Status: Implemented (2026-09-20; `WithAnchorResolver` returns a migration error; strict-head and finalized anchors are library-owned behind `anchor.go`).

The project is not deployed. Fully migrate the old AnchorResolver and
WithAnchorResolver extension surface instead of retaining a compatibility shim
or silently ignoring an injected resolver. Update affected repository callers,
tests and examples as part of the implementation. External custom resolvers are
deferred until most of the planned work is complete; reopening that extension
requires a separate review and is not a prerequisite for current delivery.

The library owns reference establishment. Carry the requested latest, safe or
finalized tag explicitly through source observations, candidate selection and
confirmation. Use the invocation's fixed membership, mode and threshold. A
reference retains its height, hash, requested tag and corresponding evidence;
merely confirming that a block exists does not establish its safety level.
Apply the accepted candidate and confirmation rules without switching tags or
reducing the threshold. The replaceable adjudication module remains in scope.

Explicit heights and hashes preserve the requested target, earliest identifies
genesis, and pending follows 0035 rather than becoming a mined reference.
Reference establishment and full business-content validation remain distinct.
No new public method signature or tag convenience API is selected here; keep
the ethclient-compatible surface and use each method's legal wire form.

Product feature: tag-preserving multi-source reference establishment with
invocation-local pinning. Ethereum RPC already defines explicit block tags
(see EIP-1898); EthGuard adds multi-source corroboration and stable reference
selection under those meanings. Do not claim that explicit tags are absent
from Ethereum, that this mechanism is required by the RPC standard, or that
source-reported finality is independent Ethereum consensus finality.

After implementation and validation, document the feature in README with
working examples, freshness tradeoffs, insufficient-support failures and the
pending boundary. Until then, describe it only as an accepted design.


## 0037. Preserve Geth block-tag mapping and reject enabled legacy caching

Status: Implemented (2026-09-20; `ethadapter` maps -1..-5 to pending/latest/finalized/safe/earliest per Geth; positive cache TTLs fail at construction).

Use the fixed Geth baseline's negative block-number mapping: -1 pending,
-2 latest, -3 finalized, -4 safe and -5 earliest. Preserve each tag's meaning;
pending follows 0035 and earliest identifies genesis. Unknown negative values,
including out-of-range negatives, return an explicit invalid-request error
instead of selecting another block. Apply the contract across compatible typed
entry points and shared parameter conversion; keep legal per-method wire forms.

No cross-invocation reference or business caching is enabled in this cycle.
For legacy WithAnchorCaching / anchor_cache_ttl inputs, a positive TTL returns
a configuration error at construction identifying the option/key and decision
0026 (with this migration rule). Omitted or zero values mean disabled; invalid
syntax, overflow and negative durations return configuration errors. Validate
both programmatic and file-based inputs; do not silently ignore an attempt to
enable caching through an overriding option. Connection reuse and invocation-
local reference reuse remain allowed. Future caching proposals require review.

Update repository examples, configuration validation and regression tests for
this behavior. This closes spec-baseline sections 6.2 and 6.4; per-method pending
support and other explicitly deferred work remain separate.


## 0038. Keep corroboration gates explicit instead of a general validity helper

Status: Implemented (2026-09-23; no new helper; `Decision.IsCorroborated` stays a corroborated-only predicate).

Do not add a general evidence-validity helper, and do not migrate the
internal status checks onto one. `rpc.Status` stays `{Unknown, Corroborated,
Selected, Forwarded}`. `failed` remains an `observe.DecisionEvent` string,
not a confidence value.

Inventory of validity judgments:

- `Decision.IsCorroborated(err)` (`rpc/types.go`) is the only helper. It is
  true only for `err == nil && Status == Corroborated`. Production code does
  not call it. Tests do: `aggregator/aggregator_test.go` and
  `rpc/decision_test.go`.
- Corroboration gates, all of the form `err == nil && status != Corroborated`,
  then force `Unknown` and `ErrConflict` only on that branch:
  `anchorHeadPolicy.requireCorroborated`, `anchorHashOf`, `anchorHeightOf`,
  `Client.AnchorBlockHashForTag`, the typed-read gate in
  `Aggregator.Aggregate`, `reference.Finalized`'s support check, and
  `customPolicy.Resolve`. These ask "is this corroborated enough to
  proceed?", not "is any successful class valid?".
- Event-layer string comparison: `observe.Log.RecordDecision` checks
  `Status == "failed"`. `Aggregator.recordDecision` and
  `reference.Finalized` write that string when the call errors and otherwise
  copy `Status.String()`. That is the dual model, not an `rpc.Status` check.
- Different enum: `WriteResult.Accepted` and the hash check in
  `SendRawTransaction` compare `rpc.WriteAccepted`. That is the write
  decision, not `rpc.Status`.
- Producers that assign a status (`unknownDecision`, strict, median,
  adjudicate, forwarded overwrite) are not judgments of a returned result.
- `cmd/soak` reports `Status.String()` as an outcome label. It does not
  decide validity.
- Test assertions that compare `Status` directly are checks on fixtures,
  not call-site policy.

Bare comparison is enough when the caller is assigning a class, or already
knows `err == nil` and is distinguishing success classes, or is a
corroboration gate that must keep the `err == nil` split visible.
`IsCorroborated(err)` is required when the caller holds both the decision
and the error and wants the corroborated claim without a prior error check:
`Unknown` is the zero value, so `Status` alone can be misread. Production
gates do not use it, because `!IsCorroborated(err)` is also true for an
existing non-conflict error and would rewrite that error.

Rejected: add a structured helper (for example `Decision.Valid(err)`:
non-nil error invalid, `Corroborated` / `Selected` / `Forwarded` valid,
`Unknown` with a nil error invalid) and migrate every internal judgment to
it. That helper is a different predicate from the corroboration gates.
Substituting it would accept `Selected` where those gates must fail closed,
or, if the gates were rewritten as `!IsCorroborated`, would swallow an
existing error into `ErrConflict`. A helper with no remaining call site
would only add a second way to ask a question the gates must not ask.

## 0039. Treat partial information absence as abstention rather than conflict

Status: Accepted (2026-09-24). Rule 4e option (i) was frozen on
2026-09-25 as the Decision.Value invariant: a method-declared projection may
delete uncorroborated fields only, and may not add, replace, or splice fields
across sources. The first declaration is `eth_call` revert `data`, implemented
with dimension evidence on 2026-09-25. Other methods remain single-key until
their own declaration. The freeze is recorded in
docs/design/method-verification-pipeline.md section 1.1 and
docs/design/architecture-record.md.

A source that omits a method-declared abstainable sub-dimension of an answer,
or returns JSON `null` for it, abstains on that sub-dimension. It does not
assert that the value is absent, empty or different from what another source
supplied. The first such field is `data` on an `eth_call` revert.

This refines 0023. Missing and null are still not merged into empty bytes;
they are classified as non-assertions instead of values. For the fields it
covers, the rules below replace 0023's "no missing-data fallback is accepted
here". 0017's behavior-level rule is unchanged: a split between results and
reverts is conflict.

Reason:

T04 split `eth_call` revert keys into `reverted|<state>` (`missing`, `null`,
`empty`, `hex:<val>`, `raw:<json>`). The committed capture
(`internal/rpcsample/testdata/revert-data/2026-09-23`) sends one request four times to two
anonymized mainnet endpoints: a state-overridden contract whose code is
`revert(0, 0)`. s1 omitted `data` all four times. s2 returned `"0x"` three
times and omitted `data` once. The shape is not stable even within one
endpoint, so it reflects the serving backend's encoding, not a different
execution outcome. The capture is anonymized and does not attribute either
shape to a named provider or client. As distinct keys, any fleet in which
neither cluster meets quorum on its own, typically two sources, reports
`ErrConflict` for an empty revert.

Classification. It applies to every method. Only class 4 is new.

1. Representational variance is normalized silently: hex letter case, JSON
   key order, whitespace, and leading zeros in QUANTITY values. Leading zeros
   in DATA values are content: `0x00` is not `0x`.
2. Out-of-scope fields do not enter the comparison key: provider extensions,
   and envelope parts outside the key such as revert `code` and `message`.
   They stay on the winning payload as uncorroborated context and remain
   available through Uncertain (0024, 0034).
3. Inadmissible answers are recorded as faults and contribute no support:
   values with no canonical form, missing required fields, and failed request
   or content binding. Their sources stay in the voting denominator (0020,
   0025).
4. Abstention applies only to fields a method contract explicitly declares
   abstainable. An undeclared missing field is class 3 when required and
   class 2 when outside the key. Being protocol-optional does not by itself
   qualify a field (0024).
   a. The answer is admitted and counts toward the dimensions it does assert.
      For `eth_call`, that is the fact that execution reverted.
   b. On the abstained sub-dimension it neither supports nor rejects any
      value, and it stays in the denominator.
   c. Concrete values on that sub-dimension are adjudicated among themselves
      under the invocation's corroboration mode, floor and denominator, like
      any other key. Abstainers never count toward that quorum. An abstaining
      answer is never completed with another source's value (0024).
   d. If every admitted answer abstains, the sub-dimension is absent from the
      result and the asserted dimensions alone decide the status. For
      `eth_call`, that is a corroborated revert with no data
      (`RevertError.Data` nil).
   e. If concrete values exist but the agreeing cluster falls short of quorum,
      the result keeps the corroborated asserted dimensions and withholds the
      uncorroborated sub-dimension from the result value. For `eth_call`, the
      typed boundary therefore returns the corroborated revert behavior with
      `RevertError.Data` unset; the concrete bytes remain available only from
      the invocation's Uncertain or audit evidence. This is a method-declared
      redaction-only projection: it may remove an uncorroborated field, but it
      may not add, replace or splice fields from another source. This revises
      the earlier architecture rule that Decision.Value must be a complete
      admitted source response; methods that do not declare the projection
      still return that source value. The status is
      honest about the dimensions it covers; dimension evidence records which
      sources supported, rejected or abstained on each dimension. A plain
      downgrade to `Selected` is not used because the typed-read gate would
      still turn it into `ErrConflict` (0038).
   f. In unanimity mode, abstention counts as neither agreement nor
      disagreement.
5. Asserted conflict, meaning admitted answers carrying different canonical
   values inside a required comparison dimension, is adjudicated as today. A
   cluster short of quorum, or any split in unanimity mode, is `ErrConflict`
   with Support and Reject preserved. For an abstainable sub-dimension, rule
   4e applies instead: no short concrete cluster is returned as a value.
   Different envelope shapes (hex versus structured data) remain different
   values until a sample-backed envelope-equivalence rule is accepted (0023).
   A conflict says the sources disagree. It does not say which one is right.

Examples, majority mode, floor 2:

- N=3: `0x`, `0x`, missing is a corroborated revert with empty data. The
  abstainer is not in Reject.
- N=3: `0xdead`, `0xbeef`, missing is a corroborated revert with data
  withheld; neither concrete byte string reaches the data quorum.
- N=3: missing, missing, missing is a corroborated revert with no data.
- N=3: missing, missing, `0xdead` is a corroborated revert with data withheld;
  `0xdead` is retained as an uncorroborated data assertion.
- N=2: `0x`, missing is a corroborated revert with data withheld.
- N=4: `0x`, `0x`, missing, missing is a corroborated revert with data
  withheld (Q=3).

Consequences:

- A provider that strips revert data can no longer create dissent against
  sources that return bytes. It also never vouches for bytes.
- When an upstream spec adds an optional field, a method contract may declare
  it abstainable, so older clients that omit it do not generate conflicts.
  This is a per-method declaration, not automatic.
- Multi-dimensional keys need a method-specific resolver behind the existing
  Policy seam; the generic Strict adjudicator keeps its single-key semantics.
  Evidence must let a caller tell which sources vouched for each returned
  dimension and which sources abstained.

## 0040. Keep method verification executable; do not add a contract DSL

Status: Accepted (2026-09-24); the standard and roadmap are recorded in
`docs/design/method-verification-pipeline.md` and
`docs/design/project-roadmap.md`.

Policy decorator composition and the tests that exercise it remain the
executable source of method behavior. The moving-tag matrix is a human-facing
cross-check that tests interlock with the registered method set. Do not add a
declarative `MethodContract` structure, code generation, or a universal
`suite.RunAll` runner: the registered methods deliberately differ in
anchoring, pending routing, admission, normalization, resolution and typed
projection, so a shared schema would become a second method language.

Sample replay and `expect.json` are versioned test data, not an executable
language. They may describe requests, source responses, corroboration
configuration, dimension evidence and expected outcomes, but contain no
conditionals, inheritance or generated test logic.

The static interlock is intentionally narrow. CI may parse the matrix's
`Tests` column and check exact `func Test*` names. It does not interpret
free-form prose, `Prefix*` wildcards or `as above` aliases. Repetition in the
matrix is preferred to maintaining a Markdown grammar. References in the
method contracts and roadmap are explanatory links, not a second test index.


## 0041. Canonical quantity and successful DATA results

Status: Accepted (2026-09-27). Quantities and successful DATA only.
`rpc/result.go` and `rpc/internal/wire/encode.go` hold the code. The method
rule index records `result=canonical` or `result=raw`. That column does not
explain fields and does not generate tests (0040).

Migrated scalars write a canonical Value. For those scalars the comparison
key is the canonical JSON bytes. EncodeCheck, the comparison, and
EncodeCommit see the same bytes. EncodeCommit writes that cluster key. It
does not rebuild Value by deserializing a decimal key. Canonical QUANTITY is
a JSON string: `0x` plus lowercase hex, no leading zeros, zero written `0x0`.
Canonical DATA is a JSON string: `0x` plus lowercase hex whose length matches
the bytes. Leading zeros in DATA are content and stay. `net_version` is a
decimal JSON string with no leading zeros, not `0x`. A successful `eth_call`
execution key is that canonical DATA string. The `ok|` prefix is not part of
the key or the Value. A revert keeps the `reverted|...` key, does not enter
the encoder, and its Value stays the representative source's original payload
aside from a 4e deletion. `NormalizeWireQuantity` still returns a decimal
string. That decimal is not the cluster key and is not the canonical bytes.

The order inside the existing resolver is one pass. EncodeCheck runs on each
source first. A failure is a Fault, the source stays in the denominator, and
it does not vote. Resolve then runs once on the sources that passed. EncodeCommit
writes the canonical bytes when every supporter in the winning cluster encoded
the same bytes. If those bytes differ, the result is Unknown,
`ErrMalformedResponse`, and the reason contains `result encoding disagrees
within cluster`. Do not select another source's original payload and do not
vote again.

Wire quantities that already reject leading zeros still reject them. Do not
loosen that check. `net_version` and `net_peerCount` still fold leading zeros
into one key, then write the canonical form.

Median quantities stay Selected. Median selection itself is unchanged: a
sample that cannot be encoded does not enter the median, then the existing
median is taken once. Local `net_peerCount` encodes only the selected source
and stays Selected. `ResolveLocal` leaves Representative at its zero value.
Unmigrated methods, including a block `number|hash` key, keep the first
captured supporter's original payload. Representative for Strict, CallRevert,
and Median is still that supporter.

Not in this decision: fee history, receipts, transactions, logs, access lists,
block re-encoding, `eth_call` code and message, moving `from` or `size` out of
Value, Provenance, an Extensions diff, shrinking the denominator, lowering the
floor, promoting Median or Local to Corroborated, a second field table, or an
encoder registry.


## 0042. Canonical eth_feeHistory

Status: Accepted (2026-09-27). `eth_feeHistory` only. Decision 0041 is not
amended and does not cover this method. Receipts, transactions, logs, access
lists, block re-encoding, and the `eth_call` envelope stay out.

The comparison key stays the method's existing feeHistory key. It is not the
canonical JSON of the whole result. A migrated scalar still clusters on its
canonical JSON bytes (0041); `eth_feeHistory` does not take that branch.
`gasUsedRatio` inside the key is the canonical decimal. It is no longer a
`[]float64`, which folded distinct decimal originals into one key.

The decimal rule reads the original text with `json.Number` and writes
`strconv.FormatFloat(f, 'f', -1, 64)`. `0.50`, `0.5000`, and `0.5` share one
key, and the Value writes `0.5`. `0.10` and `0.1`, `1.0` and `1`, and `1e-1`
and `0.1` do the same. `0.30000000000000001` has shortest form `0.3`, but the
decimal original changed, so EncodeCheck fails, the source is a Fault, it
stays in the denominator, and it does not vote. It is not another spelling of
`0.3`. A ratio outside `[0,1]`, and NaN or Inf, stay Admit failures. They are
not a new encoding class.

The protected fields are the four already in that key: `oldestBlock`,
`baseFeePerGas`, `gasUsedRatio`, and `reward`. That is the first step of
section 4.2 in `docs/design/return-value-design.md`. A field outside the key
does not enter Value. This decision does not build an Extensions diff and does
not build Provenance, so a field left outside the key has nowhere to go. That
gap stays open. It is not recorded as retained.

Quantities keep the existing QUANTITY rule. A spelling that already rejects a
leading zero still rejects it. The key may keep today's decimal projection of
a quantity. Inside Value, `oldestBlock`, `baseFeePerGas`, and `reward` are
canonical QUANTITY: `0x` plus lowercase hex, no leading zeros, zero written
`0x0`. The object uses the encoder struct's field order, compact JSON, and
`SetEscapeHTML(false)`. A missing `reward` is omitted. It is not filled in as
null or as an empty array. Value comes from the encoder. It is not rebuilt by
deserializing the comparison key.

The order is the one already in the resolver. EncodeCheck runs on each source
first. A failure is a Fault, the source stays in the denominator, and it does
not vote. Resolve then runs once on the feeHistory keys of the sources that
passed. EncodeCommit writes the canonical object when every supporter in the
winning cluster encoded the same bytes. If those bytes differ, the result is
Unknown, `ErrMalformedResponse`, and the reason contains `result encoding
disagrees within cluster`. Do not select another source's original payload and
do not vote again.

The method-rule index records `result=canonical` for `eth_feeHistory` only.
Representative stays the first captured supporter and does not decide the
spelling. Pending `ResolveLocal` may encode the selected source.
Representative stays at its zero value there, and the status stays Selected.
Median and Local are not promoted to Corroborated. The denominator is not
shrunk and the floor is not lowered. There is no encoder registry. The
encoder lives in `rpc/internal/objects/feehistory/encode.go`.


## 0043. Canonical eth_getLogs

Status: Accepted (2026-09-27). `eth_getLogs` only. Decisions 0041 and 0042
are not amended and do not cover this method. Receipts, transactions, block
receipts, access lists, block re-encoding, and the `eth_call` envelope stay
out.

The comparison key stays the method's existing `normalizeLogs` / `PruneMethod`
result. It is not the canonical JSON of the whole result. A migrated scalar
still clusters on its canonical JSON bytes (0041). `eth_feeHistory` still
clusters on its feeHistory key (0042). `eth_getLogs` does not take either
branch.

The protected fields are only the log fields the existing `eth_getLogs` mask
retains: `address`, `topics`, `data`, `blockNumber`, `transactionHash`,
`transactionIndex`, `blockHash`, `logIndex`, `removed`, and `blockTimestamp`
when it is present. That is the first step of section 4.2 in
`docs/design/return-value-design.md`. A field the mask drops does not enter
Value. This decision does not copy `projection.LogsStandardFields` and does
not add a second field table. It does not build an Extensions diff and does
not build Provenance, so a field left outside the key has nowhere to go. That
gap stays open. It is not recorded as retained.

`log.Validate` still requires the fields it requires today. A missing required
field is an EncodeCheck failure. `blockTimestamp` is omitted when absent. It
is not filled in as null. An empty array is still a legal result `[]`. JSON
null is still an illegal answer, not an empty array. Pending stays the
Prepare rejection, `ErrInvalidRequest`. Do not add a local observation and do
not encode pending.

Address and hashes are lowercase. Quantities are canonical QUANTITY: `0x`
plus lowercase hex, no leading zeros, zero written `0x0`. `data` is canonical
DATA and leading zeros stay. `removed` is a JSON boolean. Array order is the
response order and is not sorted. The object uses the encoder struct's field
order, compact JSON, and `SetEscapeHTML(false)`. Value comes from the
encoder. It is not rebuilt by deserializing the comparison key, and it is not
rebuilt from `float64`.

The order is the one already in the resolver. EncodeCheck runs on each source
first. A failure is a Fault, the source stays in the denominator, and it does
not vote. Resolve then runs once on the existing log keys of the sources that
passed. EncodeCommit writes the canonical array when every supporter in the
winning cluster encoded the same bytes. If those bytes differ, the result is
Unknown, `ErrMalformedResponse`, and the reason contains `result encoding
disagrees within cluster`. Do not select another source's original payload and
do not vote again.

The method-rule index records `result=canonical` for `eth_getLogs` only.
Representative stays the first captured supporter and does not decide the
spelling. Scalar and `eth_feeHistory` cluster keys stay unchanged. Logs use
the existing `WithCanonicalObject`. There is no encoder registry. The encoder
lives in `rpc/internal/objects/log/encode.go`.


## 0044. Canonical eth_createAccessList

Status: Accepted (2026-09-28). `eth_createAccessList` only. Decisions 0041,
0042, and 0043 are not amended and do not cover this method. Receipts,
transactions, block receipts, block re-encoding, and the `eth_call` envelope
stay out.

The comparison key stays the method's existing `normalizeAccessList` /
`PruneMethod` result. It is not the canonical JSON of the whole result. A
migrated scalar still clusters on its canonical JSON bytes (0041).
`eth_feeHistory` still clusters on its feeHistory key (0042). `eth_getLogs`
still clusters on its existing log key (0043). `eth_createAccessList` does
not take any of those branches.

The protected fields are only the fields the existing mask retains:
`accessList`, `error`, and `gasUsed`. Each access-list item keeps only
`address` and `storageKeys`. That is the first step of section 4.2 in
`docs/design/return-value-design.md`. A field the mask drops does not enter
Value. This decision does not add a second field table. It does not build an
Extensions diff and does not build Provenance, so a field left outside the
key has nowhere to go. That gap stays open. It is not recorded as retained.

A missing `address` or `storageKeys` is an EncodeCheck failure. The source is
a Fault, it stays in the denominator, and it does not vote. `error` and
`gasUsed` are omitted when absent. They are not filled in as null. An empty
`accessList` stays `[]`. An empty `storageKeys` stays `[]`. Neither empty
array is omitted.

Address and storage keys are lowercase. `gasUsed` is canonical QUANTITY:
`0x` plus lowercase hex, no leading zeros, zero written `0x0`. A spelling
that already rejects a leading zero still rejects it. `error` is a JSON
string and stays as written. Array order is the response order and is not
sorted. The object uses the encoder struct's field order, compact JSON, and
`SetEscapeHTML(false)`. Value comes from the encoder. It is not rebuilt by
deserializing the comparison key, and it is not rebuilt from `float64`.

The order is the one already in the resolver. EncodeCheck runs on each source
first. A failure is a Fault, the source stays in the denominator, and it does
not vote. Resolve then runs once on the existing access-list keys of the
sources that passed. EncodeCommit writes the canonical object when every
supporter in the winning cluster encoded the same bytes. If those bytes
differ, the result is Unknown, `ErrMalformedResponse`, and the reason
contains `result encoding disagrees within cluster`. Do not select another
source's original payload and do not vote again.

The method-rule index records `result=canonical` for `eth_createAccessList`
only. Representative stays the first captured supporter and does not decide
the spelling. Pending stays a node-local observation. It may encode the
selected source. Representative stays at its zero value there, and the status
stays Selected. Median and Local are not promoted to Corroborated. The
denominator is not shrunk and the floor is not lowered. Scalar, feeHistory,
and log cluster keys stay unchanged. This method uses the existing
`WithCanonicalObject`. There is no encoder registry. The encoder lives in
`rpc/internal/objects/accesslist/encode.go`.

