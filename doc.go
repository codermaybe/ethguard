// Package ethguard is a corroborating, security-focused wrapper around go-ethereum's
// ethclient. It aggregates multiple RPC endpoints so applications can import a
// single client instead of running a standalone RPC proxy.
//
// Typical layout of this module:
//
//	ethguard             — public Client API (this package): client.go,
//	                       call.go, and eth.go form a stable facade
//	github.com/codermaybe/ethguard/config      — configuration types, samples, and loaders
//	github.com/codermaybe/ethguard/pool        — multi-source RPC upstream management
//	github.com/codermaybe/ethguard/observe     — observability seam: one decision event per
//	                       aggregate, log adapter, Prometheus adapter under
//	                       observe/prometheus
//	github.com/codermaybe/ethguard/aggregator  — prepare → collect → resolve orchestration
//	github.com/codermaybe/ethguard/rpc         — transport model shared across modules
//	github.com/codermaybe/ethguard/rpc/methods — one policy file per corroborated method
//	github.com/codermaybe/ethguard/ethadapter  — ethclient-shaped surface over the corroborated subset
//	github.com/codermaybe/ethguard/rpctest     — scriptable in-process Ethereum nodes for tests
package ethguard
