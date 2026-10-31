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
- 语义：Close 标记 closing、拒绝新调用，pending 各自完成或取消后关闭
  socket；对端断连、读错误和超限仍立即 abort pending。
- 待最终处理问题：
  - 需要主维护者决策：自动重连、keepalive；
  - 外部证据阻塞：真实 provider 的 WSS 行为尚未采集。

## 6. WSS 进程内契约证据（route: WSS evidence gate, W3）

- 状态：已完成（仅 raw-call Adapter 范围）；决策 0056 未修订，WSS 四态
  golden 继续禁止。
- 证据：
  - `internal/transport/wss/wss_contract_test.go`：missing/null/`0x`/具体
    data、malformed frame、read limit、cancellation、Close、断连、pending
    清理、资源生命周期；
  - `docs/design/wss-transport-adapter.md`：语义、非目标、证据与未决问题；
  - `docs/design/wss-lifecycle-extension-contract.md`：reconnect、keepalive、
    backpressure 的设计边界与进入实现的证据门。
- 未完成出口：真实 provider/进程内证据都通过才能升级 0056；reconnect、
  reorg、backpressure 与真实 WSS provider capture 仍缺。
- 待最终处理问题：
  - 外部证据阻塞：WSS 四态 golden、真实 provider WSS capture；
  - 需要主维护者决策：reconnect/reorg/backpressure 的 provider 适用范围与
    配置归属；当前只完成设计契约，不实现扩展。

## 6a. WSS 真实 provider 证据（route: WSS evidence gate, W4）

- 状态：已执行真实 provider capture 与离线 replay；W4 出口未满足，决策 0056
  未修订，WSS 四态 golden 继续禁止。
- 证据：
  - `docs/design/wss-provider-evidence.md`：6 个公开、无需凭据的 WSS
    endpoint，chain 1，5 个只读 probe；30 条记录中 24 条协议错误；
  - 同日复测 8 个公开 WSS endpoint × 6 个只读 probe（新增
    `block-hash-position`）：48 条记录，2 个 endpoint 拨号失败，6 个可用
    endpoint 的 30 条协议错误为 missing=16、`0x`=1、value=13、**null=0**；
  - `cmd/wsscapture`：手动 capture/replay 工具；capture 目录在仓库外，
    只持久化 `scheme://host`，不进入默认测试或 CI；
  - 真实形态：missing=13、`0x`=1、value=10、null=0（首次 6 provider run）；
    复测仍无 null；24 个错误帧通过
    `rpcwire.DecodeResponse`，`rpc.ErrorDataPresence` 与
    `RPCError.DataPresence` 一致；12 个 `eth_call` 错误帧通过真实
    `methods.CallPolicy` 的两源回放（缺失 data 弃权、`0x`/具体 data 返回、
    比较键不跨形态合并、Support=2）；
  - 连接行为：一个公开 endpoint 在未知方法和非法参数错误后以 `1006`
    关闭连接，同连接 follow-up 失败；其他 endpoint 保持可调用；复测未重复
    该 close code。
- 依赖/阻塞：`null` 未在真实 provider 上观察；rate limit、read limit、
  response timeout、late response 和 provider 侧重连未触发或未观察。
- 风险：一次 6-provider 样本不是兼容性普查；capture 客户端是原始
  WebSocket probe，不是生产 WSS Adapter 的端到端网络测试（后者由进程内
  contract 覆盖）。不得把本次结论写成 provider 兼容性或四态 golden 授权。
- 下一步：寻找显式返回 `data:null` 的真实 provider，或由主维护者接受
  “null 仍不可知”的限制；在证据前不合成、不升级 0056。
- 生产化裁决输入：`docs/design/wss-productionization-decisions.md` 已把
  config/runtime、adapter 选择、reconnect、keepalive、backpressure、health
  归属和写路径排除七个问题收敛为推荐决策矩阵（2026-10-31），每项记录事实、
  选项、语义影响、证据、回滚和推荐；当前均未接线。
- 矩阵新发现的接线前置问题：
  - `Client.SendRawTransaction` 使用 `pool.Broadcast` 遍历固定成员，WSS
    source 进入同一 pool 会收到 `eth_sendRawTransaction`；“adapter 无写方法”
    不等于写路径已排除。
  - raw WSS 的 presence 保真会让显式 `data:null` 产生 `reverted|null` 键，
    而 geth WSS 只能产生 `reverted|missing`；接线 raw WSS 不是无行为变化的
    连接替换。
- 最终归类：外部证据阻塞（null 与限流/生命周期行为）；真实 provider 的
  missing/`0x`/value 已登记为 live observation；写路由与 presence 生产行为
  需要主维护者决策。

## 7. 逐方法对接卡片与 provider 证据（route: method integration）

- 状态：进行中（23 张卡片已建立；2026-10-31 的 live probe 补齐了若干字段，
  仍有明确 gap）。
- 证据：`docs/design/method-integration-cards.md` 按 23 个 RegisteredMethod
  记录 root、ethadapter、typed、错误、pending、EIP-1898、reference 与证据
  等级，并新增 capture/replay/live 矩阵。2026-10-31 的 4 源 × 10 请求
  live probe 记录：
  - EIP-1898 latest object 的 balance/nonce/storage/code 与
    createAccessList 均为 4/4 成功；createAccessList 结果一致；
  - `eth_getBlockByNumber` hash 位置为 3 个明确拒收 + 1 个限流，无 provider
    接受；
  - `eth_blobBaseFee` 2/4 成功、1 free-plan、1 策略拒绝；
  - `eth_feeHistory` 3/4 在相同的 `oldestBlock` 返回相同 reward，1 个源
    `oldestBlock`/区间不同；
  - `eth_getBlockByNumber latest` 4/4 返回同一 26-key 顶层集合，3/4 同一
    block，1 个落后一个区块；
  - `eth_syncing` 4/4 返回 `false`，仍无 progress object。
  - 同日定向历史块 probe（4 源 × 11 请求，共 44 条，block 18,000,000）：
    balance 的 `{blockNumber}`/`{blockHash}` object 4/4；nonce/storage/code/
    createAccessList 的 `{blockNumber}` object 4/4；createAccessList 全部返回
    `{"accessList":[],"gasUsed":"0x539f"}`；createAccessList 的
    `{blockHash}` object 在 `requireCanonical` false/true 下也 4/4；
  - 同日定向 probe：feeHistory 固定区间 `[1,33,67,99]` 4/4 返回相同
    `oldestBlock`、base fee 与 reward；历史 block header/full 4/4，header
    22 个顶层字段、94 笔交易、16 withdrawals、0 uncles，full 交易对象有 1 个
    源省略 `blockTimestamp`；hash-position 4/4 拒收；syncing 4/4 `false`。
- 风险：单次 live probe 不是兼容性普查；一个端点在本轮返回限流；历史
  EIP-1898 object 在 block 18,000,000 上 4/4，但其他高度仍受 provider
  archive 策略影响；feeHistory 舍入只有单区间一致证据；uncle 内容仍无样本；
  卡片中的 `soak`/`sample` 仍是观测，不是长期承诺。
- 待最终处理问题：
  - 外部证据阻塞：`eth_syncing` progress object、EIP-1898 其他历史高度/
    archive 策略、`eth_feeHistory` 多区间 percentile 舍入、uncle 内容与
    full 交易对象字段差异、WSS 真实 `null` 与限流/生命周期行为；
  - 需要主维护者决策：是否以及何时为这些缺口安排更有针对性的 capture。

## 8. 自适应拥塞控制设计（route: T11）

- 状态：设计不变量已完成；算法未实现；2026-10-31 观察到限流/配额信号，
  但没有容量曲线。
- 证据：`docs/design/adaptive-congestion-control.md` 固定成员/分母/权重/
  模式/门槛不变，定义信号、状态、预算共享、恢复和验收矩阵；不选择参数。
  2026-10-31 的只读方法 probe 中，一个公开 endpoint 返回
  `-32029 Too Many Requests`，一个 endpoint 对 `eth_blobBaseFee` 返回
  free-plan 错误，另一个返回非 EVM 策略拒绝。
- 风险：这些是 provider 配额/策略信号，不一定是容量反馈；真实持续限流、
  超时、并发上限和队列增长仍缺；如果实现要求动态成员或权重，必须停止在
  调度层。
- 待最终处理问题：
  - 需要主维护者决策：算法族、margin/冷却/队列参数、跨 endpoint 配额与
    写路径预算归属；
  - 外部证据阻塞：受控容量/限流样本、并发上限和超时分布。

## 9. Reference freshness（route: reference policy）

- 状态：设计不变量已完成；2026-10-31 完成短窗口与约 6 分 22 秒扩展窗口两次
  latest/safe/finalized 观测；新策略未实现。
- 证据：`docs/design/reference-freshness-policy.md` 区分调度 tip gate 与
  finalized 候选 lag，固定 tag 语义、拒绝而非降级、显式历史读取豁免与
  agreement/freshness 分离；`reference-freshness-observations.md` 记录：
  - 短窗口 4 源 × 3 pass、约 15 秒间隔：latest 最大差 1 block，safe 31，
    finalized 159，同高度未见 hash 分歧；
  - 扩展窗口 4 源 × 8 pass、约 45 秒间隔、约 6 分 22 秒：latest 最大 spread
    1，safe 224，finalized 96；一个源的 safe/finalized 在全部 8 pass 固定
    不动而 latest 仅落后 1 block；未观察到高度回退、同高度 hash 冲突或
    reorg；一次 HTTP 503 坏响应与 freshness 分开归因；
  - 代码复核：tip gate 只看 source tip、不读请求 tag/height；样本不按
    `tipObservedAt` 年龄过期；gated 成员保留分母，write path 不做 read
    freshness gating，参见 §3.1 与现有回归测试。
- 风险：6 分钟窗口仍不能证明 reorg 行为或长期 lag 分布；safe/finalized tag
  的更新节奏因 provider 而异，且可能在一个窗口内完全不动；同一 provider 的
  `eth_blockNumber` 与 latest block 可出现 1-block 差异；当前 `maxLag`/tip
  gate 仍是实现事实而非新默认承诺；显式历史读取仍会被 source-level tip gate
  影响，是否符合“豁免”语义需要裁决。
- 待最终处理问题：
  - 需要主维护者决策：lag 单位、方法适用范围、样本过期语义、显式历史读取
    与 tip gate 的关系、是否自适应；
  - 外部证据阻塞：长窗口 reorg/回退、tip 样本过期、finalized candidate 与
    reference confirmation 的交互、多客户端观测。

## 9a. HTTP connection seam 审计（route: connection seam）

- 状态：已完成；无需新增实现。
- 证据：
  - `docs/design/transport-contract.md` 冻结 L1 外部行为；
  - `internal/transport/http` 的 native adapter 与 `internal/transport/geth`
    的 compatibility adapter 都通过同一 `contract.RunL1` suite
    （`TestNativeTransportL1Contract`、`TestGethTransportL1Contract`）；
  - 默认 root facade 安装 `nativehttp.NewDialer()`；`gethcompat` 是显式
    compatibility seam；`pool` 生产代码不依赖 go-ethereum；
  - 默认构建的依赖列表不含 `ethclient` 或 `go-ethereum/rpc`；
    `TestArchitecture|TestPackage|TestDefaultBuild` 通过；
  - differential 记录见 `http-transport-differential.md` §8。
- 风险：contract 的长期有效性取决于 provider 证据和 Geth 版本；升级
  go-ethereum 前需要重跑 L1 suite 和 baseline。配置层没有 YAML adapter
  字段，只有 root `WithHTTPAdapter`/`gethcompat` 入口；当前没有新增该字段的
  需求，不把它写成缺口。
- 待最终处理问题：无（当前契约内已闭合）。
- 最终归类：已解决。

## 10. 已登记的遗留问题

| 问题 | 状态 | 证据/影响 | 处理 |
| --- | --- | --- | --- |
| WSS missing/null presence 仍不可知 | 开放（外部证据阻塞） | 决策 0056；geth 路径只表达 unknown；WSS raw-call Adapter 已能保留四态；2026-10-31 真实 provider 观察到 missing/`0x`/value，null 未观察 | W4 在 null 真实证据或主维护者接受限制后再修订 |
| geth WSS 自动重连语义 | 开放（需主维护者决策） | 替换 transport 时不得无声丢失；WSS Adapter 明确不重连 | 生产切换前另立重连/失败分类契约 |
| WSS Close 的 in-flight 语义 | 已解决 | `Close` 标记 closing、拒绝新调用；pending 各自完成或取消后关闭 socket，遵守 Transport Port | 由 `TestCloseLeavesInFlightCallsToTheirContexts` 与 WSS adapter 文档锁定 |
| `eth_syncing` progress object provider 形状 | 开放（外部证据阻塞） | 2026-10-21 与 2026-10-31 的样本均只观察到 `false`；progress object 未观察 | 保持 capture gap，不用合成样本关闭 |
| liveness/typed 的对象形状证据 | 部分解决（外部证据仍有缺口） | 2026-10-31 latest EIP-1898 object 4/4；历史 block 18,000,000 的 blockNumber object 4/4；其他高度、uncle 内容和 full 交易字段差异仍按 sample | 继续补其他历史高度、uncle 样本和跨客户端差异 |

## 11. 收尾分类（2026-10-31）

- **已解决**：
  - resolver 深模块裁决（决策 0063、契约 §8）；
  - strict-head malformed 原始字节采集；
  - finalized reference `Read`/`Confirm` 采集；
  - WSS 调用层可行性（W1）与最小 raw-call Adapter（W2/W3，进程内）；
  - 23 个方法的业务语义对接卡片；
  - T11 与 reference freshness 的设计和不变量；
  - native HTTP adapter 与冻结的 L1 transport contract（2026-10-29 基线；
    本轮复核默认装配与 `pool` 无 Geth 依赖）。
- **可接受限制**：
  - WSS Adapter 不实现订阅、批量、自动重连、主动 keepalive；生产切换前另立契约；
  - WSS 取消发生在已阻塞的写调用时，只有调用 deadline 能中断写；
  - finalized reference 的 participant/selection 诊断仍只通过
    `ReferenceEvidence` 暴露；
  - WSS Adapter 未接入默认 runtime/config，作为独立配置决策保留；
  - WSS 进程内证据不升级决策 0056，不生成 WSS 四态 golden；
  - reference freshness 观测窗口约 6 分 22 秒，未观察到 reorg，但不能证明
    reorg 分布或长期 lag；
  - 2026-10-31 的方法 live probe 是单次 4 源样本：latest 与历史 block
    18,000,000 的 EIP-1898 object 形状一致、createAccessList 的
    blockHash/canonical 形状一致、block header/full 内容一致、hash 位置无
    接受者，但这些仍不是长期兼容性普查；full 交易对象存在
    `blockTimestamp` 字段差异。
- **需要主维护者决策**：
  - WSS reconnect/reorg/backpressure 与 keepalive 的契约归属；
  - WSS 生产化的写路径路由机制（adapter-only、独立只读组合或新的成员角色）；
  - WSS raw presence 在 production 是保留四态还是降级 unknown；
  - 是否以及何时把 WSS 接入配置/runtime，采用显式 per-source 字段还是
    仅 composition root 注入；
  - T11 算法族、margin/冷却/队列参数与预算范围；
  - reference freshness 的 lag 单位、方法适用范围、样本过期语义、显式历史
    读取与 source-level tip gate 的关系，以及是否自适应；
  - 真实 provider capture 的排期。
- **外部证据阻塞**：
  - `eth_blobBaseFee` 多源支持面普查（已有 2026-10-21 sample 与
    2026-10-31 probe）；`eth_syncing` progress object；
  - EIP-1898 其他历史高度/archive 策略（block 18,000,000 的
    blockNumber object 4/4，balance blockHash object 4/4）；
  - `eth_getBlockByNumber` hash 位置（2026-10-31 4/4 拒收）；
  - `eth_createAccessList` 的 hash/canonical 跨高度普查（block 18,000,000
    单块已 4/4）；`eth_feeHistory` 多区间 percentile 舍入（固定区间
    `[1,33,67,99]` 4/4 一致）；
  - 完整区块内容承诺：block 18,000,000 的 header/full 4/4、16 withdrawals、
    0 uncles；uncle 样本与 full 交易对象字段差异仍未验证；
  - WSS 真实 provider 的 `error.data:null`、限流、read limit、响应超时、
    迟到响应与 provider 侧重连（missing/`0x`/value 已观察）；
  - T11 容量/限流样本；freshness 更长窗口的 reorg/滞后观测（当前约
    6 分 22 秒，未观察到 reorg）。
