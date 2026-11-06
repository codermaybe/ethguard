# R0 返回值与接口统一审查

状态：审查完成，订阅接口仍为 `needs-review`（2026-11-07）。本文把当前
M1–M4、T0、Pending 和 S0/S1 输入收敛为一套边界；不修改公共 API、
`CallResult`、`ResultCapture` 或 transport。

## 1. 结论

当前 unary 公共契约已经能表达已验证的事实，不需要新增包装类型或改变现有
方法签名：

- 根 `Client` 的 typed 方法返回业务值、`CallResult` 和 trailing `error`；
  `CallResult.Err` 与 trailing error 相同。
- `Client.Call` 返回 `CallResult` 和 error；写入只填 `WriteDecision`，读取只填
  `Decision`，两者互斥。调用在校验或建立 reference 前失败时两者都可以为空。
- `EthClient` 只投影官方 go-ethereum 形状并返回业务值和 error。它不丢失事实，
  因为调用方可以通过 `WithResult`/`ResultCapture` 获取同一次调用的完整结果。
- `Decision.Status` 是证据分类，不是业务成功标志。`Corroborated` 的 revert、
  receipt null/`ethereum.NotFound` 和 typed binding error 都保留已经形成的
  `Decision`，业务结论放在 error。
- `ReferenceEvidence` 只描述本次 moving-tag 调用建立的 reference。Pending
  local observation 不建立 anchor，`BlockRef` 保持空；不能为了统一形状伪造
  reference。
- `Uncertain` 是有预算的原始材料快照，不参与业务裁决；关闭、取消和预算耗尽
  不改变已形成的业务 Decision。

订阅不能复用 unary `CallResult`。根 Client 未来应使用独立的事件结果类型，
EthClient 才转换为官方 `channel + ethereum.Subscription` 形状。具体公开名称、
单 source 策略、事件预算和重连语义尚未冻结，继续标记 `needs-review`。

## 2. Unary 结果分层

| 层 | 当前载体 | 责任 | 允许的错误/状态 |
| --- | --- | --- | --- |
| 业务值 | typed 返回值或 `Decision.Value` | 规范化后的方法结果；null 在方法边界映射为 `ethereum.NotFound` | 可以为空；不能从错误来源拼装 |
| 业务 error | trailing `error` 与 `CallResult.Err` | revert、NotFound、typed decode、invalid request、关闭和取消等调用结论 | 与非 `Unknown` Decision 共存 |
| 裁决事实 | `Decision` / `WriteDecision` | source basis、quorum、status、support/reject/fault、hash 和广播状态 | 失败也保留已形成的事实 |
| reference | `Decision.Reference` | 本次 moving tag 的 hash/height、参与者和建立失败证据 | Pending/local、immutable hash read 不伪造 |
| 原始材料 | `CallResult.Uncertain`、`Decision.Provenance`/`Diagnostics` | 预算内 raw response、字段和方法专属 provenance | 可截断；不升级业务状态 |
| 生命周期 | `ErrPoolClosed`、context error、`ResultCapture` 状态 | owner Close、取消、重复 capture、并发完成 | 不把 capture 错误改写成业务 error |

`Decision.Value` 的规范字段由方法编码器决定。已迁移的数量、成功 DATA、
FeeHistory、Logs、AccessList、Receipt 和 BlockReceipts 使用规范编码；其他方法
仍按既有代表源规则。R0 不把所有方法强行迁移到一个泛型结果接口。

## 3. 状态与 error 组合

以下组合是有效且必须继续测试的：

| 结果 | `Decision.Status` | error | 解释 |
| --- | --- | --- | --- |
| 多源值达门槛 | `Corroborated` | nil | provider-backed cross-source evidence，不是 consensus proof |
| 多源 null 收据/交易 | `Corroborated` | `ethereum.NotFound` | absence 是已裁决的业务结论 |
| 多源 revert | `Corroborated` | `*RevertError` | `Decision.Revert`/受保护 data 保留，error 表达执行失败 |
| 单 source pending 观察 | `Selected` | nil 或 typed 业务 error | 只表示该 source 的 pending 状态，不是 quorum |
| variable fee/peer/sync 结果 | `Selected` | nil 或 typed error | lower-median/selected 规则，不宣称 corroborated |
| 未达门槛或无有效响应 | `Unknown` | `ErrConflict`/`ErrNoValidResponse`/malformed | 未形成可交付业务值；Faults 仍可审计 |
| 广播 hash 达门槛 | `WriteAccepted` | nil | 传播接受；不表示 mined |
| 广播分歧、拒绝或不可达 | `WriteDisputed`/`WriteRejected`/`WriteUnreachable` | 对应 write error | 不能推导 receipt 或全网状态 |

任何局部 facade 都不能把 `Selected` 升成 `Corroborated`，也不能因为 error 非空
而清空已经形成的 `Decision`。只有主 aggregation 失败才清除公开 `Value`。

## 4. Root/View 对照

根方法是裁决事实的完整切面；视图方法只隐藏事实载体，复用同一 execution、
reference、pool、typed projection 和生命周期。两者不得各自实现 transport 或
第二套错误映射。

| 能力 | Root `Client` | `EthClient` view | 当前决定 |
| --- | --- | --- | --- |
| 读取 | `(value, CallResult, error)` | `(value, error)` | 保持 |
| 泛型 RPC | `Call` 返回 `CallResult`；未知/订阅/写方法本地拒绝 | 不暴露 | 保持 |
| 广播 | `SendRawTransaction`/`SendTransaction` 返回 `CallResult` | `SendTransaction` 只返回 error | 保持；通过 ResultCapture 读取 WriteDecision |
| CreateAccessList | root typed 方法 | 不加入官方视图 | 保持隐藏边界 |
| Pending | 已有 root 参数形状按 plan 观察 | 未承诺额外 Pending facade | 保持；PendingTransactionCount/PendingCallContract unsupported |
| 订阅（候选） | 独立事件结果类型 + lifecycle handle | 官方 channel + `ethereum.Subscription` | 只在 S1/S2 与 R0/F0 冻结后实现 |

`ResultCapture` 是一次调用、一次发布的 delivery channel。它不能扩展为事件
队列，也不能被订阅复用。`WithSourceEvidence` 的预算同样是单次调用预算；
订阅必须另定义 event/lifetime budget。

## 5. 订阅候选接口边界

订阅公开接口暂不落地，以下只作为 R0/F0 的候选约束：

```go
// candidate only; names and generic shape are not frozen
type SubscriptionEvent[T any] struct {
    Value     T
    Decision  *rpc.Decision
    Uncertain *UncertainResult
}
```

根事件必须携带达到门槛后的事件 Decision、source snapshot/generation 和可选的
事件材料；未达到门槛的候选不能进入业务 channel。EthClient 的 `SubscribeNewHead`
和 `SubscribeFilterLogs` 必须只发送已经满足契约的 typed 值，异步 transport、
quorum、reorg、背压和取消通过 `ethereum.Subscription.Err()` 表达。

以下事项仍是 `needs-review`：官方方法签名、根事件 channel 形状、单 source 是否
允许降级、reorg removed 的交付方式、Pull 超时/同源能力、队列溢出策略、自动重连
和 exactly-once。S1/S2 不得在这些问题未冻结前修改 unary 类型或偷偷扩展 raw-call
Transport。

## 6. 不需要公共接口变更的事实

- `TransactionByHash` 的 pending bool 继续由 typed projection 返回；NotFound 与
  `Decision` 分层，不新增 TransactionResult。
- `FeeHistory`、Logs、Receipt 和 AccessList 的对象规范化属于方法编码器，不需要
  泛型 `Result[T]`。
- Pending 的 source-local/Selected 语义可以由已有 `Decision.Status` 和空
  `Reference` 表达；不新增 PendingResult。
- 广播接受与 mined 分离已经由 `WriteDecision` 表达；不新增同步发送返回值。
- provider 尚未观察到的 error-data/null 形态继续记录 `not_observed`，不为合成
  fixture 增加公共错误层级。

## 7. R0/F0 升级触发器

必须停止局部实现并交给 R0/F0 的事实包括：

1. 需要让 `CallResult` 同时承载异步事件，或需要第二种 capture 生命周期。
2. 需要把 Pending agreement、单 source push 或广播 accepted 宣称为更高事实等级。
3. 需要把 mined、receipt wait、reorg 或同步发送塞进 `WriteDecision`。
4. 需要新增官方 EthClient 方法、改变现有返回值、增加公共错误 sentinel，或让
   CreateAccessList/Proof/Simulate 穿透当前隐藏/unsupported 边界。
5. native HTTP/WSS/gethcompat 的差异要求业务层分支，或订阅需要修改 unary
   request-ID demux、pool quorum/reference。

## 8. F0 验收清单

F0 关闭前必须有可运行证据：

- 方法矩阵逐项列出 root/view、catalog、HTTP/WSS/gethcompat、null/pending/error、
  Decision、reference、材料和 Close/取消行为；未知方法明确拒绝。
- 所有已承诺 unary 方法通过单测、race、`go vet`、architecture/default-build
  门禁；失败日志与 provider `not_observed` 分开记录。
- Pending 的 source-local、Selected 上限和本地拒绝有反例；Mined 请求不会复用
  pending 语义。
- WriteDecision 的 accepted/disputed/rejected/unreachable 与 typed raw bytes 有
  回归；receipt 查询仍是独立调用。
- 若实现订阅，必须通过 S1/S2 的 scripted WSS、generation、reorg/removed、同源
  Pull、quorum、背压、取消、Close、Err 和 goroutine race 矩阵；否则在 coverage
  文档中明确标为待实现。
- 公共接口变化只能集中在 R0/F0 的一次决策和迁移说明中。

