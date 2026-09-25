# Anchor Transport Seam

状态：Accepted，2026-09-25

HTTP 与 WSS 的参考建立共用一条生产路径。本说明只记录这个 seam 的位置、调用链和不变量。它不新增 resolver，也不记录任务步骤。

## 位置

seam 在「发出单次 reference RPC」的地方，就是 `pool` 的 endpoint 调用。resolver 不读取 URL scheme，也不持有 HTTP response 或 WebSocket frame。

```text
StrictHeadAnchor / FinalizedHeadAnchor / reference.Custom
        |
        | method + params，tag 原样保留
        v
aggregator 或 reference.*
        |
        v
pool.Collect / CollectVoting / CallSource
        |
        v
Source.clientForCall
        |
        v
standardDialer.DialContext -> ethclient.DialContext
        |                         |
        HTTP (http, https)        WSS (ws, wss)
        |                         |
        v                         v
pool.Emission { Val json.RawMessage, Err error }
```

`ethclient.DialContext` 按 scheme 选择 geth 的 HTTP 或 WebSocket 传输。这就是两个 adapter。它们返回同一种 `pool.Emission`。quorum、参考选择、状态、`ReferenceEvidence` 和 `ObservedAt` 都在 emission 之后，由现有 resolver 完成。

没有再包一层接口：`Pool` 已经是这个边界。再包一次只会转发调用，不会改变 observation 的形状。

## 生产调用链

1. `Client.New` 用 `pool.NewPoolWithOptions` 创建 pool，并对每个 upstream `Join`。非零 `anchor_cache_ttl` 直接失败，`WithAnchorResolver` 已退役。默认 resolver 是 `newStrictHeadAnchor(pool, ...)`。`WithFinalizedAnchoring` 换成 `NewFinalizedHeadAnchor`，仍然拿同一个 pool。
2. `normalizeEndpointID` 接受 `http`、`https`、`ws`、`wss`，只用来识别端点。它不参与裁决。
3. 第一次调用时 `Source.dial` 经 `standardDialer` 拨号。同一个 `*ethclient.Client` 之后被复用。复用的是连接，不是 reference。
4. `collectOne` 调用 `client.Client().CallContext`。JSON-RPC error 和 transport error 都作为 `Emission.Err` 返回。
5. `StrictHeadAnchor` 以方法名 `anchor`、参数 `[tag, false]` 调用 `aggregator.Aggregate`。`anchorHeadPolicy.Prepare` 把方法改成 `eth_getBlockByNumber`，不改 tag。
6. `latest` 走 `AnchorReference`，origin 是 `strict-head`。`safe` 和 `finalized` 走 `AnchorReferenceForTag`，origin 是 `tagged-head`。`FinalizedHeadAnchor` 和 `reference.Custom` 也经同一个 pool 发请求，不另拨号。
7. 同一次 `Decision.Value` 由 `blockRefFromDecision` 解出 hash 和 height。`anchorHashOf`、`anchorHeightOf` 和 `Client.AnchorBlockHashForTag` 都进入 `resolveReference`。
8. `ReferenceEvidence` 和 `ObservedAt` 在 `AnchorReference` / `AnchorReferenceForTag` 填写，失败时也填写。`anchorHashOf` / `anchorHeightOf` 先发布 evidence，再把 anchor 错误交回 Prepare。aggregator 因此不 fan-out 业务方法。

## 不变量

- `latest`、`safe`、`finalized` 原样进入参考请求。WSS 不把 `safe` 或 `finalized` 降成 `latest`。
- 状态读取和 EIP-1898 继续使用 hash。`eth_getLogs` 与 `eth_feeHistory` 继续使用 height。两者来自同一次 reference establishment，不是两次独立裁决。
- transport/dial 失败、JSON-RPC error、无法归一的 reference head、quorum/conflict、取消和超时都停在 Prepare。业务请求零次。`Decision.Status` 与 evidence status 为 `Unknown`。Faults 保留。`ObservedAt` 按这次建立填写。失败不改写成成功，也不改写成另一种业务冲突。
- 空 head 的现有失败类是 `ErrNoValidResponse`（normalize fault，`missing number or hash`），不是 `ErrMalformedResponse`。两种 transport 都保持这个分类。
- 每次调用重新建立 reference。没有跨调用缓存或 TTL，不重新接入 `CachedAnchor`。WSS 长连接不复用上一次 reference。
- quorum、floor 和冲突分类只看 corroboration snapshot。不按 transport 选择规则，也不增加 transport 专属方法契约。

## 明确不在这个 seam 里

adapter 不做 quorum、参考选择、状态裁决、缓存或 evidence 组装。这里也没有第二套 resolver、第二套 evidence 类型或第二套 quorum。
