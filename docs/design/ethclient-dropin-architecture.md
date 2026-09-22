# Ethclient 兼容层架构记录（历史提案）

> 状态：历史提案，已被 `spec-baseline` §3 及后续治理决策取代。当前以规范基线、治理决策和代码中的 catalog 为准。

## 当前定位

项目的主线不是承诺完整替代 `go-ethereum/ethclient`，而是让已接入的方法在调用层尽量保持相同的方法名、参数和返回类型。调用者主要承担配置初始化的迁移成本；请求返回的信息经过多源比较、仲裁和相对可信回执处理。

当前保留两层接口，这是有意的架构选择：

```text
业务代码 → ethguard/ethadapter（ethclient 形状，隐藏 Decision）
         → ethguard.Client（类型化审计面，返回 Decision）
         → aggregator → rpc/methods
```

两者的对应关系由 `rpc/methods/catalog.go` 的 `Adapter` 字段和覆盖测试维护；“双层”不是待删除的冗余层。新增方法必须同时考虑策略、根层审计方法、adapter 兼容方法及 catalog 映射。

## 兼容范围与已知差异

当前已覆盖一组常用只读方法，调用形状与对应版本的 `ethclient` 对齐。兼容性是“方法形状兼容”，不是完整接口兼容。

- `Header*`、`*AtHash`、`Pending*`、部分交易查询方法尚未覆盖；moving tag 按 corroborated reference 语义处理。
- `SendTransaction` 等写路径和订阅路径需要独立的一致性模型，当前不属于读路径承诺。
- 目前不能承诺直接满足 `bind.ContractBackend`，合约绑定用户需检查所需方法集合。
- 配置、多个 upstream、仲裁模式、超时、锚定和生命周期管理与官方 `Dial(url)` 不同；“改 import”不代表零配置迁移。
- corroborated `BlockNumber`、`latest` 锚定、pending 拒绝、仲裁冲突和错误分类，都是有意保留的语义差异。

低成本补齐项应优先纳入路线图：根层已有 `BlobBaseFee`，adapter 尚需补齐薄壳；`NetworkID`、`FeeHistory`、`CreateAccessList` 也应分别评估，其中 `FeeHistory` 需转换为 `*ethereum.FeeHistory`。高频的 `HeaderByNumber` 等方法按规范基线排序推进。

## 演进原则

每个新 RPC 方法都应先定义稳定的多源策略和相对可信回执，再提供根层带 Decision 的类型化结果，最后提供 ethclient 形状的 adapter 方法。adapter 只隐藏证据，不改变策略结果；配置和仲裁语义通过文档明确暴露。

审计用户通过 `Unwrap()` 使用根客户端及其 Decision 能力。普通业务使用 `ethadapter.Client`，并以规范基线中的支持矩阵判断是否可迁移。
