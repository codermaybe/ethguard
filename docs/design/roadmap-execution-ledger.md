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

- 状态：已完成（结论：W1 通过，允许进入 W2）。
- 证据：
  - `docs/design/wss-call-layer-feasibility.md`：geth v1.17.5 公开接口复核、
    三条路线比较、责任矩阵、维护成本与停止条件；
  - `internal/transport/wss_feasibility_test.go`：进程内最小原型验证
    demux、取消、read limit、Close、断连、通知、presence 四态与 ChainID；
  - `go.mod` 将 `gorilla/websocket` 提升为直接依赖（已在 go-ethereum
    依赖图中，无新增模块）。
- 裁决：自建最小 WSS raw-call 调用层可行；geth 没有取得原始 error
  envelope 的公开扩展点；不使用反射、fork 或未导出类型。
- 风险：自建层拥有连接生命周期与 demux；geth 的自动重连/ping 语义不会
  自动保留，生产切换前必须另立契约。
- 待最终处理问题：
  - 需要主维护者决策：ping/pong keepalive 与自动重连是否进入 W2 或独立契约
    （当前 W2 明确不实现）。

## 5. 最小 WSS raw-call Adapter（route: WSS evidence gate, W2）

- 状态：已完成。
- 证据：
  - `internal/transport/wss/wss.go`：`transport.Transport`/`Dialer` 实现，
    复用 `rpcwire` 编解码；只含 raw call、`ChainID`、`Close`；
  - 进程内契约测试覆盖单请求、乱序、取消、超时、断连、read limit、
    Close、pending 清理、四态 presence、通知、无自动重试和 pool seam 集成；
  - 顶层 architecture interlock 禁止 WSS 包导入 aggregator、rpc/methods、
    internal/runtime、internal/reference、internal/typed。
- 风险：单 socket 的 Close 无法让在途调用继续；与 `transport.go` 通用注释
  的偏差已登记，生产切换前需要主维护者裁决。
- 待最终处理问题：
  - 需要主维护者决策：Close 的 in-flight 语义、自动重连、keepalive；
  - 外部证据阻塞：真实 provider 的 WSS 行为尚未采集。

## 6. WSS 进程内契约证据（route: WSS evidence gate, W3）

- 状态：已完成（仅 raw-call Adapter 范围）；决策 0056 未修订，WSS 四态
  golden 继续禁止。
- 证据：
  - `internal/transport/wss/wss_contract_test.go`：missing/null/`0x`/具体
    data、malformed frame、read limit、cancellation、Close、断连、pending
    清理、资源生命周期；
  - `docs/design/wss-transport-adapter.md`：语义、非目标、证据与未决问题。
- 未完成出口：真实 provider/进程内证据都通过才能升级 0056；reconnect、
  reorg、backpressure 与真实 WSS provider capture 仍缺。
- 待最终处理问题：
  - 外部证据阻塞：WSS 四态 golden、真实 provider WSS capture；
  - 需要主维护者决策：reconnect/reorg/backpressure 的契约归属。

## 7. 逐方法对接卡片与 provider 证据（route: method integration）

- 状态：进行中（23 张卡片已建立；provider 证据缺口按卡片登记）。
- 证据：`docs/design/method-integration-cards.md` 按 23 个 RegisteredMethod
  记录 root、ethadapter、typed、错误、pending、EIP-1898、reference 与证据
  等级；区分 adapter 支持、root-only 与未验证项。
- 风险：卡片中的 `soak`/`sample` 是历史观测，不是长期兼容性证明；
  `eth_blobBaseFee` 等仍为 unverified。
- 待最终处理问题：
  - 外部证据阻塞：`eth_blobBaseFee`、`eth_syncing` progress object、
    EIP-1898 object 形状、hash 位置 getBlockByNumber、createAccessList、
    feeHistory percentile 舍入、WSS provider、完整区块内容承诺；
  - 需要主维护者决策：是否以及何时为这些缺口安排真实 capture。

## 8. 自适应拥塞控制设计（route: T11）

- 状态：设计不变量已完成；算法未实现。
- 证据：`docs/design/adaptive-congestion-control.md` 固定成员/分母/权重/
  模式/门槛不变，定义信号、状态、预算共享、恢复和验收矩阵；不选择参数。
- 风险：真实 provider 容量与限流形态仍缺；如果实现要求动态成员或权重，
  必须停止在调度层。
- 待最终处理问题：
  - 需要主维护者决策：算法族、margin/冷却/队列参数、跨 endpoint 配额与
    写路径预算归属；
  - 外部证据阻塞：真实限流/容量样本。

## 9. Reference freshness（route: reference policy）

- 状态：设计不变量已完成；新策略未实现。
- 证据：`docs/design/reference-freshness-policy.md` 区分调度 tip gate 与
  finalized 候选 lag，固定 tag 语义、拒绝而非降级、显式历史读取豁免与
  agreement/freshness 分离；不选择默认 lag。
- 风险：当前 `maxLag`/tip gate 是实现事实而非新默认承诺；改参数需要专门
  决策和真实 provider 观测。
- 待最终处理问题：
  - 需要主维护者决策：lag 单位、方法适用范围、样本过期语义、是否自适应；
  - 外部证据阻塞：多 provider 高度/滞后/reorg 观测。

## 10. 已登记的遗留问题

| 问题 | 状态 | 证据/影响 | 处理 |
| --- | --- | --- | --- |
| WSS missing/null presence 仍不可知 | 开放（外部证据阻塞） | 决策 0056；geth 路径只表达 unknown；WSS raw-call Adapter 已能保留四态，但未接入默认 runtime 且无真实 provider 证据 | W4 在 provider 证据完整后再修订 |
| geth WSS 自动重连语义 | 开放（需主维护者决策） | 替换 transport 时不得无声丢失；WSS Adapter 明确不重连 | 生产切换前另立重连/失败分类契约 |
| WSS Close 的 in-flight 语义 | 开放（需主维护者决策） | 单 socket 关闭使 pending 失败，与 `transport.go` 通用注释不完全一致 | 决定是否保留或另立 WSS 生命周期契约 |
| `eth_syncing` progress object provider 形状 | 开放（外部证据阻塞） | 只有 sample/assumed 证据 | 保持 capture gap，不用合成样本关闭 |
| liveness/typed 的对象形状证据 | 开放（外部证据阻塞） | inventory 标注 assumed | 逐方法 provider capture 补齐 |

## 11. 收尾分类（2026-10-31）

- **已解决**：
  - resolver 深模块裁决（决策 0063、契约 §8）；
  - strict-head malformed 原始字节采集；
  - finalized reference `Read`/`Confirm` 采集；
  - WSS 调用层可行性（W1）与最小 raw-call Adapter（W2/W3，进程内）；
  - 23 个方法的业务语义对接卡片；
  - T11 与 reference freshness 的设计和不变量。
- **可接受限制**：
  - WSS Adapter 不实现订阅、批量、自动重连、主动 keepalive；生产切换前另立契约；
  - WSS 取消发生在已阻塞的写调用时，只有调用 deadline 能中断写；
  - finalized reference 的 participant/selection 诊断仍只通过
    `ReferenceEvidence` 暴露；
  - WSS Adapter 未接入默认 runtime/config，作为独立配置决策保留；
  - WSS 进程内证据不升级决策 0056，不生成 WSS 四态 golden。
- **需要主维护者决策**：
  - WSS Close 对 in-flight 调用的语义（当前失败 pending，与
    `transport.go` 通用注释存在偏差）；
  - WSS reconnect/reorg/backpressure 与 keepalive 的契约归属；
  - 是否以及何时把 WSS 接入配置/runtime；
  - T11 算法族、margin/冷却/队列参数与预算范围；
  - reference freshness 的 lag 单位、方法适用范围和是否自适应；
  - 真实 provider capture 的排期。
- **外部证据阻塞**：
  - `eth_blobBaseFee` 存在性/形状、`eth_syncing` progress object；
  - EIP-1898 object 形状；`eth_getBlockByNumber` hash 位置；
  - `eth_createAccessList` 兼容性；`eth_feeHistory` percentile 舍入；
  - 完整区块内容承诺（uncle/withdrawals/交易根）；
  - WSS 真实 provider 的 error envelope、限流、消息大小与重连；
  - T11 容量/限流样本与 freshness 多 provider 高度/滞后/reorg 观测。
