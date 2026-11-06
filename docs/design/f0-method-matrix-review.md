# F0 方法矩阵核对

状态：F0 前审计完成（2026-11-07）。本文把
`method-transport-coverage.md` 的方法事实与当前导出代码、测试证据和路线图状态
逐项对齐；它不改变公共接口，也不把未实现的方法标记为已支持。

## 核对结论

当前 `EthClient` 视图保持 29 个导出方法：27 个 typed reads、`SendTransaction`
和 `Close`。根 `Client` 对这些读取方法提供带 `CallResult` 的切面，并额外提供
`SendRawTransaction` 和根侧 `CreateAccessList`。根侧和视图侧共用同一 execution、
typed projection、reference、证据和生命周期；视图只投影官方 go-ethereum 形态。

已具备可运行证据的 transport 族包括：

- ChainID、Header、Receipt、BlockReceipts、Call、Logs、EstimateGas；
- NetworkID、hash-pinned Balance/Nonce/Storage/Code、GasPrice、BlobBaseFee；
- TransactionByHash、FeeHistory、CreateAccessList；
- 显式 gethcompat 与 native HTTP 的 envelope/raw-result differential（前三项）。

这些证据覆盖请求参数、typed 值、Decision 状态和主要 null/malformed/pending/local
拒绝边界。它们不代表 provider-backed 事实已经观察完毕；`eth_call` 的真实
`error.data=null`、部分 provider revert 形态和分页/资源错误仍须保持
`not_observed`。

## 方法族矩阵

| 方法族 | Root/View | Catalog/ReadPlan | HTTP/WSS | gethcompat | 当前结论 |
| --- | --- | --- | --- | --- | --- |
| 标量与节点状态 | 已有 | Registered | scalar/AtHash transport fixture | envelope 级 | 已实现；pending 语义按独立审计处理 |
| 区块、Header、BlockNumber | 已有 | Registered | Header/Block transport 与 reference 测试 | envelope 级 | 已实现；full body、moving reference 和大区块成本仍是 F0 证据项 |
| Receipt、BlockReceipts | 已有 | Registered | receipt transport、null/empty/binding 测试 | envelope 级 | 已实现；大数组和真实 provider 形态保留审计项 |
| TransactionByHash | 已有 | Registered | HTTP/WSS typed + null/pending 测试 | raw-result parity | 已实现；TransactionInBlock/TransactionSender 仍未承诺 |
| Call、EstimateGas、Logs | 已有 | Registered | HTTP/WSS 请求与 typed/revert/range 测试 | envelope 级 | 已实现；overrides、provider error-data 和订阅仍未承诺 |
| FeeHistory、费用方法 | 已有 | Registered | HTTP/WSS typed projection/参数测试 | raw-result parity（FeeHistory） | 已实现；percentile/provider 差异继续记录 |
| CreateAccessList | Root only | Registered | HTTP/WSS pending-local fixture | raw-result parity | 已实现为 root extension；不加入 EthClient |
| SendRawTransaction/SendTransaction | Root/View | Write pipeline | raw-call transport 与 WriteDecision 回归 | 普通 adapter 能力 | 仅承诺 broadcast acceptance/rejection/unknown/dispute，不承诺 mined/sync |
| PendingBalance/Storage/Code/Nonce | Root/View 未承诺 | 部分 plan（PendingObserve） | 仅调查/语义测试 | 未承诺 | 先按 source-local、Selected 上限和首个可用 source 语义冻结；是否扩 facade 交 R0/F0 |
| PendingTransactionCount/PendingCallContract | 未有 | 无闭合 plan/typed | 不应触网 | 不承诺 | Unsupported，不能用动态 Call 伪装覆盖 |
| TransactionSender/TransactionCount/TransactionInBlock | 未有 | 未闭合 | 不承诺 | 不承诺 | 待独立方法契约，不作为当前能力 |
| EstimateGasAtBlock/AtBlockHash、CallContractAtHash | 未有 | 未闭合 | 不承诺 | 不承诺 | 待独立 reference/typed 契约 |
| SimulateV1、Proof、Trace、同步发送 | 未有 | Explicit unsupported 或待契约 | 不应 silent forward | 不承诺 | 必须先定义 admission、结果和错误模型 |
| SubscribeNewHead/FilterLogs/TransactionReceipts | 未有 | 无生产订阅 plan | WSS subscription 未实现 | 不承诺 | S0/S1/S2 仍需独立事件结果、source snapshot、quorum、reorg、背压和 Err/Close 契约 |
| `net_listening` | Root `Call` only | Registered read | raw dynamic read | adapter 能力 | 当前不提供 typed wrapper；是否纳入承诺范围由 F0 决定 |

## 证据和停止条件

方法矩阵只有在以下证据齐备后才能被 F0 标为完成：

1. 每个声明方法能指向 catalog/ReadPlan、root/view 实现和至少一个正反例测试；
2. unary 方法分别记录业务值、业务 error、Decision、reference、材料和 Close/取消；
3. HTTP、native WSS 和需要时的 gethcompat 请求形状已对照；
4. 未支持方法在 catalog/admission 层拒绝，不能由未知 `Call` fallback；
5. provider 未观察的 error-data、分页、资源限制和扩展字段单独标记 `not_observed`；
6. 订阅拥有独立的通知 demux、事件身份、snapshot/generation、quorum、Pull、
   backpressure、Unsubscribe、Err 和 owner Close 测试。

当前阻塞 F0 的项目是：订阅 S1/S2 尚未实现；Pending facade 是否承诺仍待统一决定；
区块大对象、日志/收据大数组和 provider-backed error-data 的观察仍不完整；
`TransactionSender`、`TransactionInBlock`、同步发送、Simulate、Proof 和 Trace
没有独立契约。它们必须保留为待决定或 unsupported，不能写成“覆盖完成”。

## 验收命令

本次核对使用并要求 F0 重跑：

```text
gofmt -l .
git diff --check
go test ./... -count=1
go test ./... -race -count=1
go vet ./...
go test . -run 'TestArchitecture|TestPackage|TestDefaultBuild' -count=1
./scripts/release_check.sh
```

与订阅相关的 WSS race、取消、Close 和 goroutine leak 测试必须在 S1/S2 完成后追加；
在此之前，F0 只能验收 unary 方法矩阵和明确的未支持项。
