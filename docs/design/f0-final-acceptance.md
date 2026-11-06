# F0 总体验收记录

状态：**unary 方法、S1 transport seam 和 S2 内部聚合状态机已验收；公共订阅 facade 仍未完成**（2026-11-07）。本文是对当前路线图执行结果的汇总，不把待实现能力写成已支持。

## 已验收范围

- M0 方法与传输清单已逐项对齐 go-ethereum v1.17.5、catalog/ReadPlan、root/view、HTTP/WSS、gethcompat 和 unsupported 边界。
- M1/M2 的 AtHash、标量、费用、Header 和 reference 切片已通过 typed 与跨 transport 回归；Pending 保持 source-local `Selected` 语义，不建立伪造 reference。
- M3 的 Receipt、BlockReceipts、Logs、Call、EstimateGas、TransactionByHash、FeeHistory 已有 null、malformed、pending、binding、revert、预算和 HTTP/WSS 证据；大数组成本与材料预算审计记录在 `m3-cost-audit.md`。
- M4 已冻结广播 accepted/disputed/rejected/unreachable 与 mined 分离；CreateAccessList、SendTransaction/SendRawTransaction 的 transport、foreign hash、错误和签名字节回归已通过。Proof、Simulate、Trace、同步发送继续保持 unsupported 或待独立契约。
- T0 已覆盖 native HTTP、raw WSS 和显式 gethcompat differential；业务层没有新增 transport-specific 分支。
- S0 契约已冻结 Strict 默认、显式 `SingleSourceSelected`、source snapshot/generation、事件身份、队列溢出终止和 Err/Close 顺序。
- S1 已实现独立 `OpenNotificationStream` seam，隔离 unary response 与 notification，覆盖 source identity/generation、异步错误、队列背压、取消、断连、Unsubscribe、owner Close 和 race。
- S2 已实现与公共 API 解耦的 `internal/subscription` 状态机，覆盖固定 snapshot/quorum、source-local 去重、Strict/SingleSourceSelected、超时隔离、reorg/removed key、同源 Pull 校验、backpressure、Close/Unsubscribe 和 race；removed 事件与原已交付日志的绑定仍需在 F0 复核后再关闭。

## 尚未关闭的范围

S2 的状态机尚未接入 source Pull worker、业务事件交付或公共 `EthClient` 订阅 facade；缺口策略、重连和 exactly-once 仍未承诺。当前不能声明 `SubscribeNewHead`、`SubscribeFilterLogs` 或 transaction receipt subscription 已支持，也不能把单源推送升级为 `Corroborated`。

Pending facade、TransactionSender/TransactionInBlock、同步发送、EstimateGasAtBlock*、CallContractAtHash、Proof/Simulate/Trace、overrides 等仍缺少完整 catalog/admission/typed/错误契约，按 F0 unsupported 审计保持未承诺。真实 provider 的 `error.data=null`、分页/资源错误、replacement/already-known 和 pending pool 分布继续标记为 `not_observed`。

## 公共接口结论

R0 审查确认当前 unary root `(value, CallResult, error)`、EthClient 官方视图、`WriteDecision`、`ResultCapture` 和材料预算足以表达已验证事实，无需新增泛型包装或修改现有签名。订阅必须使用独立事件结果与生命周期句柄；公开根事件构造函数、类型导出细节、重连/exactly-once 和 removed 交付策略必须在 S2/F0 单独决策，不能复用 `CallResult`。

## 验收命令与结果

以下命令在 full-access 环境执行并通过：

```text
go test ./... -count=1
go test ./... -race -count=1
go vet ./...
go mod tidy -diff
go test . -run 'TestArchitecture|TestPackage|TestDefaultBuild' -count=1
go test ./internal/transport/wss -race -count=2
go test ./internal/transport/http -race -count=2
go test ./internal/subscription -race -count=2
go test . -run 'TestCallRejectsUnroutableMethodsLocally|Pending|TransactionByHash|FeeHistory|CreateAccessList|SendTransaction' -count=1
./scripts/release_check.sh
```

`release_check.sh` 的构建、race、静态检查和漏洞扫描均通过。provider-backed golden 未观察项没有用合成 fixture 关闭；它们仍单独列在方法矩阵和成本审计中。

## F0 退出条件与下一步

F0 只有在 S2 的 source Pull worker 接线、业务事件交付、缺口策略、公共 channel、取消、Close、Err 和 goroutine race 矩阵通过后，才能把订阅能力从“内部状态机/transport seam”提升为承诺。若接线需要改变 unary `Transport`、`CallResult`、公共视图签名、quorum 或错误层级，必须回到 R0 统一决策并重跑全量门禁。
