# EthGuard 当前路线图

状态：**拓扑迁移、架构收敛、T09 审计与 ReadPlan M0–M8 已完成，进入 resolver seam 与 WSS 证据门阶段**（2026-10-31）

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
- WSS 的 `error.data` 缺失与 JSON `null` 仍无法区分，这是决策 0056 的显式限制；
- Read 默认 registry 已由显式 `rpc.ReadPlan` 驱动；方法级阶段编排不再以 decorator
  chain 为事实来源；legacy constructor 保留为兼容与等价验证 seam；
- resolver 内部仍保留 `rpc.Policy` 深模块、`blockPolicy` 等 resolver-specific
  wrapper 和 capability seam。这是下一轮边界梳理对象，不应被描述为已完全扁平化；
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
- `go.mod` 固定 go-ethereum 版本以保证可复现，但这形成实现耦合债务：pool 的
  dialer/raw-body 路径仍接触具体 Geth client，typed values/codecs 也使用 Geth 类型；
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

## 路线总账

| 轨道 | 当前状态 | 下一出口 | 约束 |
| --- | --- | --- | --- |
| 拓扑迁移、架构收敛、T09 审计 | 已完成 | 只在出现真实新 Seam 或维护负担时重新审查 | 不按目录整齐度拆包 |
| HTTP connection 行为差分审计 | 已完成，规范契约待裁决 | 冻结 EthGuard HTTP contract，评估自有 Adapter 准入 | v1.17.5 仅为版本化 baseline，不是长期标准 |
| 自有 HTTP Adapter | 规范 contract 已定义，尚未实现 | project-owned wire decoder、HTTP Adapter、差分接入 | 不改变 pool/Decision；保留官方 Ethereum 类型 |
| 逐方法 HTTP/root/ethadapter 对接 | 等待 connection Adapter 出口 | 23 个 RegisteredMethod 各有对接卡片和独立验收 | 不新增方法，不改变裁决语义 |
| go-ethereum/connection 依赖债务 | Slice 0/1/1b/1c/1d 已完成 | 替换 rpc/ethclient 实现，保留官方 Ethereum 类型 | 不移除公开 ethclient-shaped 表面，不做大爆炸重写 |
| WSS transport | 评估排在方法对接和 connection seam 之后 | 契约、最小 Adapter、四态进程内验收后再议 golden | 不先改 quorum、policy、typed 或写路径 |
| T11 自适应拥塞控制 | 决策方向已接受，算法待设计 | 参数、队列、恢复和容量变化测试 | 不改成员、分母、权重、模式或门槛 |
| reference freshness | 0032/0033 已接受边界，余量和对齐待专门决策 | freshness 规则与 reference 实现单独审查 | 不由方法 adapter 默默改 tag 语义 |
| reference raw capture | strict malformed 与 finalized 轮次记录不完整 | 两个独立切片修复并补回归 | 不改裁决、阈值、成员或 reference 语义 |
| ReadPlan / resolver seam | M0–M8 已完成；resolver 内部仍是 `rpc.Policy` 深模块 | resolver 行为盘点、最小 seam 契约、legacy quarantine、是否继续扁平化的证据决策 | 不改变 Decision、quorum、reference、typed 或方法数量 |
| WSS error-data evidence | 当前只能准确表达 missing/null 不可知 | presence 诊断、WSS 调用层可行性评估、四态进程内证据；实现与 golden 后置 | 不先加通用 Subscribe，不改变 `eth_call` 比较键或写路径 |
| provider compatibility evidence | 按方法逐步补齐 | capture/replay/live 状态登记并保持证据等级 | 不用合成样本冒充 provider 兼容性 |
| 外部消费者纪律 | 当前无外部消费者 | 首次发布/tag/import 后重启 API break 评估 | 依据决策 0057 |

上述每条轨道的细节、验收和停止条件都在本文后续章节；链接的设计文档提供依据，
不替代本表的顺序和状态。

## 当前主线：resolver seam 与 WSS 证据门

ReadPlan M0–M8 已经完成了方法级阶段编排迁移，但没有宣称删除所有 Policy 形态。
当前代码必须按两层理解：

```text
方法级阶段层（已计划化）
Validate / Prepare → Reference → Collect → Admit → Resolve → Project

resolver 语义层（下一轮边界）
Normalize / cluster / Outcome / Corroboration / CallRevert /
block-specific result / MethodRule resolver columns
```

`rpc/methods` 中仍存在 `GetBalancePolicy`、`CallPolicy`、`LogsPolicy`、
`FeeHistoryPolicy`、`CreateAccessListPolicy`、`EIP1898BlockRef`、`PendingLocal`
等链式 constructor。它们当前是 legacy compatibility surface 和 plan equivalence
对照实现；默认 runtime registry 不调用它们作为方法事实来源。`ReadPlan.Resolver`
仍可持有 `rpc.Policy`，因为 strict、median、local、CallRevert 和 block-specific
resolver 是有深度的语义 Module。下一轮只判断这个 seam 是否足够稳定，不以“零
Decorator”作为孤立目标。

### Resolver seam 任务表

| 任务 | 内容 | 主要目录 | 前置 | 交付物 | 验收标准 |
| --- | --- | --- | --- | --- | --- |
| RS0 | Resolver 行为事实盘点 | `rpc/`、`rpc/methods/`、`internal/runtime/`、设计记录 | M0–M8 | 每个 RegisteredMethod 的 plan resolver、legacy constructor、wrapper、Policy capability、MethodRule resolver 列和默认 runtime 路径清单 | 23 个方法逐项登记；没有方法因“有 constructor”被误判为 runtime 使用 chain；没有方法因“走 plan”被误判为 resolver 已扁平化 |
| RS1 | Resolver 最小契约 | `rpc/`、`aggregator/` | RS0 | resolver seam 的最小行为契约：Normalize、Resolve/Outcome、Corroborated、错误、canonical value、MethodRule resolver 列 | 计划管线只依赖固定 resolver seam；reference/pending/admission/Projection 不回流到 resolver callback |
| RS2 | Block result seam 评估 | `rpc/methods/`、`rpc/`、`internal/runtime/` | RS0、RS1 | `blockPolicy` 的 header-only/source-spelling/result-form 行为评估；决定保留深模块或迁移到显式 plan hook | hash/number 两种 block 方法在 header-only、full、null、admission、canonical Value 和 BlockRef 上等价；不改变官方 schema 语义 |
| RS3 | Capability/MethodRule seam 评估 | `rpc/`、`rpc/methods/`、`aggregator/` | RS0、RS1 | OutcomeResolver、CorroboratingPolicy、AgreementPolicy、MethodRuleSource 的依赖图和最小化方案 | 计划路径没有新增 decorator 层遍历；能力来源可解释；legacy constructor 的 capability 测试保持独立 |
| RS4 | Legacy constructor quarantine | `internal/runtime/`、架构 interlock、`rpc/methods/` 测试 | RS0–RS3 | runtime 不调用 legacy constructor 的 interlock；legacy constructor 的兼容范围和删除条件 | 默认 runtime 只能从 ReadPlan registry 装配；legacy constructor 只能出现在兼容 facade、对照测试或明确 legacy seam；不存在第二个 registry |
| RS5 | Resolver 是否继续扁平化的决策 | `docs/design/`、`docs/decisions.md` | RS0–RS4 | 基于真实维护成本和语义证据的修订记录 | 明确保留深模块、迁移部分 wrapper 或继续构造 ResolverPlan 三者之一；记录触发证据、范围、回滚和未决问题 |

Resolver 任务的停止条件：如果 RS0–RS1 证明 `rpc.Policy` 已经是小而深、可测试且不
携带网络控制的 seam，RS2–RS4 可以作为长期 quarantine 收尾，不强行继续扁平化；如果
出现 resolver wrapper 继续携带请求阶段、副作用或隐式网络 callback，必须进入 RS2/RS3
的迁移切片。

### WSS 证据门任务表

WSS 的目标是 EthGuard 专用的最小调用层，不是通用 WebSocket 客户端，也不是把
`Subscribe` 加入基础 Transport。WSS 任务必须先完成证据门，再决定是否实现。

| 任务 | 内容 | 主要目录 | 前置 | 交付物 | 验收标准 |
| --- | --- | --- | --- | --- | --- |
| W0 | Presence 限制准确表达 | `internal/transport/`、`rpc/`、`internal/uncertain/`、诊断文档 | 无 | 把 geth WSS `ErrorData() == nil` 表达为“missing/null 不可知”的调用级诊断 | 不改变 `eth_call` 比较键、弃权语义、分母、门槛或 Decision.Value；HTTP 四态保持原状；WSS 不生成四态 golden |
| W1 | WSS 调用层可行性评估 | `docs/design/`、`internal/transport/` 只读验证 | W0 | 自建最小 WSS、上游公开扩展点、维护成本三路线比较；明确是否能取得原始 error envelope | 有进程内验证计划；说明 request ID demux、取消、read limit、close、inflight 清理、重连和 presence 的实现责任；不提交生产 WSS 代码 |
| W2 | 最小 WSS Adapter（仅 W1 通过后） | `internal/transport/wss/` 或等价业务目录 | W1、ReadPlan 稳定 | EthGuard 专用 raw call adapter，接入 `pool.Transport` seam | 单请求/响应、乱序、取消、超时、断连、bounded message、Close 和 pending call 清理通过；不增加基础 Transport 的通用订阅能力 |
| W3 | WSS 进程内证据 | `internal/transport/contract/`、`internal/transport/wss/`、验证目录 | W2 | 四态 error.data、malformed frame、read limit、close、cancellation、reconnect/reorg/backpressure 场景 | missing/null/`0x`/具体 data 可区分；无资源泄漏；HTTP contract 不回退；不改变 aggregator、reference、Decision、typed 或写路径 |
| W4 | WSS 决策与 golden 升级 | `docs/decisions.md`、replay/capture 目录 | W3 + 真实 provider capture | 修订 0056，增加 WSS 四态 golden 或明确继续不支持 | 只有真实 provider/进程内证据都通过才升级；seam 建立本身不等于 presence 已恢复 |

WSS 任务的停止条件：W1 无法证明原始 error envelope 可取得，或维护成本会复制完整
客户端连接层时，停在 W1，保留 W0 的“不可知”诊断，不进入 W2–W4。不得用反射、fork
geth 未导出类型、快速扫描 ID 或合成样本代替 WSS 证据。

### 两条主线的顺序与分配

```text
M0–M8（已完成）
├── RS0 → RS1 → RS2/RS3 → RS4 → RS5
└── W0 → W1 → W2 → W3 → W4
```

RS0/RS1 与 W0/W1 可以并行。W2 之后不得与 resolver seam 的行为迁移混在一个提交
中；WSS 只能填充 Transport adapter 和 contract，不得顺手修改方法 plan、quorum、
reference、typed 或写路径。

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

这是逐方法对接的前置阶段。当前 `pool.Transport` 已经提供最小调用 seam，但 dialer、
HTTP body capture 和 Geth WSS 路径仍由具体 `*ethclient.Client` / `ethereum/rpc`
行为填充；`CallContext` 也只返回 `error`，无法表达 raw error envelope 的
`error.data` presence。这是依赖债务的实际风险点。Slice 0 的独立 fake Transport
基线、Slice 1 的 Geth compatibility seam、Slice 1b 的 Adapter relocation、Slice 1c
的中性 protocol-error capability 和 Slice 1d 的 HTTP 行为差分审计已完成。当前
等待主维护者裁决 EthGuard HTTP contract；自有 HTTP/WSS Adapter 尚未开始。

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
- 不实现 WSS Adapter、subscription 或 filter；Geth 当前已有的 WSS 自动重连语义
  不得在替换时无声丢失，需另立契约后再决定；
- 不把 raw wire 放入 `Decision.Value`、比较键或普通 Observation；
- 不把 transport seam 的建立写成 WSS missing/null 已修复。

出口是 fake adapter 和独立的 Geth compatibility adapter 都能通过同一内部契约驱动
现有生产路径，且完整 offline、adapter、race 门通过；下一步由主维护者裁决 EthGuard
规范 contract，再决定是否实现自有 HTTP Adapter。需要重新定义错误、连接生命周期
或 evidence 语义的部分由主维护者设计，不交给 agent 自主决定。

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

- **T11 自适应拥塞控制**：决策 0031 方向已接受，但算法、参数、队列边界和恢复策略
  仍待设计。它必须保持固定成员、分母、权重、模式和 acceptance threshold 不变，
  不与单个方法对接混合；
- **reference freshness**：决策 0032/0033 的 freshness bounds 和实现对齐仍是
  reference 专题，不由方法 adapter 默默解决；
- **provider compatibility evidence**：T00/T10 的 capture/replay/live 证据按方法
  逐步补齐。`eth_syncing` progress object 等缺口不能用合成样本补成 provider 结论；
- **外部消费者纪律**：决策 0057 的“当前无外部消费者”会在首次发布、tag 或外部
  import 出现时失效，届时 API break 必须重新评估弃用窗口。

## reference raw capture 缺口（后置）

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
