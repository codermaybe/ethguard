// Package ethguard is a trusted, security-focused wrapper around go-ethereum's
// ethclient. It aggregates multiple RPC endpoints so applications can import a
// single client instead of running a standalone RPC proxy.
//
// Typical layout of this module:
//
//	ethguard          — public Client API (this package)
//	ethguard/config   — configuration types, samples, and loaders
//	ethguard/pool     — multi-source RPC upstream management
package ethguard
