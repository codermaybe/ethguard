# EthGuard 当前路线图

状态：**拓扑迁移、架构收敛与 T09 整体审计已完成，进入逐方法对接和依赖边界收敛阶段**（2026-10-25）

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
| 逐方法 HTTP/root/ethadapter 对接 | 下一阶段 | 23 个 RegisteredMethod 各有对接卡片和独立验收 | 不新增方法，不改变裁决语义 |
| go-ethereum/connection 依赖债务 | 已记录，待逐步收敛 | pool connection/Transport 不再把具体 Geth client 作为长期内部依赖 | 不移除公开 ethclient-shaped 表面，不做大爆炸重写 |
| WSS transport | 评估排在方法对接和 connection seam 之后 | 契约、最小 Adapter、四态进程内验收后再议 golden | 不先改 quorum、policy、typed 或写路径 |
| T11 自适应拥塞控制 | 决策方向已接受，算法待设计 | 参数、队列、恢复和容量变化测试 | 不改成员、分母、权重、模式或门槛 |
| reference freshness | 0032/0033 已接受边界，余量和对齐待专门决策 | freshness 规则与 reference 实现单独审查 | 不由方法 adapter 默默改 tag 语义 |
| provider compatibility evidence | 按方法逐步补齐 | capture/replay/live 状态登记并保持证据等级 | 不用合成样本冒充 provider 兼容性 |
| 外部消费者纪律 | 当前无外部消费者 | 首次发布/tag/import 后重启 API break 评估 | 依据决策 0057 |

上述每条轨道的细节、验收和停止条件都在本文后续章节；链接的设计文档提供依据，
不替代本表的顺序和状态。

## 第零阶段：connection/RPC transport seam 与依赖边界

这是逐方法对接的前置阶段。当前 `pool.Transport` 已经提供最小调用 seam，但 dialer、
HTTP body capture 和 Geth WSS 路径仍由具体 `*ethclient.Client` / `ethereum/rpc`
行为填充；`CallContext` 也只返回 `error`，无法表达 raw error envelope 的
`error.data` presence。这是依赖债务的实际风险点。Slice 0 的独立 fake Transport
基线和 Slice 1 的 Geth compatibility seam 已完成；自有 HTTP/WSS Adapter 尚未开始。

本阶段的目标是建立一个由 EthGuard 控制的内部 connection/RPC 调用契约，同时保持
公开 ethclient-shaped 表面和 Ethereum 类型不变：

1. 明确一次 request/response 的 raw result、structured RPC error、presence 信息、
   deadline、取消、关闭和来源归属如何进入 `pool.Transport`；
2. 先把现有 Geth/HTTP Adapter 移出 pool，保持现有请求、健康、错误分类和有界
   body capture 语义；
3. 使 fake Transport 可以独立驱动 pool → aggregator → policy 路径；
4. 记录 go-ethereum `v1.17.5` 与 Execution APIs 版本的兼容基线和升级影响清单；
5. 不改变 quorum、Decision、reference、method policy、typed projection 或方法数量。

不在本阶段做：

- 不移除 go-ethereum；
- 不创建平行 Block/Receipt/Transaction 类型、hex codec 或共识编码；
- 不实现 WSS Adapter、subscription 或 filter；Geth 当前已有的 WSS 自动重连语义
  不得在替换时无声丢失，需另立契约后再决定；
- 不把 raw wire 放入 `Decision.Value`、比较键或普通 Observation；
- 不把 transport seam 的建立写成 WSS missing/null 已修复。

出口是 fake adapter 和独立的 Geth compatibility adapter 都能通过同一内部契约驱动
现有生产路径，且完整 offline、adapter、race 门通过；自有 HTTP Adapter 是否进入
下一片，由主维护者在行为差分审计后决定。需要重新定义错误、连接生命周期或 evidence 语义
的部分由主维护者设计，不交给 agent 自主决定。

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
