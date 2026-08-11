# Metrics

This directory is reserved for passive observability.

The future metrics seam may receive request, upstream, and aggregation events
and expose in-memory or external adapters. Disabling metrics must not change
upstream eligibility, aggregation, or any other security decision.

Operational health used by the pool remains pool-owned state. Metrics receive
copies of observations; they are not the source of truth.

No metrics backend or Go interface is selected yet.
