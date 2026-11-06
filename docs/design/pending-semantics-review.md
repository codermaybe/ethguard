# Pending 语义事实卡片

状态：R0 输入，语义冻结建议（2026-11-07）。本文只记录当前执行计划已经能证明的
pending 事实，不新增 facade、结果类型或 provider 承诺。

## 已冻结的本地观察路径

`eth_getBalance`、`eth_getTransactionCount`、`eth_getStorageAt`、`eth_getCode` 和
`eth_call` 的 plan 在各自声明的参数位置使用 `PendingObserve`。`eth_createAccessList`、
`eth_feeHistory`、`eth_getBlockByNumber` 和 `eth_estimateGas` 也声明了同样的 pending
路由（logs 与 block receipts 是 `PendingRefuse`，不属于本节）。当请求在声明位置是
`"pending"`，或 EIP-1898 对象的 `blockNumber` 是 `pending` 时，执行器：

1. 不建立 moving-tag/reference anchor；
2. 原样保留 pending 参数和 `requireCanonical` 字段并向各配置 source 发请求；
3. 使用 `rpc.ResolveLocalObservation`，按捕获顺序选第一个可规范化的响应；
4. 将结果标为 `Selected`，Support 只包含被选 source，永不升级为 `Corroborated`；
5. 保留该 source 的 admission/evidence，Decision 的 `BlockRef` 保持为空。

因此多个 source 返回相同 pending 值仍然只是一个 source-local 观察。不同 source 的
pending 值也不是 quorum conflict；只要有一个可用且可规范化的响应，就选择捕获顺序中
第一个可用值。所有 source 都失败或都无法规范化时，Decision 为 `Unknown`，返回
`ErrNoValidResponse`（具体 transport/RPC 错误仍保留在 fault/material 记录中）。

这条路径只改变 pending 形状的裁决方式。相同方法的 mined、number、hash、latest、safe
和 finalized 形状继续走各自的 reference 与严格/中位数策略，不能因为 pending 结果是
`Selected` 而降低 mined 请求的 quorum。

## 证据和错误边界

- `Selected` 表示“某一配置 source 报告了自己的 pending 状态”，不表示 source 间
  一致，也不表示交易池或执行层的全局事实。
- 不应把 pending 的 disagreement 转写成 `ErrConflict`，也不应把一个 source 的
  response 标成 `Corroborated`。
- 本地参数错误（pending 放在错误位置、非法 EIP-1898 object、与不兼容字段混用）在
  Prepare 阶段返回 `ErrInvalidRequest`，不得触网或建立 anchor。
- context cancellation、Close、预算耗尽和 transport fault 仍沿现有 execution/lifecycle
  通道返回；本地观察规则不吞掉这些错误。
- `eth_call` 的 revert/data presence 仍使用 call 方法已有的 typed/admission 规则；本
  文不为 pending 引入新的 revert error 类型。真实 provider 没观察到的 `error.data`
  形态记录为 `not_observed`。

## 当前 public surface

根 `Client` 已通过 `BalanceAt`、`NonceAt`、`StorageAt`、`CodeAt` 以及 `Call`/`CallContract`
的既有参数形态承载部分 pending 请求；这些方法返回现有 `CallResult`，不能在局部任务
中改签名。`EthClient` 当前只有官方 mined/read 方法，没有
`PendingBalanceAt`、`PendingStorageAt`、`PendingCodeAt`、`PendingNonceAt`、
`PendingTransactionCount` 或 `PendingCallContract` facade。不能通过未知 `Call` 或薄包装
伪装补齐这些官方方法。

`PendingTransactionCount` 和 `PendingCallContract` 目前还没有完整的 catalog、ReadPlan、
typed projector、admission 和错误契约，因此保持 `unsupported`。若要承诺它们，必须在
R0/F0 单独冻结：RPC method admission、参数/返回绑定、source-local 与 quorum 关系、
revert/data/null 规则、预算和 transport 事实，再统一修改 root/view 接口。

## 正反例矩阵

| 场景 | 预期 | 依据 |
| --- | --- | --- |
| 三个 source 返回相同 balance pending | `Selected`，Support 仅首个 source | `TestPendingAgreementIsSelectedNotCorroborated` |
| 三个 source 返回不同 pending 值 | `Selected` 首个可用值，不是 conflict | `TestPendingDissentSelectsFirst` |
| 所有 source 返回协议错误 | `Unknown` + `ErrNoValidResponse` | `TestPendingAllFailIsUnknown` |
| pending object 带 `requireCanonical` | 原样透传，不建立 anchor | `TestPendingObjectFormIsLocalObservation` |
| pending state adapter | typed 值成功，且无 anchor 请求 | `TestPendingViaAdapterSucceedsDespiteCorroborationGate` |
| pending logs / block receipts | 本地 `ErrInvalidRequest`，零上游请求 | `TestLogsRejectPendingRangeLocally`、`TestBlockReceiptsPendingObjectSendsNoUpstreamRequest` |
| pending typed BlockByNumber(-1) | adapter 本地拒绝；不把 pending block 当普通 block | `TestBlockByNumberRejectsPendingLocally` |

缺少的事实仍是 `not_observed`：真实 provider 对 pending null、pending error-data、跨
provider pending transaction pool 差异的完整分布尚未形成 census。合成 fixture 只能验证
本地分类，不能升级 provider 兼容性结论。

## Flash 可执行白名单与升级触发器

在 R0 统一审查前，低风险任务可以：

- 为已有 `PendingObserve` 方法增加同一 source fixture 的 HTTP/native WSS 请求形状、
  null/malformed、取消、Close 和材料断言；
- 更新方法覆盖表和测试命令记录；
- 为 `PendingTransactionCount`/`PendingCallContract` 补调查 fixture，但只能标记
  `unsupported` 或 `needs-review`，不能新增 facade。

出现以下事实必须停止并升级到 R0/F0：需要改变 `CallResult` 或 `Decision.Status`、把多
source pending agreement 升为 `Corroborated`、引入 quorum/权重、为 pending 建立 anchor、
新增官方 view 方法、或把真实 provider 的 null/error-data 观察映射成新的公共错误层级。

## 验收命令

```text
go test ./rpc/methods -run 'Pending' -count=1
go test . -run 'Pending' -count=1
go test ./rpc/methods -race -run 'Pending' -count=1
git diff --check
```

