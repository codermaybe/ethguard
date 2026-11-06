# 双轨执行计划：高推理探索与 Flash 快速交付

本计划把路线图拆成两条轨道：高推理 agent 负责探索语义、发现跨模块问题和冻结边界；Flash agent（由 Zcode 使用 `glm5.3flash` 执行）负责边界已经冻结后的机械实现、测试和文档收敛。职责名称是稳定入口，不使用阶段编号。所有职责都必须先读完 `project-roadmap.md`、`method-transport-coverage.md` 和 `t0-transport-matrix.md`，并在交付中链接方法卡片、测试和实际命令。

## 模型分工

### 高推理轨：由主 agent 统筹

高推理轨先于 Flash 轨执行，负责产出可执行的事实卡片和停止条件：

| 职责 | 当前范围 | 必须回答的问题 |
| --- | --- | --- |
| 方法语义探索 | M3 收据、日志、Call、EstimateGas | null/NotFound、revert data、log/receipt binding、pending 和大数组是否改变结果模型 |
| 写入与扩展探索 | M4、CreateAccessList、Proof、Simulate | 广播接受、执行完成、同步发送和扩展 RPC 的边界是否能复用现有 execution |
| Pending 语义决策 | M1/M3 pending 族 | source-local 观察、多源比较、quorum、Decision 和错误如何定义 |
| 订阅架构探索 | S1/S2 | source snapshot、事件身份、reorg、补查、背压、重连、异步错误和 Close 如何闭合 |
| transport 异常审计 | T0 代表矩阵 | native HTTP、WSS、gethcompat 的差异是否只在 transport 层，还是会改变 typed 结果 |
| 返回值与接口审查 | R0 | 是否需要调整 `CallResult`、root/view 方法或新增独立事件结果类型 |

高推理轨的交付不是“直接写完代码”，而是：方法事实卡片、正反例矩阵、允许修改边界、升级触发器和 Flash 可执行任务单。发现公共接口或跨层语义问题时，高推理轨停止局部实现并更新 R0/F0 输入。

### Flash 轨：交给 Zcode/`glm5.3flash`

Flash 只接收高推理轨已经冻结输入的任务：

| Flash 任务 | 输入已冻结的条件 | 允许交付 |
| --- | --- | --- |
| 覆盖矩阵补全 | 方法签名、plan、typed 投影和错误契约已有事实 | 补方法卡片、覆盖表、缺失测试和 fixture |
| 已有 facade 的机械接入 | root/view 签名由现有方法族明确推导，无新语义 | 添加薄的执行包装、root/view 对照测试，不改公共模型 |
| transport fixture 扩展 | 请求参数和结果契约已由 T0 固定 | 扩展 native HTTP/WSS/gethcompat 测试、请求断言和 race 测试 |
| 已知边界回归 | 反例、预期错误和 Decision 状态已有明确断言 | 添加 null、NotFound、malformed、cancel、Close、预算测试 |
| 文档和验收收敛 | 实现事实已由高推理轨确认 | 更新 coverage、roadmap、方法卡片和命令记录 |

Flash 不负责 Pending 语义、订阅协议、`CallResult`/`ResultCapture`、root/view 公共签名、aggregator/reference/transport seam 重构，也不负责决定 unsupported 方法是否进入承诺范围。

## 双轨交接流程

1. 高推理 agent 先提交方法事实卡片和停止条件，不与 Flash 并行修改同一生产文件。
2. 主 agent 将已冻结内容转成一个 Flash 任务单，任务单必须列出文件白名单、禁止修改的文件、验收命令和预期 diff 形状。
3. Flash 执行后只提交实现、测试和文档，不自行扩大范围；失败保留日志并停止。
4. 高推理 agent 审计 Flash diff，重点检查业务值、业务 error、Decision、reference、材料和生命周期是否同时被验证。
5. 审计通过后才合并/提交；审计发现新语义时退回高推理轨，不能让 Flash 现场设计接口。

每个 Flash 任务都必须有唯一的“升级触发器”。触发器出现时，任务状态为 `needs-review`，不得标记完成。

## 推荐执行顺序

| 顺序 | 轨道 | 任务 | 交接结果 |
| --- | --- | --- | --- |
| 1 | 高推理 | 收据/日志/Call 特性探索 | M3 方法事实卡片、反例矩阵、Flash 文件白名单 |
| 2 | Flash | 扩展 T0 六方法矩阵 | `ChainID`、`BalanceAt`、`HeaderByNumber`、`TransactionReceipt`、`CallContract`、`FeeHistory` 的 transport 测试和文档记录 |
| 3 | 高推理 | 写入与扩展边界探索 | M4 的广播/执行/扩展承诺决策，明确哪些方法保持 unsupported |
| 4 | Flash | 已冻结的 M3/M4 低风险补全 | 机械 facade、typed fixture、null/malformed/Close/race 回归 |
| 5 | 高推理 | Pending 语义和订阅 S1/S2 探索 | R0 输入：source-local、quorum、事件身份、补查、背压和生命周期 |
| 6 | Flash | 仅执行已冻结的订阅 seam 测试和文档 | 不改变 unary 结果模型，不新增隐含 fallback |
| 7 | 高推理 | R0 统一接口审查 | 集中决定公共 API 是否调整，并给出迁移与全量回归清单 |
| 8 | Flash | 文档、覆盖表和机械迁移 | 按 R0 决定更新示例、方法表、测试名称和验收记录 |
| 9 | 高推理 | F0 最终审计 | 全量测试、race、transport、订阅、性能、provider `not_observed` 和 ADR 收敛 |

Flash 任务不得跨越相邻的高推理交接点。例如，M3 探索尚未冻结前，Flash 可以补现有
`TransactionReceipt` 的缺失反例，但不能自行决定 receipt null 是否映射为新的错误类型；
Pending 和订阅则必须等高推理轨给出完整语义后才能进入实现。

## 共同执行规则

- 生产公共接口、`CallResult`、`ResultCapture`、transport seam 和订阅事件类型只能由“返回值与接口审查”或“总体验收整合”职责修改；其他职责发现问题时只登记，不局部修复。
- 允许修改范围必须限于职责列出的文档、方法实现、typed 投影和测试；不得删除测试、放宽断言、把未知 `Call` 当成覆盖、把 `not_observed` 写成已观察。
- 每项验收至少运行相关包测试、`git diff --check`；涉及 pool、WSS、取消或 aggregator 时增加 race 测试。失败保留日志并停止该职责。
- 结果必须分别记录业务值、业务 error、Decision、reference、原始材料和生命周期；“能编译”不是完成条件。

## 方法事实审计

**输入**：官方 go-ethereum v1.17.5 方法列表、项目 catalog/ReadPlan、现有 root/view 方法、覆盖清单。

**允许修改**：`docs/design/method-transport-coverage.md`、方法卡片和对应测试；不得修改公共返回值或 transport 接口。

**验收命令**：

```text
go test ./rpc/methods -run 'EIP1898|Pending' -count=1
go test ./... -run 'TestArchitecture|TestPackage|TestDefaultBuild' -count=1
git diff --check
```

**停止条件**：发现 catalog、官方签名或实现事实互相矛盾；需要新增 facade/结果类型；缺少可复现 fixture。停止并提交未决项。

**升级触发器**：官方方法边界不清、同一方法在 root/view 的错误语义不同、或需要解释 provider 未观察行为时，升级到高推理模型。

## 标量与节点状态执行

**输入**：ChainID、Balance、Nonce、Storage、Code、费用和同步状态的现有 ReadPlan、typed projector 与三节点 fixture。

**允许修改**：对应 `eth.go`/typed 实现、方法测试、覆盖清单；pending 方法只能增加调查测试，不得冻结新公共语义。

**验收命令**：

```text
go test ./... -run 'ChainID|Balance|Nonce|Storage|Code|Fee|Sync|Network' -count=1
go test ./... -race -run 'ChainID|Balance|Nonce|Storage|Code|Fee|Sync|Network' -count=1
git diff --check
```

**停止条件**：source-local pending、quorum 或 Decision 无法从现有规则推出；出现 null/空值无法区分；需要改 `CallResult`。

**升级触发器**：多源 pending 冲突、历史 reference 与 source-local 事实混合、或 error-data 四态影响公共错误映射。

## 区块与 reference 执行

**输入**：Block/Header、BlockNumber、EIP-1898、moving tag、pending-local 规则及现有 `typed_block_headers_test.go`、AtHash 测试。

**允许修改**：区块方法、anchor/reference 辅助、typed 解码和方法测试；不得改全局结果模型。

**验收命令**：

```text
go test ./... -run 'Header|Block|AtHash|Reference|Anchor' -count=1
go test ./... -race -run 'Header|Block|AtHash|Reference|Anchor' -count=1
git diff --check
```

**停止条件**：hash/number/tag 不能保持同一 reference 事实；pending 被错误宣称支持；业务错误丢失 reference。

**升级触发器**：reorg、canonicality、full/header-only 请求改变执行计划，或需要跨方法共享新的 reference 类型。

## 交易、收据、日志与调用执行

**输入**：Transaction、Receipt、BlockReceipts、FilterLogs、Call、EstimateGas 的现有 typed projector、revert 和 receipt fixture。

**允许修改**：对应方法实现、对象解码、方法卡片和测试；不得新增订阅契约或局部修改公共返回值。

**验收命令**：

```text
go test ./... -run 'Transaction|Receipt|Logs|Call|EstimateGas' -count=1
go test ./... -race -run 'Transaction|Receipt|Logs|Call|EstimateGas' -count=1
git diff --check
```

**停止条件**：NotFound/null、pending、revert/data presence、bloom 或 log binding 无法形成正反例；需要新的错误层级。

**升级触发器**：调用 overrides、revert 数据四态、receipt/log 绑定或大数组预算需要改变执行与证据模型。

## 写入与扩展边界审查

**输入**：SendRawTransaction、SendTransaction、CreateAccessList、`eth_getProof`、SimulateV1 及官方扩展资料。

**允许修改**：catalog admission、写入方法测试和边界文档；不得静默暴露未承诺方法。

**验收命令**：

```text
go test ./... -run 'Send|AccessList|Proof|Simulate' -count=1
go test ./... -race -run 'Send|AccessList|Proof|Simulate' -count=1
git diff --check
```

**停止条件**：广播接受与 mined 混淆；未知写方法可通过动态 Call；扩展缺少明确承诺或拒绝理由。

**升级触发器**：需要新的 admission、写入状态机、同步发送语义或生态兼容承诺。

## HTTP/WSS 传输矩阵执行

**输入**：`t0-transport-matrix.md` 的六方法矩阵、`rpctest.Node`、HTTP differential fixture、WSS `scriptedPeer`、gethcompat adapter。

**允许修改**：`typed_transport_matrix_test.go`、transport fixture 和矩阵文档；不得改业务执行核心、请求策略或订阅能力。

**验收命令**：

```text
go test ./... -run 'TransportMatrix|Differential|WSS' -count=1
go test ./... -race -run 'TransportMatrix|Differential|WSS' -count=1
git diff --check
```

**停止条件**：无法证明同一 JSON-RPC 方法/参数在 HTTP、native WSS 和 gethcompat 的边界；IPv6/环境限制导致测试不可运行时记录并停止，不改生产代码绕过。

**升级触发器**：需要 transport-specific 业务分支、重连/订阅、请求 ID 规则改变，或 native/geth 响应差异影响 typed 结果。

## Pending 语义决策

**输入**：PendingBalance/Storage/Code/Nonce、PendingTransactionCount、PendingCallContract 的 plan、provider 行为和现有 source-local 复核。

**允许修改**：仅 `docs/design/return-value-design.md`、方法卡片、调查 fixture；公共 facade 和 `CallResult` 变更须留给接口审查。

**验收命令**：

```text
go test ./rpc/methods -run 'Pending' -count=1
go test ./rpc/methods -race -run 'Pending' -count=1
git diff --check
```

**停止条件**：无法决定单源观察、多源严格比较、quorum 与 Decision；真实 provider 事实缺失时标记 `not_observed`。

**升级触发器**：需要新结果类型、source snapshot、异步错误或跨方法 pending 一致性规则。

## 订阅契约与实现职责

**输入**：`subscription-contract.md` 草案、WSS raw-call 生命周期、newHeads/logs/receipt 官方形态。

**允许修改**：订阅契约职责只改设计文档；订阅 transport/聚合职责可新增独立接口、事件类型和测试，但不得复用 unary `ResultCapture` 或改变 raw-call Transport。

**验收命令**：

```text
go test ./... -run 'Subscription|WSS|Backpressure|Reorg' -count=1
go test ./... -race -run 'Subscription|WSS|Backpressure|Reorg' -count=1
git diff --check
```

**停止条件**：事件身份、source snapshot、quorum、Pull 补查、背压、取消、Unsubscribe 或异步错误未定义；不得把单源推送标为 `Corroborated`。

**升级触发器**：重连、缺口补查、reorg/removed、exactly-once、goroutine 泄漏或 owner Close 需要跨层协议。

## 返回值与接口审查

**输入**：M1–M4、T0、Pending 和订阅结果；所有未决方法卡片与失败日志。

**允许修改**：`return-value-design.md`、root/view 公共接口、`CallResult`、独立事件结果类型及其全量回归测试；这是唯一可统一改变公共契约的职责。

**验收命令**：

```text
go test ./... -count=1
go test ./... -race -count=1
go test . -run 'TestArchitecture|TestPackage|TestDefaultBuild' -count=1
git diff --check
```

**停止条件**：证据、业务值、错误和生命周期仍混责；任一接口变化没有迁移说明和反例测试。

**升级触发器**：需要泛型包装、root/view 不可兼容、订阅与 unary 结果强行共用，或公共 API 变更超过单一方法族。

## 总体验收整合

**输入**：完整覆盖矩阵、订阅矩阵、所有职责的证据位置、未支持项、provider `not_observed` 记录。

**允许修改**：路线图状态、最终 ADR、整合测试和必要的集中式接口修订；不得删除历史失败证据。

**验收命令**：

```text
gofmt -l .
git diff --check
go mod tidy -diff
go vet ./...
go test ./... -count=1
go test ./... -race -count=1
go test ./internal/verification/integration -run '^TestVerification' -race -count=2 -shuffle=on
go test . -run 'TestArchitecture|TestPackage|TestDefaultBuild' -count=1
./scripts/release_check.sh
```

**停止条件**：任一声明能力没有可运行证据；default build 引入 `ethclient`；存在静默 fallback、未列出的 unsupported 方法、订阅泄漏或未解释的 provider 事实。

**升级触发器**：需要跨职责重排执行模型、性能/并发数据与语义冲突、或最终 ADR 无法在现有证据下作出单一决策。
