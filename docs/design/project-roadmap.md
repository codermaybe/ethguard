# 项目路线图

更新：2026-11-16。

EthGuard 面向第三方 Ethereum execution RPC，提供保留方法语义的多源读取、明确的结果状态和可审阅的来源证据。公开业务类型已统一到官方来源的生成模型；后续继续完善逐方法语义和兼容性证据，并收敛稳定发布。

## 当前状态

| 方向 | 已具备 | 下一里程碑 |
| --- | --- | --- |
| 方法覆盖 | 锁定 go-ethereum v1.17.10 的 47 项目标方法名称集合；27 个注册读 RPC，以及独立的写入、订阅和派生能力 | 完成逐入口的参数、结果、错误和生命周期语义验收；方法计数不代表完整兼容 |
| 官方业务模型 | 目标方法及 root 扩展入口的公开业务模型已迁至 `ethapi`，写入使用 `BinaryTransaction` 载体；公开签名与调用方整体迁移已验收 | 按统一来源审计与生成流程维护类型合同，后续签名变更明确兼容影响 |
| 规范来源 | 官方 execution-apis 固定 commit 基线、独立原始向量、三项显式标准勘误，以及 Simulate 请求所需的通用生成约束 | 按其他方法的实际需要扩充生成覆盖，持续审查上游变化和勘误退出条件 |
| SimulateV1 | native root、EthClient view 和 Raw Call，生成请求／结果和完整 selector 模型 | 完善模拟块、内层结果、顶层错误及真实 provider 行为的逐项语义与证据验收 |
| HTTP / WSS | 原生 HTTP 和 raw WSS 请求能力，WSS 订阅及同源补取 | 完善相同输入的跨传输验证，以及订阅乱序、背压、重连、取消和资源释放证据 |
| 结果与证据 | 业务结果、决策、附属材料和按调用预算保留的原始来源材料 | 完善字段完整性、错误 presence、历史分叉和真实 provider 观察 |

公开业务类型迁移已完成。签名采用项目生成模型，密码学与内容承诺所需的 geth 类型保留在私有算法桥；公开签名与 `*ethclient.Client` 的 Go 类型身份不同，依赖 geth 接口的调用方需显式适配。方法名称集合闭合与类型迁移完成不代表稳定发布完成。

## 近期重点与执行阶段

已完成的前置工作：固定官方来源与生成审计基础、扩充 FilterQuery／TransactionReceiptsQuery 请求生成、建立 Header 公开模型、统一公开标量／块引用校验及写入 BinaryTransaction 载体；公开类型签名及调用方分批迁移已验收。旧阶段一设计盘点与三批签名迁移不再作为待执行任务；后续类型修正仍须经过统一来源审计与生成流程。

1. **单收据 canonical 结果复用（已完成本任务）。** `eth_getTransactionReceipt` 的编译计划已声明 `ProcessCanonical`：`PrepareTransaction` 保持原准入顺序（null 短路、请求解析、`DecodeWithCanonical` 的 Encode 与私有 geth 检查、交易绑定），`processReceipt` 直接提交同一份已验证 canonical bytes，不再串接 Admit 与 resolver Encode；raw 路径的 `Admit`、KeySource、接受域和 Forwarded／pending 回退保持不变。对象与方法合同回归、真实 Aggregator 对照（状态、值、支持／拒绝成员、代表来源、错误类别、来源故障、健康记账、诊断、扩展与原始材料）、race 和 vet 已通过；五次微基准与真实 Aggregate 测量显示分配和 CPU 改善（微基准 479 对 931 allocs/op，端到端约 4019 对 5386 allocs/op）。覆盖范围仅限该方法的 strict canonical 路由；其他方法、F1／S1 专项与发布门禁未推进，本任务完成不代表对象内核路线或发布完成。
2. **按方法族推进对象内核收敛。** 试点通过并复核后，分别评估其他 `ResolverStrict` 方法的前置兼容、官方生成模型、方法绑定与私有密码学承诺；先核对 FeeHistory／AccessList 的手写 canonical 与生成模型职责。不要把已采用生成投影的 Block／Header／Log／Receipt 路径重新迁移，也不要按文件批量删除方法级约束。`ProcessCanonical` 目前仅允许 strict resolver 与 canonical-bytes 键；Block 使用 `ResolverBlock`，须另行审定参考和决策语义，不能直接套用 strict 路径。跨来源比较键、错误归因和保留证据必须与原合同等价。
3. **逐方法补齐兼容与运行证据。** 依据 [方法与传输覆盖](method-transport-coverage.md) 分别闭合 SimulateV1 模拟块／内层结果／顶层错误、同输入 HTTP／WSS、订阅生命周期、真实 provider 样本及可复现成本测量。离线回归、本地链与真实 provider 观察独立记账；方法数、生成新鲜度或单一试点通过都不等于完整兼容。
4. **独立处理运行期合同。** [设计问题清单](../tmp/design-issues.md)的 F1（freshness tip-gate 适用范围、样本年龄）与 S1（订阅 Close／overflow 等状态机语义）不依赖再次迁移公开签名；各自形成专项任务与行为证据，不以对象内核重构为前置条件。
5. **稳定发布审查。** 完成逐方法字段、错误、引用、生命周期与证据缺口后，按 [发布合同](release-contract.md)运行正式门禁并审查发布证据；历史门禁不代替目标提交的结果。

## 公开类型接口设计与分批迁移（已完成）

公开请求、结果与调用方的类型迁移已完成并通过整体验收，统一生成模型、对象输出合同与共享完整性校验已接通。迁移保留现有执行骨架及签名／哈希／内容承诺防线；后续接受域和保护范围的必要调整仍单独审查，经统一来源与生成流程落实。

## 发布条件与保证范围

稳定发布需要逐方法语义、生成类型、字段交付、错误分类、生命周期及兼容性证据共同满足合同。离线回归、本地链测试与真实 provider 观察分别记账，未覆盖的场景保持明确列缺。

`Corroborated` 表示 provider-backed cross-source evidence（配置来源之间的佐证），不是 Ethereum consensus、canonical chain 或 cryptographic finality 的证明。Selected、Forwarded、pending 本地观察、写入和订阅各自保留其声明的保证范围。

## 相关文档

- [方法与传输覆盖](method-transport-coverage.md)：公开入口、能力和证据缺口。
- [兼容政策](go-ethereum-compatibility-policy.md)：目标方法面与公开类型迁移。
- [官方类型生成](wire-codegen.md)：固定来源、标准勘误和生成模型。
- [方法合同](method-contracts.md)与[返回值合同](return-value-design.md)：方法语义、保护范围和数据交付。
- [架构](codebase-topology.md)、[订阅合同](subscription-contract.md)与[发布合同](release-contract.md)。
