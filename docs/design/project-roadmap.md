# EthGuard 当前路线图

状态：**统一 Source 已实施（2026-11-01）：显式 WSS 与 HTTP 共用固定读写 pool，通用限流 health 已修复，生命周期/未知写结果/四态 presence 回归已通过；0056.2 批准生产保真，0064 记录统一路由。freshness 与调度切片已完成（2026-11-02，提交 6292d73）：T0 受控容量实验、F1/F2 请求范围与样本有效性接线、T1 控制器裁决（保留现有慢增快减、固定两槽、共享预算与 cooldown 控制器）均已交付并通过完整门禁。W4/provider-backed golden 门未通过：真实 null 未观察；本轮 3×6 只读补采与离线 replay 未关闭该门。T11 新容量证据与 reference freshness 新策略仍未实现，出现新证据时单独重审。**

本文件是 EthGuard 唯一的路线与阶段状态来源，记录当前事实、方向、顺序、依赖、
遗留问题和阶段出口。设计文档只记录某个契约、决策或证据的详细内容，不维护第二
份路线或阶段状态；历史执行过程和已否决方案以 Git 历史为准。

恢复上下文请先读 [架构恢复记忆](architecture-recovery.md)，再读
[代码库拓扑与模块所有权](codebase-topology.md)。

## 当前基线

EthGuard 的运行时语义已经冻结：

- `Corroborated` 是 provider-backed cross-source evidence，不是 Ethereum
  consensus、canonical proof 或 cryptographic finality；
- Decision、quorum、reference policy、方法契约和方法数量不因目录迁移改变；
- T09 的字段生命周期、决策矩阵、typed 边界和整体审计已完成；
- T10 的离线/live 证据分层已完成，live observation 不写成普遍兼容性证明；
- raw WSS 生产路径保留四态；geth WSS 的 missing/null unknown 限制仍适用。0056.2
  不豁免真实 provider 四态 golden 证据门；null 仍未观察。
- Read 默认 registry 由显式 `rpc.ReadPlan` 驱动，且这是唯一执行模型（2026-11-04
  完成，提交 e2849cf）：23 个方法级 constructor、decorator 家族、`rpc.Registry`
  runtime 路径与 aggregator 双分支已删除；未注册方法走 aggregator 的显式 forward
  分支；
- resolver 内部仍保留 `rpc.Policy` 深模块（RS5 裁决，0063）。`blockPolicy` 收敛为
  block resolver 的值包装并持有链尾（CanonicalOf 可达）；可选 capability 接口收敛；
  `block`/`strict`/`median`/`local`/`call-revert` 的 resolver 装配由
  `CompilePlan`/`assembleResolver` 单点拥有；
- reference policy 统一方案、freshness 余量和 adaptive congestion 仍按各自决策保留，
  不由方法对接阶段隐式改写。

### 结构基线

- `internal/runtime` 是唯一生产 Composition Root；
- `internal/typed` 是唯一生产 typed projection；
- `pool.Transport` 和 `internal/reference.HeadReader` 是消费者拥有的最小 seam；
- verification 当前保持单一 `internal/verification/integration` Module；
- `rpc/methods` 保持单一生产 Module，`rpc/internal/objects`、`wire`、`policy` 拥有
  各自的深语义；
- root facade 与 `ethadapter` 不存在第二套 typed decoder、policy registry 或执行链；
- T09 测试入口使用 `^TestVerification` 专项门。

### 规范和依赖基线

- Execution APIs 是方法名、参数形态、响应字段和官方边界的规范来源；
- 当前 go-ethereum `v1.17.5` 是兼容性、Geth 类型和 wire 行为的对照基线，不是
  Ethereum 上游真值的规范来源；
- `go.mod` 固定 go-ethereum 版本以保证可复现；连接 seam 已由 native HTTP 默认实现
  承担，pool 生产代码不再接触具体 Geth client，Geth compatibility adapter 只经
  `gethcompat` 显式进入。typed values/codecs 仍使用 Geth 类型，这是保留的兼容面；
- 自有 WSS wire 栈已上线（2026-11-03，见 wss-wire-contract.md 与执行台账
  §17）：HTTP Upgrade、帧协议与连接生命周期由 `internal/transport/wss` 以
  Go 标准库实现；`gorilla/websocket` 直接依赖已删除，仅作为 go-ethereum
  兼容通道的 indirect 残留，默认构建与 WSS 生产构建均不链接；
- 依赖迁移只允许渐进地收窄内部 connection/transport seam，不启动移除 go-ethereum
  类型的大爆炸重写，不建立平行 Block/Receipt/Transaction 类型系统；
- T00 的版本、分叉和 ethclient 兼容矩阵见 [spec-baseline.md](spec-baseline.md)，
  该文件是历史基线，逐方法对接必须引用并更新当前状态，不把历史快照当现状。

架构审查记录：

```text
4cffdeb verification topology split timing
04ee039 rpc/methods boundary review
31f60b3 adapter boundary review
a4c2ac9 architecture convergence close-out
c58fe40 T09 audit and method-integration roadmap
```

当前基线命令：

```text
go test ./... -count=1
go vet ./...
go test ./... -race -count=1
go test ./internal/verification/integration -run '^TestVerification' -race -count=2 -shuffle=on
```

## 接续计划：切片 9–15

状态：**切片 1–8 已完成；切片 9 正在收口，尚未通过全量门禁。** 当前工作区的
生产删除、计划回放适配和 reference trace 适配必须先恢复到可验证状态，再继续
后续切片。`go test ./... -count=1` 当前仍有 replay、reference 和 catalog 矩阵失败，
这些失败不能通过删除断言或放宽预期解决。

下表是原执行计划的接续版本。每个切片单线推进；出口未满足时停止，不进入下一行。

| 顺序 | 业务任务 | 主要改动 | 退出条件 | 提交边界 |
| --- | --- | --- | --- | --- |
| 9 | 删除双执行模型并完成 plan 执行收口 | 先修复计划回放、reference evidence 和 catalog 矩阵；再移除剩余 legacy constructor、decorator、双路 aggregator 分支、runtime `Registry` 路径和无效工具引用 | replay、runtime、methods 相关测试全绿；生产路径只由 plan 执行；方法数量、Decision、quorum、reference 和 typed 语义不变 | 测试绿化与生产删除分开提交 |
| 10 | 收敛 context ownership | 盘点 context key 的读写；保留 invocation/capture 必要事实，将成员和 corroboration 归入 Invocation/Basis；移除 adapter 对内部 context 的直接依赖 | context 只保留必要 key；取消、forward、corroboration、adapter 测试通过；没有隐式模式切换 | 只改 context ownership 与显式业务 option |
| 11 | 压平 reference facade | 让 facade 与 invocation 共用 ReferenceStage；删除重复 service 路由和 runtime helper | 同一 fleet、tag 和请求下 evidence、参与者、fault 与失败结果一致 | 不改变 reference evidence 语义 |
| 12 | 统一 reference 单轮执行 | 先补 strict-head 与业务轮次的阶段映射测试，再评估 aggregator 单轮入口；删除重复 round seam | fault、attempt、source identity 和取消规则一致；不引入重试、quorum 或成员变化 | 阶段映射测试与生产改动分开提交 |
| 13 | 归位 call typed decoding | 将 adapter 内 call/revert 解码归入 `internal/typed`，adapter 只保留兼容参数和错误映射 | adapter 不再拥有第二套 call decoder；revert、data presence 和 typed projection 回归通过 | 只移动 typed 解释逻辑 |
| 14 | 文档状态收敛 | 同步 roadmap、拓扑、verification 状态、历史提案的 Superseded 关系和 digest 行尾契约；记录切片 9–13 的实际证据 | 本文件仍是唯一路线来源；事实、目标和外部未观察状态分开；不改历史决策正文 | 文档按对应业务切片独立提交 |
| 15 | 最终代码审计 | 按 Standards/Spec 双轴审计所有切片；检查业务命名、依赖方向、删除范围、测试真实性和未授权语义变化 | gofmt、diff check、build、vet、普通测试、race、架构 interlock、Verification 专项门均有实际结果 | 审计记录独立提交；不混入新功能 |

切片 9–15 的共同门禁：

```text
gofmt -l .
git diff --check
go build ./...
go vet ./...
go test <相关业务包> -count=1
```

涉及 pool、aggregator、WSS 或 reference 时，必须增加对应 `-race` 测试。切片 15
结束时再执行：

```text
go test ./... -count=1
go test ./... -race -count=1
go test . -run 'TestArchitecture|TestPackage|TestDefaultBuild' -count=1
go test ./internal/verification/integration -run '^TestVerification' -race -count=2 -shuffle=on
```

测试失败时保留失败证据并停止；不得用删测试、放宽断言、更新 provider 样本或改写
Decision 的方式制造绿灯。测试框架设计已经落库，但不插入切片 9–15 的生产收敛链。

## 切片 15 之后：外部 RPC 测试框架实现

测试框架设计见 [external-rpc-test-framework.md](external-rpc-test-framework.md)，
状态为设计提案，代码实现排在切片 15 和最终审计之后。实现仍需单线推进，并保持
普通 CI 无公网依赖。

| 顺序 | 业务任务 | 主要改动 | 退出条件 |
| --- | --- | --- | --- |
| 1 | HTTP 传输故障场景 | 使用 `httptest` 和 `http.RoundTripper` 覆盖 status、超时、截断、malformed JSON、vendor 字段和 error-data 四态 | HTTP 场景能同时断言 Decision、fault 和来源；无生产接口扩张 |
| 2 | WSS 生命周期场景 | 扩展既有 `wstest`；必要时在已有 connection seam 使用 `net.Pipe` 覆盖帧切片、半关闭、写取消、Close 和原始 payload | WSS contract 与 HTTP 业务语义分层验证；无第二套 WSS server |
| 3 | Anvil fork 语义场景 | 固定 chain、block、fork source 和版本；用启动器管理临时端口、就绪检查、snapshot/revert 和 manifest | 固定历史 block、moving tag、EIP-1898 和状态变化场景可复现；生成物有 digest |
| 4 | 外部证据接线 | 让 livecanary manifest/report 作为外部观察输入，继续区分 passed、failed、inconclusive、not_observed | provider 观察有 provenance；不把 Anvil 或合成 fault 当 provider golden |
| 5 | 测试分层验收 | 更新命令清单和 CI 边界，运行 contract、WSS race、Anvil 专项、架构和 Verification 门 | 默认 CI 离线通过；外部证据流程独立且失败可解释；W4/null 缺口仍按证据状态记录 |

该后置轨道不建立新的 `internal/verification` package，不把 `rpcsample` 改造成永久
规范，也不引入后台重连、自动重试或第二套执行链。只有出现现有标准库 seam 无法表达
的明确缺口时，才另行提交工具评估和决策修订。

## 路线总账

| 轨道 | 当前状态 | 下一出口 | 约束 |
| --- | --- | --- | --- |
| 拓扑迁移、架构收敛、T09 审计 | 已完成 | 只在出现真实新 Seam 或维护负担时重新审查 | 不按目录整齐度拆包 |
| HTTP connection 行为差分审计 | 已完成；L1 contract 已冻结（`transport-contract.md`），native HTTP 为战略默认 | 只在出现新 provider 证据或 Geth 升级时复核 | v1.17.5 仅为版本化 baseline，不是长期标准 |
| 自有 HTTP Adapter | 已实现（`internal/transport/http`）并作为默认装配；Geth compatibility adapter 保留为显式 seam | 无；新增 provider 证据只登记为观测，不重开契约 | 不改变 pool/Decision；保留官方 Ethereum 类型 |
| 逐方法 HTTP/root/ethadapter 对接 | 23 张业务语义卡片已建立；H0 方法基线冻结完成（2026-11-03，[method-plan-inventory.md](method-plan-inventory.md)：23/23 方法 spec↔registry↔catalog 对齐，typed path 与证据缺口逐项登记）；2026-10-31 增加历史 block 18,000,000 的 EIP-1898/createAccessList/block/feeHistory live 证据 | 按卡片补齐剩余真实 capture/replay/live 证据（syncing progress、其他历史高度、uncle 内容等） | 不新增方法，不改变裁决语义 |
| go-ethereum/connection 依赖债务 | native HTTP 默认；显式 raw WSS 已接线；默认构建仍不链接 `ethclient` | 保留官方 typed 类型；compatibility seam 仍显式 | 不移除 ethclient-shaped 表面 |
| WSS transport | 自有 wire 栈已上线（2026-11-03）：标准库握手/帧/连接层替换 gorilla；sticky 失败与单 close 回执语义修复完成；显式统一 pool 接线、读写/health/cooldown/取消/Close 回归通过 | W4/provider-backed golden：继续补真实 null 等证据 | 不新增只读角色、后台重连、重放或 basis 变化 |
| T11 自适应拥塞控制 | T0 受控容量实验与 T1 控制器裁决已完成（2026-11-02，提交 6292d73）：保留现有慢增快减、固定两槽、共享预算与 cooldown 控制器；受控证据未显示 AIMD 或动态并发足以替换它 | 只在出现新的容量证据时重审控制器输入信号 | 不改成员、分母、权重、模式或门槛 |
| reference freshness | 设计不变量已补；2026-10-31 完成短窗口与约 6 分 22 秒扩展窗口 latest/safe/finalized 观测；F1/F2 请求范围切片已接线（2026-11-02）：历史/pending/safe/finalized/显式引用读取豁免 latest tip gate，过期样本按 unknown 处理；新策略未实现 | freshness 规则与 reference 实现单独审查 | 不由方法 adapter 默默改 tag 语义；观测不产生默认 lag |
| reference raw capture | 两个缺口已按独立切片修复并补回归 | 保持采集语义，出现新清空点时重新审查 | 不改裁决、阈值、成员或 reference 语义 |
| ReadPlan / resolver seam | M0–M8 与 RS0–RS5 已完成；0063 保留 `rpc.Policy` 深模块 | 只有真实新增能力/维护负担出现时重审 | 不改变 Decision、quorum、reference、typed 或方法数量 |
| WSS error-data evidence | raw WSS 生产四态按 0056.2 保留；真实 missing/`0x`/value 已观察，null 未观察 | W4 golden 门仍需真实 provider 证据 | 不用合成样本关闭 evidence 门 |
| provider compatibility evidence | 按方法逐步补齐 | capture/replay/live 状态登记并保持证据等级 | 不用合成样本冒充 provider 兼容性 |
| 外部消费者纪律 | 当前无外部消费者 | 首次发布/tag/import 后重启 API break 评估 | 依据决策 0057 |

上述每条轨道的细节、验收和停止条件都在本文后续章节；链接的设计文档提供依据，
不替代本表的顺序和状态。

## 下一轮执行计划：freshness、调度与 transport 依赖

2026-11-01 起按下表单线推进。M0–M8、RS0–RS5、W0–W3 和统一 Source 接线
均已完成，不重新启动 resolver 扁平化。旧决策可由可复现证据修订；外部样本不足
只阻塞对应的兼容性声明，不阻塞离线缺陷复现、机制验证和请求范围修正。

### 目标目录与所有权

- `rpc/` 与 `rpc/methods/` 声明请求语义；使用既有 ReadPlan/reference/pending
  事实，不为 freshness 建立第二方法表，不添加网络 callback。
- `aggregator/` 决定本轮请求的执行范围；`internal/runtime/` 绑定 reference
  建立/确认轮。pool 不解析方法参数或根据 transport 选择业务行为。
- `pool/` 拥有调度预算、健康、tip 样本和 gate 原因；reference 候选算法留在
  `internal/reference/`。二者不改变成员、basis、门槛、单位权重或请求 tag。
- `internal/transport/wss/` 拥有帧库绑定与 socket 生命周期；替换底层库不得扩张
  Transport Port。`cmd/wsscapture/` 仅负责手动只读证据；普通测试不联网。
- `internal/verification/integration/` 验证公开调用的结果和证据；设计依据记录在
  现有 freshness、congestion、WSS 文档，执行事实只追加到执行台账。

### 任务与验收

| 任务 | 顺序与目标 | 交付范围 | 验收与出口 |
| --- | --- | --- | --- |
| F0 请求 freshness 与调度事实基线 | 先执行 | 复现历史/pending/移动 tag 的 gate 范围、样本年龄、稀疏样本，以及当前共享预算；补充现有设计文档 | 真实执行路径有离线证据；明确当前缺陷与目标，不把现状测试当永久契约；确认现有 pace 调整已存在，T11 未完成的是控制器收敛 |
| F1 按请求/轮次限制 tip gate | F0 后的首个生产切片 | `rpc/`、`aggregator/`、`internal/runtime/` 向 pool 显式传递本轮的调度事实；先写历史读取失败的回归，再修复 | explicit height/hash、pending、本地/无块引用方法不因 latest tip 落后而被暂停；moving-tag 建立与 pin 后业务轮分别验收；safe/finalized 不以 latest tip 冒充自身 freshness；cooldown/identity/deadline 不被绕开；basis、阈值与 admission 不变。直接 pool/legacy 调用的零值行为须写明 |
| F2 样本有效性与 gate 诊断 | F1 后 | `pool/` 样本时间、部分覆盖和结构化 gate 原因；相关 reference/capture 诊断 | 过期/缺失样本为未知，不证明 fresh 或 stale；少数已采样高 tip 不可借缩小样本集合暂停诚实成员；expired 样本不参与参考；年龄窗口须显式配置或有依据的决策，无新隐藏默认值；历史请求仍豁免；旧 sentinel 的 errors.Is 保持 |
| T0 调度预算盘点与受控实验 | F0 可先完成，单线执行 | `pool/`、离线容量 fixture、`adaptive-congestion-control.md` | reference/read/write/admission/probe 共享预算；饱和等待取消无额外发送/health 惩罚；容量下降、恢复、限流、空闲与饥饿有确定性场景；不对公共 provider 压测。输出当前控制器与候选的差异，不能写成真实 provider 容量结论 |
| T1 控制器选择与最小实现 | F1/F2 稳定、T0 有结果后 | 优先在现有 monitor 内收敛慢增快减与并发上限；需要独立深模块时再拆目录 | 先提交输入信号/状态/参数来源的决策；区分配额拒绝和容量反馈；突降快速收敛、低负载不虚增容量、恢复有界、所有轮次共享预算、队列等待有界；不新增自动重试、动态成员或方法优先级。没有证据时保留现有算法也是有效裁决 |
| D0 WSS 帧层依赖重新评估 | **已完成（2026-11-03）**：结论为自有实现替换（非保留临时依赖），依据与成本见 [wss-wire-contract.md](wss-wire-contract.md) 与执行台账 §17 | HTTP Upgrade、帧协议、连接生命周期由 Go 标准库实现；不复制第三方库内部，不扩张 Transport Port | 同一组 contract 全部保持（见 D1 验收）；架构互锁锁定生产无第三方 WS 依赖 |
| D1 WSS 底层替换或局部加固 | **已完成（2026-11-03，W1–W7 单线执行）** | `internal/transport/wss/` 自有 handshake/frame/connection；adapter 表面不变；差分验证（过渡期 oracle）后移除 gorilla | presence、demux、malformed、limit、写锁等待/写中取消、Close、断连、ChainID、无重放逐项保持；message 级 sticky 失败与"每连接至多一个协议 close 回执"已修复并测试；`go list -deps . ./internal/transport/wss ./cmd/wsscapture` 无 gorilla（geth 兼容通道的 indirect 残留除外） |
| E0 逐方法与 W4 定向证据 | 独立证据队列，不能阻塞 F/T 主线 | 现有 capture/replay 工具与方法卡片；每轮限定方法、预算、时限 | 优先 syncing progress、含 uncle 的历史块、历史 EIP-1898 与 feeHistory；WSS null 仅自然出现时归档。每个缺口区分实现正确性与真实兼容性；未出现即登记未观察，不反复遍历公共 endpoint；捕获可校验 hash、可回放且脱敏，不提交网络交易、不合成 provider golden |
| L0 WSS 生命周期扩展 | D0 后且存在运维需求 | keepalive、可取消写入/容量限制按独立业务切片推进；后台 reconnect 另行判断 | 每次只新增一种机制，说明故障归属、资源上界和关闭顺序；旧 pending 失败不重放；下一独立调用由 pool 重拨；没有收益证据时保持当前不启用范围，不把全部可选功能列为欠实现 |
| A0 集成验收与债务收尾 | 每个生产切片后，整轮结束汇总 | 唯一路线图、执行台账、决策修订和适当 gate | 事实/目标/外部未观察分别列出；不重开已完成的 RS 工作；只提交本轮文件，保留他人改动；完整检查通过后标记相应任务完成 |

### 执行顺序与接续规则

单线顺序：`F0 → T0 → F1 → F2 → T1 → D0 → D1`（D0/D1 已完成）；E0 在
这些切片间以有界证据轮执行；L0 只有真实运维需求时启动，A0 随每个生产切片执行。
D0/D1 已于 2026-11-03 完成（自有 WSS wire 栈上线，gorilla 直接依赖移除）；
L0 仍以真实运维需求为前提，进入前完成独立设计切片。
F0/T0 先交付基线，不将整轮计划标记完成。F1 的新范围和 F2 的样本策略必须先记录
目标及语义差异，再替换旧现状测试，不能为满足旧断言保留已确认问题。

本轮接续状态：F0 已有代码审计和离线基线；T0 的共享预算、等待取消、容量变化、
突降、恢复、空闲和 probe/reference 竞争场景已用进程内 transport 完成，结果只用于
比较控制器机制，不构成 provider 容量结论。F1/F2 的首个生产切片已接线：请求范围
显式传入 pool，历史/pending/safe/finalized/无块引用读取豁免 latest tip gate，moving
latest 在 reference 建立与 pin 后业务轮分别处理；过期样本按 unknown 处理，并保留
结构化 gate 原因。T1 选择保留现有慢增快减、固定两槽、共享预算和 cooldown 控制器，
不新增参数、重试、动态成员或优先级；后续只在出现新的容量证据时重审控制器输入信号。

接手先读本节、`architecture-recovery.md`、`codebase-topology.md`、
`reference-freshness-policy.md`、`adaptive-congestion-control.md`、
`wss-productionization-decisions.md` 和 `roadmap-execution-ledger.md` 最新条目。
首个范围迁移目标 F1 已完成；provider 探测仍按需执行，gorilla 直接依赖已删除
（2026-11-03），无需再以删除依赖为前置目标。

生产切片最终检查：

```text
gofmt -l .
git diff --check
go test ./... -count=1
go vet ./...
go test ./... -race -count=1
go test . -run 'TestArchitecture|TestPackage|TestDefaultBuild' -count=1
go test ./internal/verification/integration -run '^TestVerification' -race -count=2 -shuffle=on
```

涉及 WSS 再执行 `go test ./internal/transport/wss -race -count=10`；纯文档/测试
切片只执行对应验证，记录实际运行范围，不借用上一轮结果声称全部门禁复跑。

### 本轮决策和提交纪律

- F1 优先解决已知的请求适用范围；F2 的时间/覆盖策略与 T1 参数单独提交，不能
  用等待长窗口 provider 观测推迟历史读取问题的离线修复。
- D0/D1 已完成：重新评估的结论是自有实现替换，gorilla 直接依赖已删除；W4 是否
  观察到 null 与底层库替换无依赖关系（继续成立）。正式扩展 keepalive/reconnect/
  queue 前，按 [wss-lifecycle-extension-contract.md](wss-lifecycle-extension-contract.md)
  单独设计与验收。
- 每个生产提交只表达一个业务意图；文件、测试、fixture 和类型以业务语义命名，
  禁止以 F0、T1、D0 等任务号或 phase/stage 名命名。无需为了任务机械新增目录。
- 局部验证使用相关包及有意义的 race/repeat；生产切片完成后执行全量 test、vet、
  race、architecture/default-build 和 Verification 专项门，结果无变化时不机械复跑。
- 本轮新增 ZK/轻客户端证明、订阅、自动写恢复均不在范围；保留 seam 不等于提前
  实现未来能力。真实 provider 缺口不等于现有功能不可用或已知实现错误。

## 已完成主线与 WSS 证据门记录

ReadPlan M0–M8 已经完成了方法级阶段编排迁移，但没有宣称删除所有 Policy 形态。
当前代码必须按两层理解：

```text
方法级阶段层（已计划化）
Validate / Prepare → Reference → Collect → Admit → Resolve → Project

resolver 语义层（RS5 已裁决保留的深模块）
Normalize / cluster / Outcome / Corroboration / CallRevert /
block-specific result / MethodRule resolver columns
```

`rpc/methods` 的方法级链式 constructor（`GetBalancePolicy`、`CallPolicy`、
`LogsPolicy`、`FeeHistoryPolicy`、`CreateAccessListPolicy`、`EIP1898BlockRef`、
`PendingLocal` 等）已于 2026-11-04 删除：`ReadPlan.Resolver` 由 `CompilePlan` 单点
装配，`rpc.Policy` 深模块保留为 resolver 语义层（0063），reference 轮次的阶段映射
契约由 `internal/runtime/round_phases_test.go` 固化。

### Resolver seam 任务表

| 任务 | 内容 | 主要目录 | 前置 | 交付物 | 验收标准 |
| --- | --- | --- | --- | --- | --- |
| RS0 | Resolver 行为事实盘点 | `rpc/`、`rpc/methods/`、`internal/runtime/`、设计记录 | M0–M8 | 每个 RegisteredMethod 的 plan resolver、legacy constructor、wrapper、Policy capability、MethodRule resolver 列和默认 runtime 路径清单 | 23 个方法逐项登记；没有方法因“有 constructor”被误判为 runtime 使用 chain；没有方法因“走 plan”被误判为 resolver 已扁平化 |
| RS1 | Resolver 最小契约 | `rpc/`、`aggregator/` | RS0 | resolver seam 的最小行为契约：Normalize、Resolve/Outcome、Corroborated、错误、canonical value、MethodRule resolver 列 | 计划管线只依赖固定 resolver seam；reference/pending/admission/Projection 不回流到 resolver callback |
| RS2 | Block result seam 评估 | `rpc/methods/`、`rpc/`、`internal/runtime/` | RS0、RS1 | `blockPolicy` 的 header-only/source-spelling/result-form 行为评估；决定保留深模块或迁移到显式 plan hook | hash/number 两种 block 方法在 header-only、full、null、admission、canonical Value 和 BlockRef 上等价；不改变官方 schema 语义 |
| RS3 | Capability/MethodRule seam 评估 | `rpc/`、`rpc/methods/`、`aggregator/` | RS0、RS1 | OutcomeResolver、CorroboratingPolicy、AgreementPolicy、MethodRuleSource 的依赖图和最小化方案 | 计划路径没有新增 decorator 层遍历；能力来源可解释；legacy constructor 的 capability 测试保持独立 |
| RS4 | Legacy constructor quarantine | `internal/runtime/`、架构 interlock、`rpc/methods/` 测试 | RS0–RS3 | runtime 不调用 legacy constructor 的 interlock；legacy constructor 的兼容范围和删除条件 | 默认 runtime 只能从 ReadPlan registry 装配；legacy constructor 只能出现在兼容 facade、对照测试或明确 legacy seam；不存在第二个 registry |
| RS5 | Resolver 是否继续扁平化的决策 | `docs/design/`、`docs/decisions.md` | RS0–RS4 | 基于真实维护成本和语义证据的修订记录 | 明确保留深模块、迁移部分 wrapper 或继续构造 ResolverPlan 三者之一；记录触发证据、范围、回滚和未决问题。**已完成（2026-10-31）：保留 `rpc.Policy` 深模块，见决策 0063 与 `resolver-seam-contract.md` §8** |

Resolver 任务的停止条件：如果 RS0–RS1 证明 `rpc.Policy` 已经是小而深、可测试且不
携带网络控制的 seam，RS2–RS4 可以作为长期 quarantine 收尾，不强行继续扁平化；如果
出现 resolver wrapper 继续携带请求阶段、副作用或隐式网络 callback，必须进入 RS2/RS3
的迁移切片。

RS0 已交付：逐方法 resolver 行为清单见
[`resolver-behavior-inventory.md`](resolver-behavior-inventory.md)（capability
按 resolver 本体的 Go 接口登记；`CallRevert` 无 Outcome、legacy wrapper 的
method-specific capability、plan/chain 不对称等未决问题已登记给 RS1–RS3）。
后续 RS/WSS 切片以该清单为 resolver 事实基线，新增行为先补清单再动代码。

RS1 已交付：resolver 最小 seam 契约冻结在
[`resolver-seam-contract.md`](resolver-seam-contract.md) —— 计划路径已走
CallRevert 的统一 Outcome（execution 维度 verdict，data 状态留在
Dimensions/Revert/Provenance）、capability 由计划编译期双向绑定并一次性读取
（不再按 decorator 遍历），legacy constructor 只保留 establishment 两个
声明 seam。RS2 已将 `blockPolicy` 保留为 block-specific 深模块，并把计划与
legacy 的 full-block admission 收敛到同一业务 helper；RS4 完成 quarantine 收尾。
RS3 的 capability interlock 也已补齐：计划编译同时核对 corroboration、Outcome
和 agreement 三个公共边界，避免固定 basis 与 typed/client agreement 语义分叉。

### WSS 证据门任务表

WSS 的目标是 EthGuard 专用的最小调用层，不是通用 WebSocket 客户端，也不是把
`Subscribe` 加入基础 Transport。WSS 任务必须先完成证据门，再决定是否实现。

W0 已交付：`rpc.RPCError.DataPresence` 记录调用级 presence 事实。具备
`rpc.ErrorDataPresence` 的适配器报告 `missing`、`null` 或 `value`；只有解码后
`DataError.ErrorData() == nil` 的路径报告 `unknown`，因此当前 geth WSS 的限制被
准确表达。该字段不参与 `RPCError.Raw()` 的业务比较，未改变 `eth_call` 的弃权、
分母、门槛或 `Decision.Value`；WSS 四态 golden 仍保持禁止。

WSS 扩展契约已补齐为设计-only 文档：
[`wss-lifecycle-extension-contract.md`](wss-lifecycle-extension-contract.md)。它
冻结 reconnect、keepalive、backpressure 的边界、错误和资源语义，不进入生产实现；
真实 provider 证据和配置决策仍是后续入口。

| 任务 | 内容 | 主要目录 | 前置 | 交付物 | 验收标准 |
| --- | --- | --- | --- | --- | --- |
| W0 | Presence 限制准确表达 | `internal/transport/`、`rpc/`、`internal/uncertain/`、诊断文档 | 无 | 把 geth WSS `ErrorData() == nil` 表达为“missing/null 不可知”的调用级诊断 | 不改变 `eth_call` 比较键、弃权语义、分母、门槛或 Decision.Value；HTTP 四态保持原状；WSS 不生成四态 golden |
| W1 | WSS 调用层可行性评估 | `docs/design/`、`internal/transport/` 只读验证 | W0 | 自建最小 WSS、上游公开扩展点、维护成本三路线比较；明确是否能取得原始 error envelope | 有进程内验证计划；说明 request ID demux、取消、read limit、close、inflight 清理、重连和 presence 的实现责任；不提交生产 WSS 代码。**已完成（2026-10-31）：W1 通过，自建最小调用层可行；见 `wss-call-layer-feasibility.md`** |
| W2 | 最小 WSS Adapter（仅 W1 通过后） | `internal/transport/wss/` 或等价业务目录 | W1、ReadPlan 稳定 | EthGuard 专用 raw call adapter，接入 `pool.Transport` seam | 单请求/响应、乱序、取消、超时、断连、bounded message、Close 和 pending call 清理通过；不增加基础 Transport 的通用订阅能力。**已完成（2026-10-31）：`internal/transport/wss` 实现 raw call/ChainID/Close；见 `wss-transport-adapter.md`** |
| W3 | WSS 进程内证据 | `internal/transport/contract/`、`internal/transport/wss/`、验证目录 | W2 | 四态 error.data、malformed frame、read limit、close、cancellation、reconnect/reorg/backpressure 场景 | missing/null/`0x`/具体 data 可区分；无资源泄漏；HTTP contract 不回退；不改变 aggregator、reference、Decision、typed 或写路径。**进程内 raw-call 证据已完成（2026-10-31）；reconnect/reorg/backpressure 仍缺；真实 provider presence 证据已采集，null 未观察** |
| W4 | WSS provider-backed golden 升级 | `docs/decisions.md`、replay/capture | W3 + 真实 provider capture | golden 证据门独立于生产接线 | **未完成**：真实 null 未观察，golden 继续禁止；0056.2 仅批准 raw presence 生产语义，0064 接线不关闭 W4。2026-11-01 补采 3×6 只读 probe：missing=10、value=5、null=0、empty-hex=0；详见 `wss-provider-evidence.md` |

WSS 任务的停止条件：W1 无法证明原始 error envelope 可取得，或维护成本会复制完整
客户端连接层时，停在 W1，保留 W0 的“不可知”诊断，不进入 W2–W4。不得用反射、fork
geth 未导出类型、快速扫描 ID 或合成样本代替 WSS 证据。

WSS 生产化采用 [统一 Source 决策](wss-unified-source-write-path.md) 与
[已接受矩阵](wss-productionization-decisions.md)（0056.2/0064）。`transport: wss`
显式 opt-in，缺省 native HTTP；组合根绑定每个固定 Member 的 adapter，无第二套
registry。WSS 与 HTTP 共同参与读写，unsupported 只减少结果支持，不减少 basis。
通用限流分类、read/write shared health、30 秒 cooldown 与 gated emission 已覆盖；
未知写结果不重放。pending 取消保持连接，实际 frame 写失败/read failure/read limit
abort，Close 等在途响应或 context；不启后台 reconnect、keepalive 或 queue。

### 两条主线的顺序与分配

```text
M0–M8（已完成）
├── RS0 → RS1 → RS2/RS3 → RS4 → RS5
└── W0 → W1 → W2 → W3 → W4
```

WSS adapter/构造根接线与 resolver 行为迁移保持隔离。adapter 只拥有 Transport
Port，组合根按 0064 选择 per-source Dialer；不修改方法 plan、quorum、reference、
typed 或写裁决，也不因接线引入 transport 专用业务分支。

命名规则：任务编号只出现在本路线图、issue、分支和提交说明中。生产文件、测试文件、
fixture 和类型必须使用业务语义名称，例如 `resolver_behavior_inventory.md`、
`block_result_projection_test.go`、`resolver_capability_interlock_test.go`、
`wss_error_presence_test.go`；禁止创建 `rs0.go`、`phase_wss_test.go`、
`task_rs2_fixture.go` 等按任务阶段命名的文件。

### 统一验收标准

每个 RS/WSS 切片都必须通过：

```text
gofmt -l .
go test ./... -count=1
go vet ./...
go test ./... -race -count=1
git diff --check
```

Resolver seam 还必须通过：

```text
go test ./rpc/... ./rpc/methods/... ./aggregator/... ./internal/runtime/... -race -count=1
go test . -run 'TestArchitecture|TestPackage|TestDefaultBuild' -count=1
```

并提供一个逐方法 resolver inventory，至少包含：

```text
method / plan resolver kind / resolver implementation / legacy constructor /
network callback present / Outcome capability / Corroboration capability /
MethodRule resolver fields / projector ID / typed path / evidence status
```

WSS 在 W1 之前只允许文档和离线 seam 验证；W2–W4 还必须提供进程内 WebSocket
证据，禁止普通测试联网或用合成 provider capture 冒充真实兼容性。

## 第零阶段：connection/RPC transport seam 与依赖边界

这是逐方法对接的前置阶段。`pool.Transport` 已经是最小调用 seam；native HTTP
adapter 是该 seam 的战略默认实现，Geth compatibility adapter 和 WSS raw-call
adapter 是可选实现。`CallContext` 仍只返回 `error`，raw error envelope 的
`error.data` presence 通过可选 capability 传递。Slice 0 的独立 fake Transport
基线、Slice 1 的 Geth compatibility seam、Slice 1b 的 Adapter relocation、Slice 1c
的中性 protocol-error capability、Slice 1d 的 HTTP 行为差分审计和 native HTTP
Slice 2 均已完成，L1 contract 已冻结在
[`transport-contract.md`](transport-contract.md)。raw WSS 已按 0064 显式接入统一
pool；后台 reconnect、主动 keepalive、queue 等生命周期扩展不在本期实现范围。

本阶段的目标是建立一个由 EthGuard 控制的内部 connection/RPC 调用契约，同时保持
公开 ethclient-shaped 表面和 Ethereum 类型不变：

1. 明确一次 request/response 的 raw result、structured RPC error、presence 信息、
   deadline、取消、关闭和来源归属如何进入 `pool.Transport`；
2. 先把现有 Geth/HTTP Adapter 移出 pool，保持现有请求、健康、错误分类和有界
   body capture 语义；
3. 使 fake Transport 可以独立驱动 pool → aggregator → policy 路径；
4. 让 protocol errors 通过项目拥有的最小 capability 进入 pool/aggregator/write，
   而不是由上层识别 Geth error interface；
5. 记录 go-ethereum `v1.17.5` 与 Execution APIs 版本的兼容基线和升级影响清单；
6. 不改变 quorum、Decision、reference、method policy、typed projection 或方法数量。

不在本阶段做：

- 不移除 go-ethereum；
- 不创建平行 Block/Receipt/Transaction 类型、hex codec 或共识编码；
- 不把 WSS 设为默认 transport，不实现 subscription/filter；显式 per-source
  接线按 0064 实施，不移植 geth 后台重连，不重放未知结果；
- 不把 raw wire 放入 `Decision.Value`、比较键或普通 Observation；
- transport seam 本身不是 presence 证据；raw 生产保真按 0056.2，golden 门独立保留。

出口已满足：fake adapter、native HTTP adapter 和 Geth compatibility adapter 都能
通过同一内部契约驱动现有生产路径，完整 offline、adapter、race 门通过。下一步是
WSS 生产化决策，不在本阶段重开已冻结的 HTTP contract。需要重新定义错误、连接
生命周期或 evidence 语义的部分仍由主维护者设计，不交给 agent 自主决定。

事实审计、最小 seam、分片顺序和风险矩阵见
[HTTP/WSS connection 与 RPC transport seam 迁移设计](transport-seam-migration.md)。

决策 0060、`pool.Transport` 现状和 WSS 根因分别见
[架构负债与客户端依赖](../decisions.md#0060-execution-api-is-normative-client-library-dependence-is-a-tracked-migration-debt)、
[codebase-topology.md](codebase-topology.md) 和
[wss-error-data-presence.md](wss-error-data-presence.md)。

## 第一阶段：逐方法对接

目标是闭合每个已注册 read method 的 policy、root facade、ethadapter、typed projection、
错误形状和验证入口。当前注册表固定为 23 个方法；本阶段不新增 RPC 方法、不改变裁决
语义、不修 reference policy 或 WSS。

### 每个方法的对接卡片

每个方法一个独立切片、一个可回滚提交，至少记录：

1. `rpc/methods` catalog、root `Client`、ethclient-shaped adapter 的对应关系；
2. 对照 go-ethereum v1.17.5 的参数、返回类型和已接受语义差异；
3. Prepare、EIP-1898、moving tag、pending、admission、normalization 和 canonical Value；
4. Decision.Value 到 typed projection、`ethereum.NotFound`、sentinel、revert/error 的映射；
5. anchor、local/median/strict、固定分母和 fault 归属；
6. root/adapter/verification 覆盖状态；
7. provider compatibility evidence 状态：HTTP capture、replay、live observation 或
   明确未验证。离线测试不能替代 provider evidence，也不因缺样本合成 golden。

### 对接顺序

1. **确定性与本地方法**：chain ID、gas/fee scalar、net、peer/listening、block number；
2. **anchored state**：balance、nonce、storage、code、call、estimate gas；
3. **对象和数组**：block、transaction、receipt、block receipts、logs、fee history、
   access list；
4. **兼容性边界**：revert data、pending、EIP-1898 object、NotFound、负 block number、
   `requireCanonical` 和错误形状。

`HeaderByHash`/`HeaderByNumber`、Pending* 全族、hash-specific 便捷方法、订阅、
`SimulateV1`、`eth_getProof`、debug/trace 和交易生命周期方法不因对接阶段自动加入；
它们按 [spec-baseline.md](spec-baseline.md) 的缺口分类保持 root `Call`/Unsupported/
WriteMethod 的诚实边界。

### 方法对接出口

- 23 个 RegisteredMethod 都有一张当前对接卡片；没有隐含的“方法存在即完全兼容”；
- adapter 的支持、root-only、Forwarded、Unsupported 和 WriteMethod 状态明确；
- 每个已支持方法的请求形状、typed 返回和错误语义有离线回归；
- provider 证据缺口单独登记，不通过修改阈值、golden 或测试语义关闭；
- 每个切片运行：

```text
go test ./... -count=1
go test ./... -race -count=1
go test ./internal/verification/integration -run '^TestVerification' -race -count=2 -shuffle=on
go vet ./...
gofmt -l .
git diff --check
```

主维护者负责方法契约、reference、错误、Transport 或 evidence 语义；agent 只接手
已有契约下的局部 Adapter/测试对接。

## 第二阶段：WSS transport 设计与分片实现

WSS 只在逐方法 HTTP 对接和内部 connection seam 稳定后进入。WSS 首先是独立 transport
契约，不是方法 policy 或 quorum 项目。

### WSS 顺序

1. **先承认限制**：当 Geth WSS `ErrorData() == nil` 时，把状态表达为“无法判断
   `error.data` 缺失还是显式 null”，不改裁决行为、不新增比较键、不生成 WSS 四态 golden；
2. **评估 transport seam**：比较自建最小 WSS 调用层、上游扩展点和维护成本；
3. **实现最小 Adapter（若评估通过）**：单请求/单响应、request ID demux、context
   cancellation、bounded message、error envelope presence、乱序、断连、超时清理和
   Close 释放 pending call；
4. **进程内验收后再升级决策 0056**：四态、malformed frame、read limit、close 和
   cancellation 全部验证后，才考虑 WSS 四态 golden；
5. subscription、filter、自动重连、自动重放、未知结果恢复和写路径重连另立契约。

WSS Adapter 只替换 `pool.Transport`，不得改变 aggregator、reference、Decision、
method policy、typed projection、quorum 或方法数量。详细事实和候选路线见
[wss-error-data-presence.md](wss-error-data-presence.md)。

## 并行但不混入方法对接的遗留轨道

- **T11 自适应拥塞控制**：决策 0031 方向已接受；T0 受控实验与 T1 裁决（保留现有
  控制器）已完成，未新增算法、参数或队列机制。它必须保持固定成员、分母、权重、
  模式和 acceptance threshold 不变，不与单个方法对接混合；
- **reference freshness**：决策 0032/0033 的 freshness bounds 和实现对齐仍是
  reference 专题，不由方法 adapter 默默解决；
- **provider compatibility evidence**：T00/T10 的 capture/replay/live 证据按方法
  逐步补齐。`eth_syncing` progress object 等缺口不能用合成样本补成 provider 结论；
- **外部消费者纪律**：决策 0057 的“当前无外部消费者”会在首次发布、tag 或外部
  import 出现时失效，届时 API break 必须重新评估弃用窗口。

## reference raw capture 缺口（已修复）

修复状态（2026-10-31）：两个缺口已分别以独立提交修复。strict 路径在
`jsonsafe.Validate` 前保留原始值，记录时把 post-admission fault 与原始字节
配对；finalized 路径在 runtime 侧用包装的 `reference.HeadReader` 记录
`Read`/`Confirm` 的 observation 与 collect error。`HeadReader` 签名、
`reference.Finalized` 算法、阈值、成员、Decision 和 typed 语义均未改变；
回归见 `internal/runtime/strict_head_raw_capture_test.go`、
`internal/runtime/finalized_head_raw_capture_test.go` 和
`internal/runtime/captured_head_reader_test.go`。以下保留原始缺口描述作为
历史依据。

`WithUncertain` 的原始响应记录在 reference 建立阶段有两个已知缺口。两者都只影响
审计与 provider 兼容性证据，不改变 Decision、quorum、frozen basis、成员或 typed
projection，因此独立排期，不阻塞方法对接，也不随方法 adapter 顺手修改。

1. **strict reference 的 malformed 结果在采集前被清空**：`internal/runtime/strict_head.go`
   在 `jsonsafe.Validate` 失败时设置 `ErrMalformedResponse` 并同时把
   `emissions[i].Val` 置为 nil，`ReferenceStage` 随后才在
   `internal/runtime/reference_stage.go` 中通过 `captureEmission` 记录
   `emission.Val`，因此 malformed 的 reference 响应只留下错误文本、没有原始字节。
   业务读取路径的 `admitPhase` 在 admission 清空值之前采集，两类路径的证据等级
   因此不一致，也与 README 中“malformed responses ... retained in the response
   records when budget permits”的承诺不符。
2. **finalized reference 的轮次完全不进入 InvocationCapture**：`ReferenceStage` 的
   finalized 分支直接调用 `reference.Finalized` 并保持 `emissions` 为 nil，不会
   触发 `captureEmission`；`internal/reference/finalized.go` 的 `reader.Read` 和
   `reader.Confirm` 虽然持有 `HeadObservation.Value` 原始字节，但没有采集 seam。
   启用 `WithFinalizedAnchoring` 时，reference 轮次在响应记录中整体缺席。

修复方向（每个缺口一个可回滚切片）：

- strict：在 `jsonsafe.Validate` 之前保留原始字节的独立副本，让采集读取原始值而
  不是被清空后的值，并补 malformed 原始字节保留的回归测试；
- finalized：在 `internal/runtime` 侧用包装的 `reference.HeadReader` 记录
  `Read`/`Confirm` 的 observation 和 collect error，不改 `HeadReader` 签名、
  不改 `internal/reference` 算法，补离线回归；
- 两个切片都沿用 `internal/uncertain` 的有界采集和截断语义，不把 raw wire 放入
  `Decision.Value`、比较键或普通 Observation。

在修复完成前，对接卡片和 provider 证据登记必须把 reference 轮次的 raw capture
标为不完整，不能据此声称 malformed reference 响应已被保留。

## 不进入当前路线图

- provider 权重、信誉和 failure-domain 模型；
- Ethereum light client、BLS、sync committee、trusted checkpoint；
- 把 provider-backed evidence 写成 consensus、canonicality、finality 或 provider
  independence；
- 仅为扩大方法数量新增 RPC；
- 为目录整齐创建浅层 forwarding package；
- 普通测试联网，或把 live 结果直接写入 capture/golden/testdata；
- 大爆炸移除 go-ethereum 或自建平行 Ethereum 类型系统。

## 执行规则

每个切片只包含一个结构或实现意图，提交说明记录 Module、所有权、不变语义、命令、
失败路径、网络/sample 状态和剩余风险。任何无法证明为纯结构变化的操作都单独设计、
单独提交、单独审计；需要重新定义并发、错误、连接生命周期或 evidence 语义的核心 seam
由主维护者设计，局部且可回滚的 Adapter/测试整理才交给 agent。
