# 官方业务语义与 go-ethereum 方法覆盖政策

状态：2026-11-14 明确整体业务类型统一到 execution-apis 生成模型；代码迁移尚未完成。方法集合基线为 2026-11-09，SyncProgress 字段缺口于 2026-11-11 修复。本文不以历史工具升级日志或单次检查结果替代兼容声明。

## 完整性目标

`EthClient` 按 `go.mod` 锁定的 go-ethereum 版本核对完整业务方法与语义集合，不发布“当前已接入子集”兼容版本。v1.17.10 的 `ethclient.Client` 有 48 个导出 receiver 方法；`Client()` 是返回底层 `*rpc.Client` 的具体访问器，明确排除，因此业务目标是 47 个。

官方 JSON 业务类型以固定 execution-apis 原始来源及显式审核的标准勘误为源，由项目生成并统一服务对象处理、root 与 view。公开签名使用项目生成类型，不再以 geth Go 名义类型身份一致为目标；这不是 SimulateV1 单项例外。原有方法的部分公开签名仍保留 geth 类型；逐方法完整签名测试已显式固定已迁移的 CallMsg／FeeHistory／SyncProgress 签名，旧签名一致性是迁移前代码事实，不是阻止新类型方案的永久约束。迁移须逐项建立项目公开类型、官方参数／结果语义、错误和生命周期合同，不能把方法名计数宣布为语义完整。执行现状及接手顺序见 [路线图](project-roadmap.md)。

`SimulateV1` 的 native root/view 和 Raw Call 共用一个注册执行计划。公开 payload 使用 `ethapi.SimulateV1Request`，base 使用 `*ethapi.SimulateBlockReference`，结果仍为 `[]ethapi.SimulateBlock`；nil base 表示 latest。请求和嵌套 override／call／selector 模型从固定有效 schema 生成，不引入手写 DTO。当前官方基线允许 quantity、tag、裸 hash 和两种 EIP-1898 对象；显式 requireCanonical 的 presence/value 保留，blockNumber 对象不接受官方未声明的 moving tag。请求模型完成不代表所有模拟语义或稳定发布验收完成；当前逐方法证据见 [method-transport-coverage.md](method-transport-coverage.md)。

该迁移有明确的公共类型变更：原 Simulate `json.RawMessage` payload 与 geth selector 签名被生成类型替换，Raw Call 仍可携带原始 JSON，但通过同一方法准备入口校验。低层 `MethodSpec.Validate`／`rpc.ReadPlan.Validate` 也替换为返回请求的 `Prepare(context.Context, Request) (Request, error)`；不保留两套并行注册或隐式兼容装配。其余尚未迁移的 geth 业务类型仍按方法族推进。

公共 `gethcompat` 构造／Simulate 入口和生产 geth transport 已退出。`internal/testkit/gethoracle` 只承载内部差分测试与显式诊断对照；它不是生产 adapter 或业务兼容退路。`cmd/nativesmoke` 可显式调用 oracle 做诊断，但其输出不能作为 native 语义或兼容性证明。历史 geth adapter 观测仍标记为 geth，不改写为 native 证据。

## 两个 facade，一个执行 owner

Root `Client` 提供 CallResult 证据面和必要控制；`Client.EthClient()` 提供对应业务方法 view，共享生成业务类型、runtime、结果投影、传输与生命周期，不复制业务实现。CallResult 属于单次 unary 调用；订阅使用独立事件／生命周期合同。

view 不暴露 `Client()`，因为它会绕过 runtime、quorum、typed 与 evidence。native 生产图不得依赖 `go-ethereum/ethclient`；geth 差分 oracle 仅允许位于内部测试工具及显式诊断工具依赖图中。

公开 Go 类型兼容不意味着使用 geth struct 解释全部 provider 原文，也不承诺把所有域外字段塞入同一类型。官方字段以 execution-apis 及方法语义为基准，经过有限前置兼容后检查和裁决；无法由公开类型表达的受保护信息及域外材料，分别按 [返回值合同](return-value-design.md#数据交付与责任范围) 交付。域外材料不承担与官方受保护结果相同的语义保证。

## 发布与升级门禁

1. 从锁定的官方源码核对完整方法集合，运行 `scripts/ethclient-method-diff.sh`；明确保留 Client() 排除项。
2. 每个业务方法闭合 Root execution、项目生成类型的 view 签名、官方字段／错误绑定、生命周期、HTTP/WSS fixture 和 evidence；生成结构不替代内容、签名和承诺检查。
3. 订阅还需 ethereum.Subscription、取消、重连、背压、异步错误与 race 矩阵，不能从 WSS request 能力推出订阅兼容。
4. 方法数完整不是返回字段完整。SyncProgress 已迁入官方生成模型，仅包含 startingBlock／currentBlock／highestBlock 三个 optional 字段；geth 同步／索引计数移至附属及原始来源证据，生成类型不提供 Done()。公开 Root／EthClient 的 HTTP／raw WSS 七场景同时验证官方主值和完整原始证据，并独立检查 remaining counters。既有外部 syncing capture 只有 false，不能将本地测试当作新增 provider 对象形态观察。
5. 升级先生成 old/new 方法差异，再补齐变更方法与行为证据，最后同步依赖锁、矩阵、contract tests 和发布说明。不能把 build／方法计数通过当作整份兼容性证据。

release 方法集合门要求 47 个业务方法完整。逐方法语义证据仍由覆盖清单维护；数量闭合不能代替字段、错误、引用、传输、证据和生命周期合同，也不能单独通过稳定发布门。

## 唯一对外声明

```text
business method reference: go-ethereum vX.Y.Z
official field reference: pinned execution-apis version
business methods and semantics: complete
public business types: EthGuard generated types
geth Go nominal type identity: not claimed
Root Client: evidence facade over the same execution
HTTP/WSS: unary + subscription status from the same method matrix
```

只有全部方法、typed 行为和传输／生命周期证据闭合后才能使用此声明。内部阶段名、完成百分比、普通测试通过或局部 provider 观察均不创建对外子集兼容版本。历史依赖升级与本地 smoke 日志留在 Git；操作命令和工具固定版本由发布合同单源维护。
