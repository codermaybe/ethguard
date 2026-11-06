# T0 transport fixture and method matrix

This is the pre implementation survey for the six representative typed reads. It
records the smallest fixture set that can exercise native HTTP, raw WSS, and the
go-ethereum compatibility adapter without changing a public API or adding
subscriptions.

## Existing fixtures

* `rpctest.Node` (`rpctest/node.go`) is the reusable production-path HTTP
  server. `NewNode(chainID)`, `Return/ReturnRaw`, `Fail/FailWithData`, `Hang`,
  and `Calls()` cover quorum, malformed data, and parameter assertions. Existing
  typed tests (`typed_state_wire_test.go`, `typed_reads_test.go`,
  `typed_block_headers_test.go`, `typed_receipt_canonical_test.go`) use this
  fixture with a three-node fleet.
* `internal/transport/http/http_test.go`'s `nativeServer` and
  `differential_test.go` capture exact HTTP request envelopes. The differential
  fixture dials native HTTP and `internal/transport/geth.StandardDialer` to the
  same server, and already fixes JSON-RPC envelope parity plus documented
  response-validation differences.
* `internal/transport/wss/wss_contract_test.go`'s `scriptedPeer` is the minimal
  raw WebSocket peer. It performs the owned RFC6455 handshake, records text
  frames, and responds by echoing request IDs. `wss.go` uses the same
  `rpcwire.EncodeRequest` and response codec as HTTP; there is one persistent
  socket, request-ID demultiplexing, and no batch/reconnect/subscription path.
* `gethcompat/gethcompat.go` injects `geth.StandardDialer` into the normal pool.
  `gethcompat/gethcompat_test.go` verifies the explicit construction seam;
  method-level wire comparison belongs in the existing HTTP differential
  fixture, since the compatibility adapter is HTTP-only.

## Minimal method matrix

The business request is identical across transports at the JSON-RPC layer. HTTP
serializes one POST body with `Content-Type` and `Accept: application/json`;
WSS serializes the same bytes as one text frame after the Upgrade handshake.
Native HTTP and geth HTTP therefore have identical request bytes for this
matrix. Their intentional response difference remains: native validates JSON-RPC
IDs and rejects malformed/mutually-exclusive envelopes; geth's single-call HTTP
path matches by position and accepts absent or mismatched IDs. No typed method
should depend on this quirk.

| Method | Representative call and wire params | Reusable fixture | Gap / estimate |
| --- | --- | --- | --- |
| `ChainID` (`eth_chainId`) | no params (`{method,id}`; an explicitly empty slice is `params:[]`) | `rpctest.Node` default chain ID; `differential_test.go` envelope case | Add one cross-transport smoke assertion of no-params shape and result; **S** |
| `BalanceAt` (`eth_getBalance`) | `[address, blockRef]`; moving `nil` first anchors with `eth_getBlockByNumber`, then sends anchor hash; pending sends `"pending"`; hash pin may send EIP-1898 object | `typed_state_wire_test.go` (`tagHead`, `businessCalls`, `wireString`/`wireObject`) | Repeat one pinned and one pending request through WSS peer; **S/M** (anchor helper) |
| `HeaderByNumber` (`eth_getBlockByNumber`) | `[blockRef, false]`; moving tags are anchored and rewritten to hash; pending is rejected locally | `typed_block_headers_test.go`, `typed_block_number_test.go` | WSS only needs frame/params assertion; no new header fixture; **S** |
| `TransactionReceipt` (`eth_getTransactionReceipt`) | `[txHash]`, immutable (no anchor) | `typed_reads_test.go` receipt fixture and `typed_receipt_canonical_test.go` raw receipt builders | Add native-vs-geth-vs-WSS raw result parity, including `null`/NotFound; **S** |
| `CallContract` (`eth_call`) | `[callObject, blockRef]`; object is go-ethereum call fields; moving `nil` anchors, pending is allowed for raw call; revert error data presence is significant | `typed_call_boundary_test.go`, `typed_call_revert_4e_test.go`, `typed_state_wire_test.go` `callMsgTo` | Verify exact object field omission and frame payload; preserve revert data states; **M** |
| `FeeHistory` (`eth_feeHistory`) | `[blockCount, lastBlockRef, rewardPercentiles]`; `nil` maps to `"latest"`; hash newest block is rejected locally | `typed_state_wire_test.go` local hash rejection; `rpc/methods/eth_feehistory_test.go` admission fixtures | Add one valid typed projection and one invalid-hash local rejection across native/WSS; **M** |

## Planned test shape

Keep the existing three-node `rpctest.Node` fleet for adjudication and typed
projection. Add a transport table test that runs each representative call once
against a native HTTP endpoint and once against a `scriptedPeer`, asserting the
method, parameter count/order, and raw result. Run the existing HTTP differential
cases unchanged for native/geth envelope and response behavior. Reuse the WSS
peer's request-ID echo helper; do not introduce a subscription or reconnect
fixture. The only expected WSS-specific assertions are Upgrade/frame lifecycle,
shared request IDs, and parity of JSON request bytes.

## Gaps and sequencing

`typed_transport_matrix_test.go` now drives one typed `Client` through native
HTTP and raw WSS together, and checks `ChainID` plus `HeaderByNumber` reach both
sources. This closes the first transport-to-execution seam without changing
method policy, anchoring, quorum, AtHash behavior, or adding subscriptions. The
remaining work is to extend the same harness to the other four representative
methods and add explicit gethcompat envelope assertions; estimate remains
medium because the fixture plumbing is reusable.

