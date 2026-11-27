// Package ethapi contains Ethereum JSON-RPC business values generated from
// the pinned execution-apis schemas plus explicitly reviewed standard errata.
// Types are owned here, independently of
// transport, quorum policy, and go-ethereum business structs. New constructors
// enforce the applicable schema; callers constructing values directly must
// preserve the same invariants.
package ethapi
