# 项目路线图

更新：2026-11-14。

EthGuard 面向第三方 Ethereum execution RPC，提供保留方法语义的多源读取、明确的结果状态和可审阅的来源证据。当前重点是统一官方业务类型、完善逐方法语义和兼容性证据，并在这些工作完成后收敛稳定发布。

## 当前状态

| 方向 | 已具备 | 下一里程碑 |
| --- | --- | --- |
| 方法覆盖 | 锁定 go-ethereum v1.17.10 的 47 项目标方法名称集合；27 个注册读 RPC，以及独立的写入、订阅和派生能力 | 完成逐入口的参数、结果、错误和生命周期语义验收；方法计数不代表完整兼容 |
| 官方业务模型 | 公开 `ethapi` 生成类型；CallMsg、FeeHistory、AccessListResult 和 SyncProgress 已接入部分公开入口 | 按方法族迁移其余请求、结果、地址、hash 和 selector，明确公开签名的迁移影响 |
| 规范来源 | 官方 execution-apis 固定 commit 基线、独立原始向量、三项显式标准勘误，以及 Simulate 请求所需的通用生成约束 | 按其他方法的实际需要扩充生成覆盖，持续审查上游变化和勘误退出条件 |
| SimulateV1 | native root、EthClient view 和 Raw Call，生成请求／结果和完整 selector 模型 | 完善模拟块、内层结果、顶层错误及真实 provider 行为的逐项语义与证据验收 |
| HTTP / WSS | 原生 HTTP 和 raw WSS 请求能力，WSS 订阅及同源补取 | 完善相同输入的跨传输验证，以及订阅乱序、背压、重连、取消和资源释放证据 |
| 结果与证据 | 业务结果、决策、附属材料和按调用预算保留的原始来源材料 | 完善字段完整性、错误 presence、历史分叉和真实 provider 观察 |

公开类型仍在迁移。部分方法已使用项目生成类型，其他方法仍使用 geth 业务类型；当前不承诺与 `*ethclient.Client` 的全部 Go 类型身份一致，也不把方法名称集合闭合视作稳定发布完成。

## 近期重点

1. **维护一致的官方规范来源。** 从固定官方来源重建 schema 与原始向量，复核必要勘误，按实际方法需要扩充类型生成覆盖。
2. **完善 SimulateV1 方法语义。** 在生成请求与 selector 基础上核对模拟块、结果、错误和参数行为，补充真实 provider 证据；保留原始官方数据与项目接受政策的区别。
3. **按方法族完成公开类型迁移。** 依次处理标量／DATA、调用／费用、transaction／receipt／block、写入／订阅；保留签名、哈希、对象关系和内容承诺检查。
4. **完善兼容性与运行证据。** 补齐跨传输行为、订阅生命周期、真实 provider 样本和可复现性能测量，再完成发布所需的质量检查。

## 发布条件与保证范围

稳定发布需要逐方法语义、生成类型、字段交付、错误分类、生命周期及兼容性证据共同满足合同。离线回归、本地链测试与真实 provider 观察分别记账，未覆盖的场景保持明确列缺。

`Corroborated` 表示 provider-backed cross-source evidence（配置来源之间的佐证），不是 Ethereum consensus、canonical chain 或 cryptographic finality 的证明。Selected、Forwarded、pending 本地观察、写入和订阅各自保留其声明的保证范围。

## 相关文档

- [方法与传输覆盖](method-transport-coverage.md)：公开入口、能力和证据缺口。
- [兼容政策](go-ethereum-compatibility-policy.md)：目标方法面与公开类型迁移。
- [官方类型生成](wire-codegen.md)：固定来源、标准勘误和生成模型。
- [方法合同](method-contracts.md)与[返回值合同](return-value-design.md)：方法语义、保护范围和数据交付。
- [架构](codebase-topology.md)、[订阅合同](subscription-contract.md)与[发布合同](release-contract.md)。
