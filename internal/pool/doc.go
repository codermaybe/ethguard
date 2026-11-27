// Package pool implements multi-source Ethereum RPC management for EthGuard.
//
// Responsibilities:
//   - Pool: shared, concurrent-safe management of RPC sources
//   - Source: one long-lived Transport Adapter per endpoint
//   - HealthSnapshot: read-only latency and availability observations
//
// internal/runtime assembles the pool and its transport adapters. The root
// Client owns the public lifecycle and health diagnostics.
package pool
