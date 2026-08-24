// Package ethguard is a trusted, security-focused wrapper around go-ethereum's
// ethclient. It aggregates multiple RPC endpoints so applications can import a
// single client instead of running a standalone RPC proxy.
//
// Typical layout of this module:
//
//	ethguard             — public Client API (this package): client.go,
//	                       call.go, and eth.go form a stable facade
//	ethguard/config      — configuration types, samples, and loaders
//	ethguard/pool        — multi-source RPC upstream management
//	ethguard/rpc         — transport model shared across modules
//	ethguard/rpc/methods — one policy file per verified method
package ethguard
