# 当前路线图

状态基线：v1.17.10 完整业务方法接入仍有缺口，原生 HTTP／raw WSS 与多源订阅已有实现，但证据和部分合同尚未闭合。本次文档整理不是新的实现或验收通过声明。

## 唯一职责

- 对外完整兼容目标：[go-ethereum-compatibility-policy.md](go-ethereum-compatibility-policy.md)。不发布中间子集兼容版本。
- 每方法 Root／view／transport／证据状态：[method-transport-coverage.md](method-transport-coverage.md)。路线图不复制第二张矩阵。
- 架构与模块所有权：[codebase-topology.md](codebase-topology.md)；当前验证义务：[method-verification-pipeline.md](method-verification-pipeline.md)。
- 已接受理由与取代关系：[decisions.md](../decisions.md)；未裁决的设计与标准化候选不纳入现行合同，采纳后再并入对应永久文档。

已完成的 T 阶段施工单、agent 纪律、逐条测试日志和旧 prototype 不再作为活动任务保留；仍适用的不变量并入职责合同，历史提交由 Git 保存。当前方法自治迁移方向已确认，具体施工清单只作本地临时材料，完成后清除，不成为正式文档依赖。

## 仍需处理的工作

| 工作 | 当前边界 | 后续出口 |
| --- | --- | --- |
| 完整 Root／EthClient 契约 | SimulateV1 只有 opt-in gethcompat 单源入口，未闭合默认 root/view 的完整兼容门 | 统一官方签名／类型、导入 interlock、执行 owner、证据和生命周期，不以 unknown Call 或 ErrUnsupported 占位 |
| 返回值与官方 schema | receipt 已按决策 0068 闭合；独立 transaction 首个方法级 key 检查点已落地，本地公开路径和 Anvil 已执行；block 和新公网观察未随之完成 | 已确认双 switch 样板收敛方向，先核查职责、校验覆盖与单次修正；具体规则及已有证据见 [transaction 检查点](transaction-semantic-matching.md#12-当前实施检查点)，provider／状态证据仍按 [测试路线](external-rpc-test-framework.md#当前执行路线先最小入口双线取证逐-key-收敛) 分层取得 |
| 方法自治与归属迁移 | 两个 transaction 方法为组织样板，其余仍有集中定义；全量方案已确认，尚未启动 | 按 [拓扑](codebase-topology.md#方法自治组织样板) 小批次迁移，inventory／catalog 只收集；最多两个 agent，主控验收后续开，组织移动与语义变化分别交付 |
| typed 字段完整性 | SyncProgress 七个字段已补齐，Done() 保留上游 remaining-counter 行为；方法计数不代表其他结果已保真 | 保留可选缺失为零及 source-local Selected 边界；已验证的本地场景不能补外部 syncing 对象缺样本 |
| 跨传输与订阅 | 三类 WSS 聚合、同源 Pull、session 过滤及 newHeads gap 路径已有实现 | 补同 fixture 的 HTTP/WSS 值／错误／证据／生命周期矩阵；先统一订阅 Close、单源、budget、generation 等 S1 问题 |
| freshness | 请求范围、旧样本、采样覆盖与默认策略仍需决定 | 按 F1 区分调度／候选规则，验证历史／pending 请求和样本年龄，不顺手改 quorum 或默认常数 |
| 发布与外部观察 | 本地测试、API 差异、历史 provider 观察证明不同事情 | 执行当前 [release-contract.md](release-contract.md) 的完整门禁和明确的真实观察，不以文档完成或 workflow success 代替兼容 complete |

上述问题的列举不预先授权新抽象、固定配额、重试或范围缩减。实现顺序由真实依赖和评审结果决定；不因过去阶段编号强制继续旧施工顺序。

### 下一轮优先执行

1. 先分批保存已有改动，取得干净源码基线。恢复迁移时先盘点方法全集及验证状态，再闭合命名、字段校验覆盖、职责收敛、双 switch 及 timestamp 单次处理；不因计划已确认就标记任务完成。
2. 样板验收后分批迁移其余方法，组织调整不改变既有 resolver、reference、pending 或输出合同；新语义规则独立评审、交付正反例，无法兼容则拒绝，不向 key 内局部语义扩展。
3. 按实际缺口执行真实 provider 定点采集及必要状态验证；既有无 fork Anvil 的 legacy pending → mined 与恢复隔离不能替代公网证据。内嵌 transaction 独立接入，不继承 standalone 的派生规则。
4. 每批都可在具体代码与规则验收后停止；最后完成跨模块验收、实测性能及过渡定义清理。不要求一次推进到全部完成，临时施工清单不替代正式合同和覆盖状态。

工具实现、产物规则与依赖细节只在 [测试框架设计](external-rpc-test-framework.md) 维护。真实历史 fork／moving tags／EIP-1898 仍需单独完成，不把本地链成功冒充这些专项或真实 provider 兼容性。

## 不变的验收边界

一次调用的成员／分母不缩水，失败不降门槛；一个 runtime owner、一个 typed 实现和清楚的 adapter seam。Corroborated 是 provider-backed cross-source evidence，不是 Ethereum consensus、canonical proof 或 cryptographic finality。

行为变更按受影响场景验证；发布再执行完整 normal／race／vet／release 门禁。仅文档整理不重新冒领过去的测试、provider 或远端发布结果。未观察项、兼容缺口和被拒绝的方案必须明确保留，而不是用新章节标题宣告完成。

架构收敛的最终验收包含 `go test ./... -race -count=1`，其余命令与发布条件沿用 [发布合同](release-contract.md)。这是后续执行要求，不是本阶段已通过的声明。
