# EthGuard 当前路线图

状态：**方法覆盖与订阅闭环验证进行中（2026-11-07）**。

EthGuard 的 `Corroborated` 只表示 provider-backed cross-source evidence，不是 Ethereum consensus、canonical proof 或 cryptographic finality。架构收敛仍以 execution、quorum、reference 和 typed 边界为约束。

双切面接入基线已在提交 `aaeb905` 落地：

- 根 `Client` 提供带 `CallResult` 的原生方法；
- `EthClient` 提供官方 go-ethereum 形态的视图；
- 两个切面共享一套 execution、quorum、reference、typed projection、证据采集和生命周期；
- `ResultCapture`、`WithSourceEvidence`、预算边界和并发 `Close` 已完成复审；
- raw WSS 调用层已实现并接入 transport seam；
- WSS 订阅尚未实现；
- provider-backed W4 golden 仍未关闭，真实 `null` error-data 尚未观察。

本文件是当前唯一的路线与执行记录来源。旧的阶段台账、被取代的接口探索和历史执行细节保留在 Git 历史中，不在本文重复维护。临时探索稿继续放在 `docs/design/tmp/`，不作为当前执行契约。

执行分工见 [双轨执行计划](flash-execution-plan.md)：高推理 agent 负责语义探索和接口收敛，Flash agent 只执行边界已冻结的机械实现、测试和文档任务。

## 重启接续摘要

新 agent 接续时以以下事实为准，不依赖对话历史：

- 当前基线 HEAD：`3b242c5`；工作区应保持干净。
- 最近完成的代表提交：`8cfe5c3`（`eth_call` transport）、`8468354`（`eth_getLogs` transport）、`42e8198`（`eth_estimateGas` transport）。
- 当前 29 个 EthClient 导出方法 = 27 个 typed reads + `SendTransaction` + `Close`。
- 项目 catalog 的约 23 个 RPC 条目不是官方 ethclient 的完整方法数；官方未承诺项见 `method-transport-coverage.md` 的官方方法表。
- 下一执行游标：先补 `TransactionByHash`、`FeeHistory` 和剩余标量的跨 transport 事实；随后进入 M4 写入/扩展边界；Pending、订阅和公共接口统一审查必须由高推理轨处理。
- 当前 full-access 基线门禁：`go test ./... -count=1`、`go test ./... -race -count=1`、`go vet ./...` 均通过。

agent 按语义族或垂直切片分配，不按单个方法机械拆分。一个切片通常覆盖一个方法族及其 root/view、typed、transport 和反例测试；只有存在独立高风险语义时才单独拆 agent。

Flash 任务只允许在独立工作区执行已冻结的测试、fixture、覆盖矩阵和文档任务。不得让 Flash 修改公共 API、Pending 语义、订阅协议、aggregator/reference 或 transport seam；若发现这些需求，标记 `needs-review` 并停止。

## 当前边界

本轮先验证方法事实，再决定是否调整公共接口：

- 现有双切面和 `CallResult` 作为稳定基线，单个方法任务不得自行改变公共返回值；
- 方法任务可以暴露真实的参数、null、pending、EIP-1898、reorg、typed 解码、HTTP/WSS 差异和错误分层问题；
- 发现公共接口问题时，先登记到方法卡片和返回值审查任务，不能在局部任务中形成新的例外；
- `EthClient` 视图继续保持官方方法签名；根 Client 继续承担显式裁决事实；
- WSS 先完成请求/应答覆盖，再冻结订阅契约；订阅不得直接复用单次 `ResultCapture`；
- HTTP 普通请求不自动变成订阅；没有明确订阅契约时，方法应标记为不支持或待实现。

## 工程量评估与当前调度

剩余工作没有超出路线预期，但必须按语义复杂度分层调度：

| 类别 | 代表任务 | 估计工程量 | 调度判断 |
| --- | --- | --- | --- |
| 已有 plan/typed，只缺 facade 与回归 | `HeaderBy*`、四个 `*AtHash` | 一个垂直切片 | 已按同一 execution 接入；后续只补跨 transport 事实，不改公共结果模型 |
| 已有方法，只缺跨 transport 事实 | ChainID、Balance、Block/Header、Receipt、Call、FeeHistory | 一个测试切片，随后按方法族扩展 | 不先改 transport seam；用同 fixture 对照 HTTP/WSS/gethcompat |
| 有 plan 但本地状态语义未冻结 | PendingBalance/Storage/Code/Nonce | 一个设计复核加一个实现切片 | 先由 R0 冻结 source-local、quorum、Decision，再接 facade |
| 缺 plan/typed/绑定闭环 | TransactionSender、TransactionInBlock、PendingCall、Simulate、同步发送 | 至少两个切片，可能需要新方法契约 | 不做薄包装；先补 catalog、admission、typed 和错误模型 |
| 持续事件模块 | WSS newHeads、logs、transaction receipts | 独立 transport、聚合、生命周期和故障矩阵 | S0 之后至少拆 S1/S2；不与 unary 方法任务并行改共享接口 |

`at_hash_methods` 已验证 EIP-1898 plan 可以直接复用，未引入新的公共返回形态；代表性的
HTTP/WSS transport 切片已覆盖 ChainID、Header、Receipt、BlockReceipts、Call、Logs 和
EstimateGas。Pending 仍先由 R0 冻结 source-local、quorum 和 Decision 语义；订阅实现
暂不进入生产代码阶段。

## 执行任务表

每个任务完成后必须更新本表的状态、证据位置、实际命令和遗留问题。任务文件、生产文件和测试文件使用职责命名，不使用阶段编号命名。

| ID | 任务 | 交付物 | 前置 | 退出条件 | 状态 |
| --- | --- | --- | --- | --- | --- |
| M0 | 方法与传输事实清单 | `docs/design/method-transport-coverage.md`；逐项列出官方版本方法、catalog/ReadPlan、根切面、EthClient、HTTP、WSS request/response、订阅、不支持项和测试缺口 | 无 | 官方方法与项目方法无漏项；每项能指向实现和测试；未知方法不会静默绕过 catalog | 完成（2026-11-06；method_inventory） |
| M1 | 标量、费用和节点状态 | 方法卡片、根/视图调用、HTTP/WSS fixture、CallResult 状态与错误分层测试 | M0 | lower-median/selected/corroborated 语义明确；null、空值、同步状态和本地错误有反例 | AtHash 与标量 transport 切片完成（2026-11-07；`typed_scalar_transport_test.go`）；Pending 语义审计完成（`pending-semantics-review.md`），实现仍待 R0 |
| M2 | 区块与 reference | block、BlockNumber、EIP-1898、moving tag、pending-local、full/header-only、reference 附带证据的测试矩阵 | M0 | hash/number/tag 语义一致；业务失败不丢 reference；跨传输请求和结果一致 | Header + AtHash 切片完成；其余 reference/pending 与跨 transport 事实待补 |
| M3 | 交易、收据与日志 | Transaction、Receipt、BlockReceipts、Call、EstimateGas、FilterLogs 的方法卡片和测试 | M0 | null/NotFound、pending、receipt bloom、log binding、revert、范围边界和 typed 错误分层闭合 | 语义审计、receipt slice、Call/Logs/EstimateGas、TransactionByHash transport 和大数组成本审计完成（`m3-cost-audit.md`）；provider error-data 仍 `not_observed` |
| M4 | 写入与扩展方法 | SendTransaction、SendRawTransaction、CreateAccessList 及视图隐藏边界 | M0 | 广播接受与 mined 明确分开；未知写方法拒绝；raw 与 typed 写入材料一致 | 边界审计完成（2026-11-07；`m4-boundary-review.md`）；fixture/transport 回归待补 |
| T0 | HTTP/WSS request-response 覆盖 | 同一 fixture 下的 HTTP、native WSS、显式 gethcompat 对照结果 | M1–M4 按方法族推进 | 值、错误、Decision、reference、材料和 Close 语义一致；不新增 transport-specific 业务分支 | ChainID/Header/Receipt/BlockReceipts/Call/Logs/EstimateGas、标量状态、FeeHistory、TransactionByHash、CreateAccessList 和三方法 gethcompat differential 已完成；provider 事实与 typed facade 集成仍待 F0 |
| S0 | 订阅契约冻结 | `docs/design/subscription-contract.md`；新头、logs、reorg、quorum、source snapshot、补查、背压、重连、取消和异步错误契约 | M0、T0 初步结果 | 明确事件身份、法定人数、单源政策、事件交付和 `Subscription.Err()`；不把未验证推送标为 Corroborated | 契约候选冻结（2026-11-07；Strict 默认、显式 SingleSourceSelected、generation、队列溢出和 Err/Close 顺序已写入）；公开构造函数细节留给 F0 |
| S1 | WSS 订阅 transport seam | 订阅能力接口、source 生命周期、请求 ID 与通知隔离、关闭和取消测试 | S0 | 不影响现有 raw-call Transport；连接断裂、Unsubscribe、owner Close 和 goroutine 退出有界 | 最小内部 notification seam 完成（2026-11-07；`OpenNotificationStream`、bounded queue、generation/source identity、异步错误和 race 测试）；尚未接入业务聚合 |
| S2 | 订阅聚合与补查 | newHeads/logs 多源聚合、软超时 Pull、隔离、reorg、重复和缺口处理 | S0、S1 | 不静默丢事件；Pull 只使用同一 source 的已配置能力；事件 source snapshot 和 quorum 可重放 | 待开始 |
| R0 | 返回值与接口统一审查 | `return-value-design.md` 更新；单次调用与异步事件的最小结果类型对照 | M1–M4、S0 | 业务值、业务 error、网络裁决、证据材料四者职责清楚；不引入无必要的泛型包装；订阅不强塞 CallResult | unary 结果与现有 API 审查完成（2026-11-07；`r0-interface-review.md`）；订阅签名、单源策略、reorg/backpressure/reconnect 仍 needs-review |
| F0 | 总体验收与决策收敛 | 完整方法矩阵、订阅矩阵、未支持项、性能数据、接口变更建议和最终 ADR | M1–R0 | 所有声明能力有可运行证据；未观察 provider 事实单独标记；公共接口只在此任务统一调整 | 待开始 |

## Agent 分配

agent 按垂直切片工作；共享的 `eth.go`、`ethclient_view.go`、`CallResult` 和路线图由整合任务统一修改，避免并行冲突。

| Agent | 任务 | 当前交付 |
| --- | --- | --- |
| `method_inventory` | M0 | 已完成 `method-transport-coverage.md`；确认官方 Header、AtHash、Pending、订阅、Simulate 等族群仍未闭合 |
| `wss_contract` | S0 | 已完成 `subscription-contract.md`；确认订阅必须独立于 unary `CallResult` 和 raw-call Transport |
| `block_methods` | M2 首个切片 | 已接入 HeaderByHash/HeaderByNumber，补 header-only 请求、root/view 对照和 pending 本地拒绝；未改 CallResult/订阅 |
| `state_methods` | M1/M3 前置复核 | 已确认 `*AtHash` 可复用 EIP-1898，但 pending 语义和 public facade 仍待 R0；未改生产代码 |
| `at_hash_methods` | M1/M2 首个切片 | 已接入四个 `*AtHash` root/view 方法；验证 EIP-1898 hash object、冲突、缺块和方法集合；未改公共结果模型 |
| `transport_matrix` | T0 前置调查 + 最小实现 | 已完成 `docs/design/t0-transport-matrix.md`，并以 `typed_transport_matrix_test.go` 覆盖同一 Client 的 native HTTP + raw WSS `ChainID`/`HeaderByNumber`；未改 API 或订阅。HTTP/WSS 连接证据已在 `88b0ea5` 修正，full-access 环境复验通过 |
| `m3_semantics` | M3 高推理探索 | 已完成 `docs/design/m3-semantics-review.md`；冻结六个 M3 方法的现有事实、可执行回归范围和 R0 未决项；未改公共 API |
| `receipt_slice` | M3/T0 receipt slice | 已完成 receipt/log/bloom typed binding、root/view null 对照和 TransactionReceipt/BlockReceipts HTTP/WSS 测试；未改公共 API。根包测试已在 full-access 环境复验通过 |
| `call_logs_slice` | M3/T0 Call/Logs/EstimateGas | 已完成 logs 逆序范围本地拒绝、gas null/overflow 回归；错误的 zero-value call-object 断言未纳入提交 |
| `call_transport_slice` | M3/T0 CallContract | 已完成 `eth_call` replay fixture、call object 字段省略、pinned hash 和 HTTP/WSS 对照；提交 `8cfe5c3` |
| `logs_transport_slice` | M3/T0 FilterLogs | 已完成 `eth_getLogs` HTTP/WSS 请求形状、null/[]、range/pending 拒绝和 blockHash/range binding；提交 `8468354` |
| `gas_transport_slice` | M3/T0 EstimateGas | 已完成 `eth_estimateGas` HTTP/WSS 请求对象、pinned ref、Selected lower median、null/overflow/all-error 契约；提交 `42e8198` |
| `scalar_transport` | M1/T0 标量状态 | 已完成 `typed_scalar_transport_test.go`：NetworkID、AtHash 状态读取、费用和 BlobBaseFee 的 HTTP/WSS 请求与 Decision 对照；未改公共 API |
| `tx_fee_transport` | M3/T0 交易与费用 | 已完成 `TransactionByHash`、`FeeHistory` HTTP/WSS typed 投影、null 和请求 envelope 回归；未改公共 API |
| `m4_fixture_regression` | M4/T0 写入与扩展 | 已完成 CreateAccessList、SendTransaction 的 HTTP/WSS fixture、错误和签名字节一致性回归；未改公共 API |
| `pending_semantics` | M1/M3 Pending 语义审计 | 已完成 `pending-semantics-review.md`；冻结 source-local/Selected 边界，PendingTransactionCount/PendingCallContract 保持 unsupported |
| `subscription_architecture` | S1/S2 订阅准入审计 | 已完成 `subscription-architecture-review.md`（needs-review）；明确 transport seam、事件身份、quorum、补查、背压和生命周期准入 |
| `gethcompat_matrix` | T0 显式兼容层 | 已完成 `internal/transport/http/differential_test.go`：TransactionByHash、FeeHistory、CreateAccessList 的 native/geth envelope 与 raw-result parity；未改业务层 |
| `m3_cost_audit` | M3/F0 成本与预算审计 | 已完成 `m3-cost-audit.md`；核对大数组、材料预算、取消/Close 和 provider `not_observed` 边界 |
| `r0_interface_review` | R0 返回值与接口审查 | 已完成 `r0-interface-review.md`；确认 unary API 保持不变，订阅独立事件类型和候选签名仍需冻结 |
| `s1_wss_notification_seam` | S1 WSS notification seam | 已完成 `internal/transport/wss` 独立 notification stream、request/notification demux、backpressure、取消/Close 和 race 回归；未改 unary Transport 接口或公共 API |
| 后续 agent | M1–M4、T0、S1–S2、R0 | M0/S0 已满足准入；方法 agent 仍不得局部改写公共返回值 |

每个 agent 的交付必须包含：实现或文档 diff、方法特性卡片、正反例测试、实际命令、未决问题和对下一任务的准入条件。不得以“代码能编译”代替方法闭环。

## 方法验证要求

每个 unary 方法至少验证：

- 官方参数和返回签名；
- root 与 EthClient 两个切面的行为；
- HTTP、WSS 和显式 compatibility adapter 的请求形态；
- source basis、quorum、Decision.Status、reference 和最终业务 error；
- null、空数组、NotFound、pending、invalid request、malformed response；
- typed projection、规范化、请求-应答绑定和 revert；
- WithResult、WithSourceEvidence、预算耗尽和 Close/取消；
- 是否需要更新方法卡片、返回值设计或正式决策。

方法结果不能只记录“成功返回值”。必须记录“值 + 裁决 + 错误 + 材料 + 生命周期”五类事实。

## 订阅验证底线

订阅设计暂不接受以下隐含假设：

- 收到一个 source 的推送就等于业务可交付；
- source 没有权重就不需要稳定 source identity；
- 按高度聚合即可处理 reorg；
- 任意 WSS source 都能通过 HTTP Pull；
- 单次调用的 `ResultCapture` 可以无限复用到流；
- 有界 channel 满时静默丢事件；
- 断线重连、补查和 exactly-once 可以由 transport 自动推断。

订阅必须单独定义事件类型、事件身份、source snapshot、quorum、超时、Pull 能力、reorg/removed、重复、缺口、背压、预算、Unsubscribe、Close 和异步错误。官方视图保持 go-ethereum 的 channel + `ethereum.Subscription` 形态；根 Client 的裁决事件使用独立事件结果类型，不直接复用单次 `CallResult`。

## 自检与门禁

每个 agent 完成自己的任务后执行相关包测试、race 和 `git diff --check`。涉及方法执行、pool、reference、WSS 或 aggregator 时必须增加对应集成测试。

当前基线门禁：

```text
gofmt -l .
git diff --check
go mod tidy -diff
go vet ./...
go test ./... -count=1
go test ./... -race -count=1
go test . -run 'TestArchitecture|TestPackage|TestDefaultBuild' -count=1
go test ./internal/verification/integration -run '^TestVerification' -race -count=2 -shuffle=on
./scripts/release_check.sh
```

最终 `F0` 还需核对：

- `go list ./...` 不含已删除的 `ethadapter`；
- default build 不引入 `ethclient`；
- HTTP/WSS contract 和互操作测试；
- 订阅故障矩阵、race、取消和 goroutine 泄漏；
- provider-backed 证据的 provenance；
- 未支持方法明确列出，不以未知 Call fallback 伪装覆盖；
- 所有接口变化集中在 `R0/F0` 的统一决策中。

## 证据与停止规则

实现正确性、离线 transport 行为、provider 兼容性和 provider 未观察必须分开记录。真实 provider 没有观察到某种 error-data 形态时，记录为 `not_observed`，不能用合成样本关闭 W4。

任务失败时保留失败日志并停止该任务；不得删除测试、放宽断言、改写历史决策或把未实现能力写成已完成。只在退出条件满足后推进依赖任务。

## 关联文档

- [双切面执行计划](dual-facade-execution-plan.md)
- [方法计划清单](method-plan-inventory.md)
- [方法契约](method-contracts.md)
- [方法集成卡片](method-integration-cards.md)
- [WSS raw transport 契约](wss-wire-contract.md)
- [WSS 订阅探索稿](tmp/wss-sub-design.md)
- [双轨执行计划](flash-execution-plan.md)
- [后续传输与收敛分析](tmp/transport-coverage-and-simplification-plan.md)
- 决策记录：[docs/decisions.md](../decisions.md)
