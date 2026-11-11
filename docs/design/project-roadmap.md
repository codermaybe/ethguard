# 当前路线图

状态基线：v1.17.10 完整业务方法接入仍有缺口，原生 HTTP／raw WSS 与多源订阅已有实现，但证据和部分合同尚未闭合。本次文档整理不是新的实现或验收通过声明。

## 唯一职责

- 对外完整兼容目标：[go-ethereum-compatibility-policy.md](go-ethereum-compatibility-policy.md)。不发布中间子集兼容版本。
- 每方法 Root／view／transport／证据状态：[method-transport-coverage.md](method-transport-coverage.md)。路线图不复制第二张矩阵。
- 架构与模块所有权：[codebase-topology.md](codebase-topology.md)；当前验证义务：[method-verification-pipeline.md](method-verification-pipeline.md)。
- 已接受理由与取代关系：[decisions.md](../decisions.md)；未裁决的设计与标准化候选不纳入现行合同，采纳后再并入对应永久文档。

已完成的 T 阶段施工单、agent 纪律、逐条测试日志和旧 prototype 不再作为活动任务保留；仍适用的不变量并入职责合同，历史提交由 Git 保存。

## 仍需处理的工作

| 工作 | 当前边界 | 后续出口 |
| --- | --- | --- |
| 完整 Root／EthClient 契约 | SimulateV1 只有 opt-in gethcompat 单源入口，未闭合默认 root/view 的完整兼容门 | 统一官方签名／类型、导入 interlock、执行 owner、证据和生命周期，不以 unknown Call 或 ErrUnsupported 占位 |
| 返回值与官方 schema | receipt 已按决策 0068 闭合；独立 transaction 首个方法级 key 检查点已落地，本地公开路径和 Anvil 已执行；block 和新公网观察未随之完成 | 当前批次收束，先审阅 [具体实现、规则及验收边界](transaction-semantic-matching.md#12-当前实施检查点)，不自动扩面；后续仍按 [测试路线](external-rpc-test-framework.md#当前执行路线先最小入口双线取证逐-key-收敛) 分别取得 provider／状态证据，不引入新策略框架 |
| typed 字段完整性 | SyncProgress 七个字段已补齐，Done() 保留上游 remaining-counter 行为；方法计数不代表其他结果已保真 | 保留可选缺失为零及 source-local Selected 边界；已验证的本地场景不能补外部 syncing 对象缺样本 |
| 跨传输与订阅 | 三类 WSS 聚合、同源 Pull、session 过滤及 newHeads gap 路径已有实现 | 补同 fixture 的 HTTP/WSS 值／错误／证据／生命周期矩阵；先统一订阅 Close、单源、budget、generation 等 S1 问题 |
| freshness | 请求范围、旧样本、采样覆盖与默认策略仍需决定 | 按 F1 区分调度／候选规则，验证历史／pending 请求和样本年龄，不顺手改 quorum 或默认常数 |
| 发布与外部观察 | 本地测试、API 差异、历史 provider 观察证明不同事情 | 执行当前 [release-contract.md](release-contract.md) 的完整门禁和明确的真实观察，不以文档完成或 workflow success 代替兼容 complete |

上述问题的列举不预先授权新抽象、固定配额、重试或范围缩减。实现顺序由真实依赖和评审结果决定；不因过去阶段编号强制继续旧施工顺序。

### 下一轮优先执行

1. 当前不再新开实施批次，先由用户审阅独立 transaction 的集中 key 规则、生成链定点修正和既有健康接线；检查点不等于整体兼容完成。
2. 经确认后再执行真实 provider 定点采集，按实际缺口补其他交易类型／状态；本轮无 fork Anvil 的 legacy pending → mined 与恢复隔离已执行，不能替代公网证据。
3. 仅对明确 key 形态新增代码和正反例，无法兼容就拒绝；独立交易整体验收后才接 block 内嵌交易，不向 key 内局部语义扩展。

工具实现、产物规则与依赖细节只在 [测试框架设计](external-rpc-test-framework.md) 维护。真实历史 fork／moving tags／EIP-1898 仍需单独完成，不把本地链成功冒充这些专项或真实 provider 兼容性。

## 不变的验收边界

一次调用的成员／分母不缩水，失败不降门槛；一个 runtime owner、一个 typed 实现和清楚的 adapter seam。Corroborated 是 provider-backed evidence，不是 Ethereum consensus、canonical proof 或 cryptographic finality。

行为变更按受影响场景验证；发布再执行完整 normal／race／vet／release 门禁。仅文档整理不重新冒领过去的测试、provider 或远端发布结果。未观察项、兼容缺口和被拒绝的方案必须明确保留，而不是用新章节标题宣告完成。
