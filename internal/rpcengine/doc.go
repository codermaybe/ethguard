// Package rpcengine owns read plans, invocation snapshots, response resolution,
// field aggregation, and write adjudication. Its resolvers consume rpc data
// contracts and return outcomes; they own no connections or Ethereum field rules.
// The aggregator coordinates collection and methods supply method semantics.
package rpcengine
