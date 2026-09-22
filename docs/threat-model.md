# Threat Model

Status: Draft

EthGuard is designed for cautious data selection in the presence of unreliable
or dishonest RPC providers. It is not a proof system and does not make a
majority of endpoints independent or honest.

## Trust assumptions

- A provider can be unavailable, stale, misconfigured, buggy, or malicious.
- Multiple provider URLs can share the same infrastructure or upstream data.
- Agreement is evidence about observed consistency, not proof of truth.
- Ethereum consensus and cryptographic verification remain the ultimate source
  of truth where the client can validate it.

## Configuration and runtime control boundary

Accepted boundary (2026-09-07; decisions 0027 and 0030): authorized configuration
and explicit membership/mode operations are trusted control inputs. Upstream
RPC responses are untrusted data inputs. The caller owns source admission and
protects configuration files, management interfaces, process restart and the
execution environment through its deployment controls.

EthGuard must validate configuration consistency, execute the selected mode
faithfully and expose its actual guarantees. Upstream responses, failures or
health changes must not automatically lower thresholds, remove voting members
or switch a corroborated invocation into single-source forwarding. Runtime
mode/member changes apply to subsequent invocations, not an in-flight snapshot.

If an attacker can alter configuration and restart the application, EthGuard
cannot distinguish the resulting single-source configuration from one supplied
intentionally by the caller. Preventing or attributing that compromise belongs
to the deployment environment, not RPC response adjudication. This boundary
does not excuse accepting upstream data as control instructions or failing to
validate library configuration.

Record the selected mode, voting-member count and effective support requirement
at startup for operational visibility; this does not replace configuration
protection. A single-source forwarding mode has no corroboration threshold and
must not imply Corroborated. These mode/reporting requirements are accepted design
boundaries, with implementation alignment pending.

## Failure classes

- transport failure: timeout, disconnect, or rate limiting;
- protocol failure: malformed JSON-RPC or invalid Ethereum encoding;
- freshness failure: a source is behind the selected chain state;
- semantic conflict: deterministic responses disagree after normalization;
- local variance: the method describes a provider-local or moving state;
- correlated failure: several sources share the same wrong observation.

## Safety rules

- Validate chain identity before admitting a source to aggregation.
- Never label fallback forwarding as corroborated.
- Do not punish a source for disagreement unless the comparison was anchored
  and high-confidence.
- In corroborated modes, prefer explicit conflict errors to silently returning
  an unverified value. Intentional single-source mode remains Forwarded, with
  applicable method validation and no cross-source guarantee.
- Keep enough evidence to explain why a result was selected.
- Eligibility gates driven by provider-reported data (block tips) must use
  collusion-resistant references such as medians, so one lying source cannot
  vote honest sources out of the fan-out.

## Non-goals

- proving that an RPC provider is honest;
- replacing Ethereum consensus validation;
- protecting or authenticating a compromised deployment configuration, process
  or runtime management surface;
- treating every JSON-RPC method as aggregatable;
- guaranteeing availability when all sources fail or disagree.
- optimizing gas or choosing transaction fee parameters;
- allocating, caching, or coordinating account nonces;
- signing, broadcasting, retrying, or replacing transactions.
