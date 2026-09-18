# 多源 Ethereum JSON-RPC 聚合：标准语义与公共观测

状态：第三方评审草案  
定位：设计讨论，不是已承诺的实现方案

## 1. 背景

ethguard 当前是一个面向 `go-ethereum v1.17.5` 的多源 JSON-RPC 客户端。项目已经具备以下技术基础：

- `rpc` 包定义 `Request`、`Observation`、`Decision`、`Policy` 和通用裁决策略；
- `rpc/methods` 按 Ethereum 方法实现归一化、锚定、必填字段和方法级错误策略；
- 当前方法语义使用 `internal/spec/methods.json` 的裁剪版比较白名单，并由方法代码承担具体 requiredness；
- 上游连接、健康状态和来源选择由其他模块负责，`rpc` 层不拥有连接池；
- 未注册或未知方法可以透明转发，但不会被提升为 `Verified`；
- 项目依赖锁定版本的 go-ethereum，并且希望保持较小、清晰的实现边界。

这份设计讨论的是下一层通用模型：如何在不破坏上述结构的前提下，把多个上游返回的标准字段和非标准扩展字段同时提供给调用方。

## 2. 目标与非目标

目标是：

1. 从多份杂乱 JSON 中保留完整观测信息；
2. 对节点共同返回的字段做结构和数值比较；
3. 用外部标准定义识别其中具有标准语义的字段；
4. 为标准字段提供稳定、兼容的核心结果；
5. 将 L2、厂商或节点私有字段作为带来源信息的扩展附带返回；
6. 让运行时间较长、响应稳定的上游在健康模型中自然胜出。

非目标是：

- 不设计节点权重或故障域权重；
- 不声称提供链共识证明或完整 Byzantine 容错；
- 不验证无法由 RPC 对象独立证明的 `StateRoot`、`ReceiptsRoot` 等密码学关系；
- 不兼容所有非标准 RPC；不标准的响应可以被降级或淘汰；
- 不把所有额外字段提升为经过协议语义验证的字段；
- 不要求一次实现所有 Ethereum 方法。

## 3. 两种“可信”

本设计必须区分标准语义和观测一致性。

### 标准语义可信

字段由当前 OpenRPC schema 描述，经过类型归一化、方法策略和多源裁决后，可以进入稳定的核心结果。

### 来源与一致性可信

字段没有当前标准语义，但多个通过核心检查的节点给出了相同值，或者字段只来自某个具体 Provider。系统可以可靠记录来源、支持节点和与核心结果的关联，但不应替调用方解释其业务含义。

因此扩展信息至少分为：

- `CorroboratedExtension`：多个来源提供了相同的非标准值；
- `ObservedExtension`：字段出现，但没有形成足够一致性；
- `ProviderResidual`：特定 Provider 的私有信息或冲突信息。

## 4. 建议的聚合顺序

聚合不是先用 OpenRPC 过滤响应，而是先保留和比较观测，再做标准投影：

```text
N 份原始 JSON
    ↓
严格解析与资源限制
    ↓
按 JSON path 计算结构公共集，并逐值比较
    ↓
CommonSet / 冲突字段 / Provider Residuals
    ↓
用当前 OpenRPC SchemaSnapshot 投影标准字段
    ↓
标准核心裁决 + 扩展 Carrier + 冲突证据
```

`CommonSet` 表示多个响应共同观察到的信息，不等同于标准集。OpenRPC 投影之后，才得到 `Standard Core`。

字段分类如下：

| 分类 | 处理 |
| --- | --- |
| 标准且一致 | 进入核心结果 |
| 标准但冲突 | 按方法策略失败、降级或重试 |
| 非标准且一致 | 放入 `CorroboratedExtension` |
| 非标准或值冲突 | 放入 `ObservedExtension` 或 `ProviderResidual` |
| 标准必填字段缺失 | 不能静默从交集中删除，应报告缺失 |

核心对象不得从不同节点拼装。扩展字段也必须记录支持节点和对应的核心对象指纹，避免形成没有物理来源的混合对象。

## 5. 双室归约

项目采用两个职责清晰的处理室，不引入新的大型 `consensus` 模块。

### 第一室：通用观测室

位置：现有 `rpc` 包。

它只处理与 Ethereum 业务无关的通用工作：

- 严格 JSON 解析、重复 key 检测、深度和大小限制；
- 按 JSON path 计算结构公共集；
- 对相同 path 的候选值统计支持来源；
- 保留原始值、来源、冲突值和 Provider residual；
- 生成 `CommonSet`、`CorroboratedExtension`、`ConflictField` 和 `ProviderResidual`。

它不负责判断某个字段是否是 Ethereum 标准字段，也不负责解释 `QUANTITY`、`DATA`、区块锚定或方法语义。

### 第二室：语义投影室

位置：现有 `rpc/methods` 包。

它继续负责各方法的具体语义：

- 方法核心字段集；
- 字段类型和归一化规则；
- requiredness；
- 锚定、状态和方法级错误策略；
- 到 go-ethereum 类型的投影。

标准核心的定义为：

```text
StandardCore = CommonSet ∩ MethodStandardFields
```

非标准但公共一致的字段为：

```text
CorroboratedExtension = CommonSet − MethodStandardFields
```

标准必填字段不在 `CommonSet` 时，方法策略必须报告缺失，不能静默生成部分核心对象。

## 6. OpenRPC 的作用与明确取舍

OpenRPC 在本设计中是标准字段和类型描述的来源，不是运行时每个方法的手写白名单，也不应成为可以随意改变安全策略的远程控制面。

OpenRPC 不进入生产运行路径，也不作为运行时安全裁决依据。原因是 execution-apis 仍在演进，部分 optional、类型和方法语义与实际客户端行为存在差异；动态加载还会引入网络供应链、版本漂移和额外解释器复杂度。

OpenRPC 仅作为 CI 离线参考资料，用于：

- 提醒官方规范新增或删除方法、字段；
- 对当前 `rpc/methods` 的字段声明做静态 diff；
- 生成或补充兼容性回归样例；
- 帮助开发者决定是否在锁定的 go-ethereum 版本和方法代码中纳入变化。

生产环境只依赖编译进二进制的 Go 代码和锁定版本的 go-ethereum。标准更新通过受控代码变更和发布完成，不通过运行时下载、验签或原子替换 schema 完成。

这意味着项目明确接受一个现实取舍：

- `CommonSet` 可以随 Provider 群体变化，但只被称为公共观测；
- `Standard Core` 必须由项目代码显式定义，具有稳定、可审计、可复现的语义；
- 新标准字段在项目升级前仍可作为 `CorroboratedExtension` 返回，不会被丢弃。

## 7. 健康模型与语义模型的分离

节点健康度只决定请求发送给谁、谁需要冷却以及谁在长期运行中自然胜出。健康度不改变字段的类型、标准含义或核心裁决规则。

```text
健康模型：成功率、超时、延迟、错误和长期偏差
语义模型：解析、归一化、标准投影和结果裁决
```

这允许项目继续使用现有的上游池和健康机制，而不需要把节点权重或 Provider 信任等级引入标准语义。

## 8. 需要第三方评审的问题

请重点评估以下问题，而不是先评估完整五层架构是否应该实现：

1. `CommonSet → Standard Core` 的投影边界是否清晰；
2. 方法代码声明的核心字段和 requiredness 是否足够小且可审计；
3. 通用观测室是否能在不理解 Ethereum 语义的情况下保留必要证据；
4. 标准更新时，核心结果如何通过受控版本升级保持兼容；
5. 扩展字段应该按字段一致性、来源和核心对象归属如何分级；
6. 未知方法的 `ObservedOnly` 接口和错误语义是否清晰。

## 9. 实施计划

实施遵循“先加能力、再迁移方法、最后扩大覆盖”的顺序，不推倒现有架构。

### 阶段一：冻结边界和术语

- 在 `rpc` 文档和类型注释中固定 `CommonSet`、`StandardCore`、`CorroboratedExtension`、`ProviderResidual`、`ConflictField` 的含义；
- 明确 `Verified` 只表示标准方法策略完成裁决，不表示链共识证明；
- 保持现有 `Policy`、`Observation`、`Decision` 和健康池接口不变。

### 阶段二：实现通用观测归约

- 在 `rpc` 内增加通用 JSON path 结构归约能力；
- 增加重复 key、深度、大小和非法 JSON 的防御测试；
- 为每个公共字段保留支持来源和冲突值；
- 对输入响应做拷贝，避免 Carrier 暴露可变的上游缓冲区；
- 不在这一阶段引入 Ethereum 字段白名单或 OpenRPC 解释器。

### 阶段三：接入现有方法策略

- 让已注册方法声明自己的 `MethodStandardFields` 和 requiredness；
- 复用现有 Quantity、Data、Hash、Address 和锚定逻辑；
- 将 `CommonSet` 投影为现有 go-ethereum 类型；
- 将剩余公共字段写入扩展 Carrier；
- 对标准字段冲突继续使用现有方法策略的错误和降级语义。

### 阶段四：补充扩展与未知方法接口

- 扩展字段绑定核心对象指纹、支持节点和原始值；
- 区分 `CorroboratedExtension`、`ProviderResidual` 和 `ConflictField`；
- 为未知方法提供明确的 `ObservedOnly` 或 `UntrustedCommonSet` 结果，不填充 Geth 原生类型；
- 保持普通已注册方法的现有调用方式和错误兼容性。

### 阶段五：离线规范回归

- 保留 OpenRPC 作为 CI 输入，不进入运行时；
- 对规范变化生成静态 diff 和提醒；
- 使用规范样例、真实节点响应和故障注入补充方法级回归测试；
- 由人工决定是否更新方法代码和 go-ethereum 版本。

### 阶段六：逐步扩大方法覆盖

优先覆盖已有稳定方法和高价值读取路径：区块、交易、回执、余额、调用和日志。每扩展一个方法，必须先定义其标准字段、requiredness、归一化方式和扩展字段处理，再接入通用归约。

## 10. 当前倾向

项目目前不应把这份设计直接实现成巨型 `consensus` 模块。更合适的演进路径是：

1. 在现有 `rpc` 与 `rpc/methods` seam 上实现双室归约；
2. 坚决不在生产运行时加载外部 OpenRPC schema；
3. 以锁定版本的 Go 方法代码作为标准核心的编译期基准；
4. 用 CommonSet 和 Carrier 保留标准之外的共同观测与 Provider 信息；
5. 让 OpenRPC 只服务于 CI 离线提醒和兼容性测试。

`CommonSet` 和 `Standard Core` 不使用同一个置信度名称，也不对外作同等安全承诺。

## 11. 当前实现进度

已完成：

- `rpc.AggregateFields`：通用 JSON path 观测归约；
- `Common`、`Residuals`、`Conflicts` 及来源保留；
- `rpc/methods.ProjectStandard`：方法级标准字段投影；
- 交易回执的编译期核心字段声明；
- 协同模式下将通用观测结果挂载到 `rpc.Decision.Observed`；
- 转发模式不产生 CommonSet 声明。
- 扩展 Carrier：按核心对象指纹绑定公共扩展、Provider residual 和冲突字段；
- 未注册方法的 `Decision.ObservedView()` 观察结果接口。
- 通过可选 `ObservationProjector` 将已接入方法的观测结果写入 `Decision.Extensions`；
- `eth_getTransactionReceipt` 已完成标准字段投影和扩展 Carrier 接入。
- 区块、交易、余额和调用方法已复用同一 projector 装配机制；
- 日志和 fee history 已通过 JSON Pointer 通配路径接入同一装配机制；
- 日志响应会校验请求的 blockHash 或 fromBlock/toBlock 覆盖范围；
- fee history 会校验请求 blockCount 与响应数组长度一致；

尚未完成：

- 将每个已注册方法的标准字段声明补齐；
- 对数组结果增加更严格的长度、覆盖范围和分页完整性校验；
- 为未知方法设计稳定的 `ObservedOnly` 调用接口；
- 将 OpenRPC CI diff 和方法字段声明接入自动化检查。
