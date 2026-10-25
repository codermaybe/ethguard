// Package ethguard is a corroborating, security-focused wrapper around go-ethereum's
// ethclient. It aggregates multiple RPC endpoints so applications can import a
// single client instead of running a standalone RPC proxy.
//
// Typical layout of this module:
//
//	ethguard             — public Client facade (this package): client.go,
//	                       call.go, and eth.go form the root facade; it holds
//	                       no concrete wiring
//	github.com/codermaybe/ethguard/internal/runtime  — sole Composition Root binding
//	                       methods, projectors, aggregator, and seams
//	github.com/codermaybe/ethguard/internal/typed    — sole production typed
//	                       projection shared by the root facade and ethadapter
//	github.com/codermaybe/ethguard/internal/reference — reference algorithm and
//	                       the Head-Reading Port it consumes
//	github.com/codermaybe/ethguard/internal/verification — cross-module
//	                       verification through public seams (not imported by
//	                       production code)
//	github.com/codermaybe/ethguard/config      — configuration types, samples, and loaders
//	github.com/codermaybe/ethguard/pool        — multi-source RPC upstream management
//	                       and the minimal Transport Port
//	github.com/codermaybe/ethguard/observe     — observability seam: one decision event per
//	                       aggregate, log adapter, Prometheus adapter under
//	                       observe/prometheus
//	github.com/codermaybe/ethguard/aggregator  — prepare → collect → resolve orchestration
//	github.com/codermaybe/ethguard/rpc         — generic request, decision, policy,
//	                       and evidence model shared across modules
//	github.com/codermaybe/ethguard/rpc/methods — one policy file per corroborated method
//	github.com/codermaybe/ethguard/ethadapter  — ethclient-shaped surface over the corroborated subset
//	github.com/codermaybe/ethguard/rpctest     — scriptable in-process Ethereum nodes for tests
package ethguard
