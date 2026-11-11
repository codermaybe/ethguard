# go-ethereum 版本兼容政策

状态：现行发布政策；方法集合基线为 2026-11-09，SyncProgress 字段缺口于 2026-11-11 修复。本文不以历史工具升级日志或单次检查结果替代兼容声明。

## 完整性目标

`EthClient` 按 `go.mod` 锁定的 go-ethereum 版本实现完整业务方法集合，不发布“当前已接入子集”兼容版本。v1.17.10 的 `ethclient.Client` 有 48 个导出 receiver 方法；`Client()` 是返回底层 `*rpc.Client` 的具体访问器，明确排除，因此业务目标是 47 个。

当前 view 有 46 个，仍缺 `SimulateV1`。其官方 `SimulateOptions`、`SimulateBlockResult` 与方法定义仍在 `go-ethereum/ethclient`；默认原生生产根包不允许直接导入该 client。完整兼容发布因此受阻，不能用 `ErrUnsupported`、未知 Call 或变更计数隐藏缺口。当前状态和逐方法证据只有 [method-transport-coverage.md](method-transport-coverage.md) 维护。

`gethcompat.SimulateClient` 是显式 opt-in 的单源兼容 seam：使用官方类型与签名，复用 geth 的连接和生命周期，不进入默认根包执行图，不产生 EthGuard CallResult／quorum／跨源证据，也不计为默认 EthClient 已接入。后续若关闭缺口，必须同时决定导入边界、root/view 执行、证据和生命周期，不能只增加签名。

## 两个 facade，一个执行 owner

Root `Client` 提供 CallResult 证据面和必要控制；`Client.EthClient()` 提供官方形状 view，共享 runtime、typed projection、传输与生命周期，不复制业务实现。CallResult 属于单次 unary 调用；订阅使用独立事件／生命周期合同。

view 不暴露 `Client()`，因为它会绕过 runtime、quorum、typed 与 evidence。内部 adapter 的具体 rpc/client 依赖受现有生产白名单与静态 interlock 管理；显式兼容 adapter 不解除默认构建限制。

## 发布与升级门禁

1. 从锁定的官方源码核对完整方法集合，运行 `scripts/ethclient-method-diff.sh`；明确保留 Client() 排除项。
2. 每个业务方法闭合 Root execution、view 签名、typed 字段／错误绑定、生命周期、HTTP/WSS fixture 和 evidence。
3. 订阅还需 ethereum.Subscription、取消、重连、背压、异步错误与 race 矩阵，不能从 WSS request 能力推出订阅兼容。
4. 方法数完整不是返回字段完整。SyncProgress 已补齐七个上游同步／索引字段，remaining counters 保真以公开 Root／EthClient 的 HTTP／raw WSS 合成场景验证；可选字段缺失仍按上游为零。既有外部 syncing capture 只有 false，不能将本地修复当作新增 provider 对象形态观察。
5. 升级先生成 old/new 方法差异，再补齐变更方法与行为证据，最后同步依赖锁、矩阵、contract tests 和发布说明。不能把 build／方法计数通过当作整份兼容性证据。

发布流程及显式接受单一外部 SimulateV1 缺口的预发布检查模式由 [release-contract.md](release-contract.md) 维护；该检查模式不允许宣称 complete。

## 唯一对外声明

```text
go-ethereum: vX.Y.Z
EthClient: complete
Root Client: evidence facade over the same execution
HTTP/WSS: unary + subscription status from the same method matrix
```

只有全部方法、typed 行为和传输／生命周期证据闭合后才能使用此声明。内部阶段名、完成百分比、普通测试通过或局部 provider 观察均不创建对外子集兼容版本。历史依赖升级与本地 smoke 日志留在 Git；操作命令和工具固定版本由发布合同单源维护。
