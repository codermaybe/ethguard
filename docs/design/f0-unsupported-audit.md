# F0 未支持方法与 admission 审计

状态：完成（2026-11-07）。本文记录当前 catalog 和 `Client.Call` 的拒绝边界，避免把未知 RPC 或扩展方法误报为已覆盖。

`eth_sendRawTransaction` 只能通过 `SendRawTransaction` 写路径进入；`eth_sendTransaction`、同步发送扩展和未注册的写方法在通用 `Call` 入口本地拒绝。`eth_subscribe` 以及 pending subscription 族同样在 unary `Call` 入口拒绝，必须等待独立 S1/S2 订阅能力。

当前明确保持 unsupported 或待独立契约的方法包括：

- `eth_getProof`：需要 proof admission、请求绑定、Merkle 验证和证据模型；不能通过未知 Call fallback。
- `eth_simulateV1`、`trace_*`、`debug_trace*`：需要独立 typed projection、预算和错误模型。
- `TransactionSender`、`TransactionInBlock`、`TransactionCount`：官方方法存在，但当前 catalog、typed projection 和 root/view 结果闭环未完成。
- `PendingTransactionCount`、`PendingCallContract` 与 pending facade：需要 source-local 策略和统一 Decision 事实，当前不提供薄包装。
- `SendTransactionSync` / `SendRawTransactionSync`：广播接受、receipt、reorg、超时和取消需要新的生命周期结果类型。
- `CallContract` overrides、`EstimateGasAtBlock*` 和 gethclient 扩展：尚未冻结 admission、reference 和 provider 兼容事实。

`call_route_test.go` 的拒绝矩阵覆盖 raw write、账户托管写入、同步写入、Proof、Simulate、Trace、订阅和未知方法，并断言请求不会到达 provider，返回 `ErrInvalidRequest` 且不伪造 `Decision`。任何新增方法必须先进入 catalog/ReadPlan、typed projection、transport fixture 和错误分层矩阵，不能依靠动态方法名放行。

真实 provider 的 `error.data=null`、replacement/already-known、扩展方法字段和同步发送结果仍未观察到；这些事实保持 `not_observed`，合成 fixture 只能验证本地分类，不能升级为 provider 兼容性结论。

## F0 升级触发器

出现以下任一情况必须回到 R0/F0 统一决策：需要新增公共结果类型或 facade；需要让 unsupported 方法通过未知 Call；需要把 broadcast accepted 映射为 mined；需要 cryptographic proof/trace 验证；或 native HTTP/WSS/gethcompat 差异要求业务层分支。
