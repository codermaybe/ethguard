# M4 写入与扩展边界事实卡片

状态：审计完成（2026-11-07）。本文冻结当前可承诺的边界，并为后续机械任务提供准入条件；不改变公共接口。

## 已确认的写入语义

`Client.SendRawTransaction` 是唯一的 JSON-RPC 广播入口，`SendTransaction` 只是把已签名的 `types.Transaction` 编码后复用同一实现。`eth_sendTransaction`（节点托管账户）没有可证明的幂等重发语义，因此不进入当前承诺范围。

广播在发请求前固定 voting basis、corroboration mode 和 support floor。正常模式使用 `pool.Broadcast` 联系所有可请求来源；forwarded 模式只向一个来源执行并将 basis 设为 1。来源答案按本地解码的交易 hash 分簇，获胜 hash 必须等于待发送字节本地计算出的 hash。

写入结果是传播裁决，不是挖矿或共识证明：

| 状态 | 事实 | 调用方动作 |
| --- | --- | --- |
| `WriteAccepted` | hash 簇达到固定 basis 的 quorum；其余 dissent/rejection 仍保留为证据 | 取得 hash 后按自己的确认窗口轮询 `TransactionReceipt` |
| `WriteDisputed` | 有 hash 答案但没有簇达到 quorum | 不绑定任何 receipt hash；记录冲突并决定是否重新广播 |
| `WriteRejected` | 所有回答来源都给出结构化协议拒绝 | 只能说明回答来源拒绝；静默来源可能已收到，不能推出全网未提交 |
| `WriteUnreachable` | 没有可用值或协议答案 | 接受状态未知；不能把 transport fault 当作交易无效 |

`CallResult.WriteDecision` 与 trailing error 表达同一裁决；`IsWriteAccepted` 只检查 accepted、非空 hash，不表示 mined。错误文本（如 already known）不会被转换为接受；协议错误保留 `error.data` 作为拒绝证据。广播后等待收据必须是独立调用，当前没有同步发送方法。

## 当前扩展边界

### `eth_createAccessList`

该方法已注册并有 root `Client.CreateAccessList`，但不进入 `EthClient` 官方视图。它沿现有 read execution 使用 method-specific admission、EIP-1898 reference 参数和 typed `AccessListResult` 投影；成功 Value 是规范 access-list 对象，null、字段缺失、数量编码错误、来源分歧和 pending 规则由既有方法计划裁决。它不是写入，也不承诺交易已广播或已执行。

后续机械任务可以补同一 fixture 的 HTTP/native WSS/gethcompat 请求与响应对照、malformed/null/Close/race 回归，但不得把它加入 `EthClient` 或改成写路径。

### `eth_getProof`

目录明确将其标记为 `UnsupportedMethod`，不存在 read plan、root facade 或视图方法；`Client.Call` 触网前拒绝未知/未注册方法。只有在单独定义 proof admission、请求绑定、Merkle 验证、返回值和证据模型后，才能从 unsupported 提升。任何动态 Call fallback 都违反当前边界。

### `SimulateV1` 与其他扩展

`SimulateV1`、`Trace*`、gethclient `GetProof`、pending subscriptions 以及 `CallContract` overrides 当前均未进入项目承诺。它们需要各自的 catalog admission、typed projection、错误/预算模型和 transport 事实，不能通过通用 `Call` 伪装覆盖。扩展探索阶段只登记事实和拒绝理由，不新增公共方法。

## 明确不做的同步语义

`SendTransactionSync` / `SendRawTransactionSync` 需要把广播接受、receipt 出现、reorg 和超时策略组合成新的状态机；现有 `WriteDecision` 不能表达 mined 生命周期。除非 R0/F0 先冻结独立结果类型、确认窗口、取消和 owner `Close` 规则，否则保持未实现。后续任务不得用“广播成功后立即调用一次 receipt”作为同步发送替代品。

## Flash 可执行白名单

在本卡片冻结后，低风险 agent 只可执行以下任务：

1. 扩展 `CreateAccessList`、`SendRawTransaction` 和 `SendTransaction` 的既有 fixture，覆盖 native HTTP/WSS/gethcompat 请求形状、null/malformed、预算、取消和 Close；
2. 为写入结果表补充断言，验证固定 basis、foreign hash fail-closed、minority dissent、协议拒绝和 unreachable 的 `WriteDecision` 与材料记录；
3. 更新 `method-transport-coverage.md`、方法卡片和实际命令记录。

任务文件白名单应限制在对应 `*_test.go` 与 `docs/design/*coverage*`/方法卡片。禁止修改 `CallResult`、`WriteDecision`、pool broadcast、transport seam、catalog admission、EthClient 方法集合和订阅代码。

## 升级触发器

出现以下任一事实时必须停止机械任务并交回 R0/F0：

- 需要把 accepted 映射成 mined、同步发送或 receipt 等待；
- 需要新增写入状态、错误层级、结果类型或公共 facade；
- provider 对 `error.data`、already-known、replacement 或 null 形态的真实观察改变现有分类；
- CreateAccessList/Proof/Simulate 需要新的 admission、quorum、reference、typed 字段承诺或 cryptographic verification；
- native HTTP/WSS/gethcompat 的差异要求业务层分支，或需要复用 raw-call transport 实现订阅。

## 验收命令

```text
go test ./... -run 'Send|AccessList|Proof|Simulate' -count=1
go test ./... -race -run 'Send|AccessList|Proof|Simulate' -count=1
git diff --check
```

本次审计未观察到新的 provider-backed error-data 事实；若后续真实 provider 仍未出现某种形态，记录为 `not_observed`，不得用合成 fixture 将其标记为已观察。
