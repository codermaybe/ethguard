# Roadmap Execution Ledger

状态：执行中（2026-10-31）。本文件按业务语义记录当前路线图各项工作的证据、
状态、阻塞、风险和待最终处理问题，是 `project-roadmap.md` 的执行侧台账。

纪律：

- 本台账只登记事实和执行状态，不复制路线图的任务表，也不替代决策文件；
- 条目使用业务语义名称，路线图编号仅作为交叉引用；
- 每个条目写明：证据、状态、依赖/阻塞、风险、待最终处理问题；
- 发现的偏差先登记在这里，能继续的工作继续推进，不因单个问题停住；
- 收尾时按“已解决 / 可接受限制 / 需要主维护者决策 / 外部证据阻塞”分类汇总。

## 1. Resolver 深模块裁决（route: resolver seam, RS5）

- 状态：已完成。
- 证据：
  - `docs/design/resolver-behavior-inventory.md`（23 个方法的 resolver 事实）；
  - `docs/design/resolver-seam-contract.md`（seam 归属、能力矩阵、CallRevert
    Outcome、feeHistory 边界、blockPolicy 裁决）；
  - `rpc/methods/resolver_capability_interlock_test.go`、
    `aggregator/resolver_capability_interlock_test.go`、
    `internal/runtime/legacy_quarantine_interlock_test.go`。
- 裁决：保留 `rpc.Policy` 为 resolver 深模块，不为“零 decorator”继续拆分。
  记录见 `docs/decisions.md` 0063 与 `resolver-seam-contract.md` §8。
- 风险：Policy 上的可选能力接口继续增加时，计划路径的 checked read 可能变成
  第二套能力注册表；`blockPolicy` 若重新携带请求阶段会破坏本裁决。
- 待最终处理问题：
  - 需要主维护者决策：未来出现新的 resolver 能力时，继续扩展可选接口还是
    引入显式 ResolverPlan（当前无触发证据，不预先设计）。

## 2. Strict-head malformed 响应的原始字节采集（route: reference raw capture）

- 状态：已完成。
- 证据：
  - `internal/runtime/strict_head.go` 在 `jsonsafe.Validate` 清空前保留
    emission 的原始值副本；`rawEmissions` 把 post-admission 的 fault 与原始
    值配对，`reference_stage.go` 用该副本记录 InvocationCapture；
  - 回归测试 `internal/runtime/strict_head_raw_capture_test.go`：malformed
    场景原始字节逐成员可读、错误文本保留 admission 分类、业务 fan-out 为零；
    well-formed 场景每成员恰好一条原始记录。
- 语义：admission 仍然清空工作值，malformed 响应不能赢得聚类；只影响
  `InvocationCapture` 的证据，不改变 Decision、quorum、reference、typed。
- 风险：采集副本与工作 emission 的生命周期必须保持对齐；新增 admission
  清空点时若在复制之后发生，需要同步更新采集副本。
- 待最终处理问题：无（当前契约内已闭合）。

## 3. Finalized reference 轮次的采集（route: reference raw capture）

- 状态：已完成。
- 证据：
  - `internal/runtime/captured_head_reader.go` 在 runtime 侧包装
    `reference.HeadReader`，记录 `Read`/`Confirm` 的 observation 与 collect error；
  - 回归测试 `internal/runtime/finalized_head_raw_capture_test.go`：selection
    轮与 confirmation 轮的原始 head 都进入 InvocationCapture，来源与高度按
    算法顺序登记；`internal/runtime/captured_head_reader_test.go`：两轮
    observation 与 collect error 的包装契约。
- 语义：不修改 `reference.HeadReader` 签名、`reference.Finalized` 算法、阈值、
  成员、Decision 或 typed；采集沿用 `internal/uncertain` 的有界预算。
- 风险：包装器只应记录 observation，不得参与错误分类或决定失败；确认轮次
  与首轮共用同一 stage 名称，来源顺序由算法决定。
- 待最终处理问题：
  - 可接受限制：`reference.Finalized` 的 participant/selection 诊断仍只通过
    `ReferenceEvidence` 暴露，不额外新增结构化采集字段。

## 4. WSS 调用层可行性评估（route: WSS evidence gate, W1）

- 状态：执行中（离线审查与进程内验证）。
- 证据（待补全）：
  - go-ethereum v1.17.5 `ServerCodec` 的未导出方法集；
  - `decodeError` 的坍缩点复核；
  - 进程内最小 WSS 验证（仅测试，不进入生产构建）。
- 待决问题：request ID demux、取消、read limit、关闭、inflight 清理、
  断连与 presence 的实现责任归属，以及自建层与 geth 升级的维护成本。

## 5. 最小 WSS raw-call Adapter（route: WSS evidence gate, W2）

- 状态：等待第 4 项证据。
- 约束：只实现 raw call；不加入 Subscribe、filter、通用客户端或写路径重连；
  WSS 代码不得混入 resolver、method plan、quorum 或 reference 提交。

## 6. WSS 进程内契约证据（route: WSS evidence gate, W3）

- 状态：等待第 5 项。
- 约束：missing/null/`0x`/具体 data、malformed frame、read limit、
  cancellation、close、断连和资源释放全部通过后，才允许修订决策 0056 或
  增加 WSS golden。

## 7. 逐方法对接卡片与 provider 证据（route: method integration）

- 状态：进行中。
- 计划：按 roadmap 顺序（确定性/本地、anchored state、对象数组、兼容边界）
  为 23 个 RegisteredMethod 建立业务语义卡片；provider 证据等级与离线回归
  分开登记，不用合成样本冒充兼容性证据。

## 8. 自适应拥塞控制设计（route: T11）

- 状态：执行中（只补设计与不变量）。
- 约束：不改固定成员、分母、权重、模式、acceptance threshold；不与方法或
  WSS 提交混合。

## 9. Reference freshness（route: reference policy）

- 状态：执行中（只补设计与不变量）。
- 约束：不静默改 tag 语义；不改 reference 算法、成员或阈值；
  freshness 失败意味着拒绝而非降级。

## 10. 已登记的遗留问题

| 问题 | 状态 | 证据/影响 | 处理 |
| --- | --- | --- | --- |
| WSS missing/null presence 仍不可知 | 开放（外部证据阻塞） | 决策 0056；W0 只表达 unknown | W1–W3 完成且真实 provider 证据通过后再修订 |
| geth WSS 自动重连语义 | 开放（需主维护者决策） | 替换 transport 时不得无声丢失 | WSS Adapter 明确不实现重连，保留为独立契约 |
| `eth_syncing` progress object provider 形状 | 开放（外部证据阻塞） | 只有 sample/assumed 证据 | 保持 capture gap，不用合成样本关闭 |
| liveness/typed 的对象形状证据 | 开放（外部证据阻塞） | inventory 标注 assumed | 逐方法 provider capture 补齐 |

## 11. 收尾分类（待完成报告汇总）

- 已解决：见 §1–§3。
- 可接受限制：见 §3、§10。
- 需要主维护者决策：见 §1、§10（WSS 重连）。
- 外部证据阻塞：见 §10（WSS presence golden、provider 形状）。
