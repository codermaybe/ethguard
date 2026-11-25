# 官方业务语义与 go-ethereum 覆盖政策

状态：2026-11-23 NewHead接口合同已同步。公开业务类型与调用方迁移已完成，方法名称集合闭合；逐方法语义、跨传输和运行证据仍待完整验收。

## 1. 兼容目标与当前状态

业务方法参考为 `go.mod` 锁定的 go-ethereum v1.17.10。其 `ethclient.Client` 有 48 个导出 receiver 方法；底层 `Client()` 访问器明确排除，目标为剩余 47 项（含 Close）。`scripts/ethclient-method-diff.sh` 检查名称集合，完整签名由 `TestEthClientBusinessSignatures` 按项目类型合同检查；两者均不证明逐方法行为全部兼容。

2026-11-25重新运行名称差异与签名检查，业务缺失为0。该分母不包含包级Dial/DialContext/NewClient/RevertErrorData，也不覆盖参数接受域差异。构造与传输、pending选择、同步发送机制及订阅用法的实际映射见[覆盖清单](method-transport-coverage.md#go-ethereum-v11710-官方-ethclient-方法)。其中项目RevertError只提供DataError而没有ErrorCode；已实际确认上游RevertErrorData不能直接消费它，应用应通过errors.As读取项目Data/DataRaw，不把错误data存在等同上游helper兼容。

公开 JSON 业务模型以固定 execution-apis 原始来源及审核勘误为源，由 `ethapi` 生成。标量输入为 `ethapi.Address`／`ethapi.Hash` 等；Block、Header、Receipt、Transaction、Log、查询请求和 Simulate 使用项目生成模型。数值、context、错误和订阅生命周期接口按各自职责保留。写入接受 `BinaryTransaction`（提供 MarshalBinary 的载体），可消费 geth transaction，但不把 geth 业务对象重新作为公共模型。

项目不承诺 geth Go 名义类型身份或其接口的直接满足。`EthClient` 具有相应业务方法形态，使用项目生成类型；依赖 `bind/v2` 等 geth 接口的调用方需显式适配。当前没有“未迁移的公开业务类型”施工阶段；内部重复定义属于下一阶段实现审查，不能与公开迁移混记。

`SyncProgress` 仅表达官方 startingBlock／currentBlock／highestBlock 三个 optional 字段，false 映射为未同步对象；不提供 geth Done()。geth 额外同步和索引计数通过附属及预算内原始材料交付。真实 syncing 对象观察仍有缺口。

`SimulateV1` 的 root、view 与 Raw Call 共用注册计划。强类型入口使用 `ethapi.SimulateV1Request`、`*ethapi.SimulateBlockReference` 和 `[]ethapi.SimulateBlock`；nil base 默认 latest。生成校验、输入快照与官方 selector 处理不替代模拟块、内层失败、顶层错误或真实 provider 的验收。

## 2. 两个 facade，一个执行 owner

Root Client返回业务值、单次`CallResult`和业务错误；EthClient view投影同一执行并省略CallResult。NewHead两入口均接受`HeadSubscriptionPolicy`并返回`HeadSubscription`，通过Next/Basis/Close交付连续路径、重组与来源证据；其签名与上游channel接口不同。Logs使用`LogsSubscriptionPolicy`与`LogsSubscription.Next/Basis/CommittedTip/Close`，交付完整实时批次并显式处理撤回，2026-11-24本地验收通过。Receipts已统一为`ReceiptsSubscriptionPolicy`与`ReceiptsSubscription`，提供相同交付操作，以完整块证明和固定Q字段声明交付完整收据批次；2026-11-25本地验收通过。三种流均不保留channel/`ethereum.Subscription`兼容桥。流式合同独立，不把unary CallResult延伸为订阅事件，详见[订阅合同](subscription-contract.md)。

view 不提供 `Client()` 逃生口。runtime 是唯一生产装配点，typed projection 只有一份；未知公开 Call 本地拒绝，不能通过默认 forwarding 补齐缺失方法。低层 rpc Registry 的 forwarding 能力不等于公开客户端授权。

生产 geth adapter 与公共 gethcompat 已退出。`internal/testkit/gethoracle` 仅供差分测试和显式诊断；历史 geth 观察仍是当时的 adapter 证据，不改标为 native。私有 geth 算法桥仍用于签名、hash、bloom 和内容承诺，不承担公开业务模型的权威定义。

## 3. 官方语义与数据交付

官方定义、有限生态兼容和项目额外接受限制分别记录。生成形态不替代请求绑定与算法检查；多个来源一致也不能把非法输入投成有效对象。主 Value、附属比较与预算内原始材料的职责由 [返回值合同](return-value-design.md)统一维护。

HTTP/WSS request、订阅、写入和派生入口分别验收；普通 WSS 调用能力不推出订阅兼容。固定成员、来源身份与 provider-backed evidence 不提供链共识、canonicality、finality 或 provider 独立性证明。

## 4. 升级与发布

1. 升级固定 geth 参考时重建方法差异，并明确项目公开签名与官方业务语义的映射。
2. 官方字段变更沿 [生成合同](wire-codegen.md)审计原始来源、勘误、模型与消费者；不得手改生成物或补平行 DTO。
3. 逐入口核对请求、结果字段、错误、reference、HTTP/WSS、证据与生命周期；订阅另核对重连、背压、取消和资源回收。
4. 完成 [覆盖清单](method-transport-coverage.md)中的缺口后，对目标提交执行 [发布门禁](release-contract.md)。方法计数、构建或一次 provider 观察都不能代替完整验收。

完整对外声明必须同时说明业务方法参考版本、固定 execution-apis 来源、项目生成业务类型、未承诺 geth Go 类型身份，以及已验收的传输／生命周期范围。当前仍是实现与证据完善阶段，不发布“已接入子集即完整兼容”的声明。
