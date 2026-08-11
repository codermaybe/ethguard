// Package pool implements multi-source Ethereum RPC management for EthGuard.
//
// Responsibilities:
//   - Pool: shared, concurrent-safe management of RPC sources
//   - Source: one long-lived ethclient wrapper per endpoint
//   - HealthSnapshot: read-only latency and availability observations
//
// Application code should prefer package ethguard's Client. Import pool
// directly only when integrating advanced lifecycle or health diagnostics.
package pool
