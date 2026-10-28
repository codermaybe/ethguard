# EthGuard 下一阶段架构提案

> **状态：提案（Proposal）/ 2026-10-29 收敛草稿。** 本文综合 2026-10-28/29 的架构讨论与外部评审，用于多方比对与后续决策；不构成已接受契约，不改变 `docs/design/codebase-topology.md`、`docs/design/architecture-record.md` 或已冻结的方法契约。基本方向待引入 GPT 参考后定稿。
>
> 基线：`master` @ `a6aa940`（审计修复已合并，`fix/audit-findings` 已回归；临时 fixlist 已删除）。
>
> 原则性倾向（尚未最终冻结）：link-time 隔离；native HTTP 作为战略默认；不新增渠道选择参数；contract 基线翻转为 EthGuard-native；Read 以 `MethodSpec` 为靶、先落地方案 A；`Invocation` 用阶段值，context 仅作过渡；Reference 建立作为 Read invocation 的内部阶段，runtime 统一装配；Verification 保持正交；WSS/订阅暂时搁置；`eth_getProof` 在范围内但按真实场景数据决定证据语义。**MethodSpec 形态、Invocation 签名、link-time 具体机制**留待 5 类纸上原型与 GPT 复核后冻结。

---

## 0. 摘要

EthGuard 的现有骨架在“名词”上是优雅的：`Decision`、`Observation`、`Corroboration`、`ReferenceEvidence` 和 fixed-basis 不变量共同构成了一套诚实的证据模型。问题集中在“装配”和“执行模型”两处：

1. **装配有平行真相**：同一份方法契约被嵌套装饰器链、能力接口 + 孪生包装、`MethodRule` 逐层投影、context 隐式开关四种东西同时表达。
2. **执行模型单一**：当前只有“一次性 fan-out + 固定基投票”。全量 stateless 读取可以继续用它，订阅、密码学证明、多调用模拟、写入同步则需要各自不同的执行家族。

本文的核心建议不是换掉现有架构，而是：

- Read family 用 `MethodSpec` 把方法装配数据化；
- Stream family 用独立的 `StreamPipeline`，不挤进 `MethodSpec`；
- Verification 作为 Read 的正交证据维度，不污染 `Status`；
- Transport 渠道做 link-time 隔离，native 默认，ethclient 退为可选兼容通道；
- 用 EthGuard 自有 contract suite 拥有行为契约，而不是依赖 ethclient 的行为稳定性。

---

## 1. 当前骨架

### 1.1 数据流

```text
ethadapter.Client ─┐
                   ├─> ethguard.Client (root facade)
eth.go / call.go ──┘        │
                            ├─> internal/runtime（唯一业务 Composition Root）
                            │       ├─> rpc/methods（方法语义与 policy 装配）
                            │       └─> aggregator（Prepare → Collect → Admit → Resolve）
                            │               └─> pool（成员/健康/调度）─> Transport Port ─> geth / native HTTP
                            └─> internal/typed（唯一生产 typed projection）

root anchor.go ──> internal/reference ──> pool（HeadReader Port）
```

一次读取：

```text
Prepare（moving-tag 改写）
→ CollectVoting / Collect / Forward / Broadcast
→ jsonsafe
→ Admit（可同源取数）
→ Normalize / cluster
→ Decision + Evidence
→ internal/typed 投影
```

### 1.2 三个正交面

| 面 | 拥有 | 不知道 |
| --- | --- | --- |
| `pool` | 连接生命周期、健康、tip/freshness、调度、fan-out | Ethereum 字段语义、裁决规则 |
| `rpc` | Request / Observation / Decision / Policy / Corroboration / Strict / Median / Local | 具体方法的 wire 形状 |
| `rpc/methods` + `rpc/internal` | 请求绑定、admission、比较键、规范编码 | 连接、quorum 循环 |

### 1.3 接缝

- Transport Port：`pool` 消费的最小上游连接契约。
- HeadReader Port：`internal/reference` 消费的最小 head 读取契约。
- Recorder：`observe` 的被动观测契约。
- typed projection：root facade 与 `ethadapter` 共用的唯一解码路径。

### 1.4 目标目录架构（Reference 阶段化之后）

```text
ethguard/
├── anchor.go / client.go / call.go / eth.go / write.go
│   └── 公共 facade；anchor.go 只转发给 runtime-owned ReferenceService
│
├── aggregator/
│   ├── aggregator.go
│   ├── invocation.go
│   ├── phases.go
│   └── reference_stage.go
│       └── 一次 Read invocation 的 coordinator 与 consumer-owned seam
│
├── pool/
│   ├── collect.go / source.go / monitor.go / tip.go
│   └── transport.go
│       └── 只消费 internal/transport 的 Port；不拥有 adapter 实现
│
├── rpc/
│   ├── basis.go / invocation.go / outcome.go
│   │   └── Basis、Invocation、SourceVerdict、Outcome 等通用值模型
│   ├── reference.go
│   │   └── ReferenceKind、ReferenceRequest、ReferenceEvidence
│   └── policy.go / adjudicate.go / acceptance.go / ...
│
├── rpc/methods/
│   ├── spec.go / compile.go
│   ├── reference_requirement.go
│   └── eth_*.go
│       └── 方法契约；只声明 reference 需求，不发起网络请求
│
├── rpc/projection/
│   └── ObservationProjector 实现；不承担 typed facade 解码
│
├── internal/runtime/
│   ├── runtime.go
│   ├── reference_stage.go
│   ├── reference_service.go
│   ├── pool_head_reader.go
│   ├── method_registry.go
│   └── projector_registry.go
│       └── 唯一生产装配：方法、ReferenceStage、projector、aggregator
│
├── internal/reference/
│   ├── head.go / head_view.go
│   ├── strict.go / finalized.go
│   └── *_test.go
│       └── HeadReader/HeadCodec seam 与纯选择、确认、reorg 算法；不依赖
│           pool、rpc/methods，不构造 aggregator
│
├── internal/typed/
│   └── Decision.Value 到公开 Go 类型的唯一生产投影
│
├── internal/transport/
│   ├── transport.go / errors.go
│   │   └── Port-only：Transport、Dialer、生命周期与中立 sentinel
│   ├── http/
│   │   └── native HTTP adapter
│   ├── geth/
│   │   └── ethclient/gethrpc adapter；唯一允许 import ethclient 的生产目录
│   └── contract/
│       └── 不依赖 ethclient 的 L1 contract harness
│
├── gethcompat/
│   └── 过渡期显式构造入口；不改变 root facade 的默认行为
│
└── ethadapter/
    └── ethclient-shaped facade；不建立第二套执行图
```

目标依赖图：

```text
ethadapter ──> root facade ──> internal/runtime
                                      ├──> aggregator ──> pool ──> internal/transport
                                      ├──> rpc/methods ──> rpc
                                      ├──> internal/reference ──> rpc
                                      ├──> rpc/projection
                                      └──> internal/typed

anchor.go ──> runtime.ReferenceService
internal/transport/geth ──> internal/transport
internal/transport/http ──> internal/transport
gethcompat ──> internal/transport/geth + root facade
```

硬规则：`aggregator` 不导入 `internal/runtime`；`internal/reference` 不导入
`pool`、`rpc/methods` 或 `aggregator`；`rpc/methods` 不导入 projection/typed；
生产代码中只有 `internal/runtime` 可以组合 Reference、Method policy、projector
和 aggregator。任何迁移任务若需要增加第二个组合点，必须先停止并回到架构评审。

---

## 2. 设计哲学评估

现有实现符合以下原则：

- **Ownership-oriented / Parnas 信息隐藏**：目录 = Module + Interface + 允许的依赖。
- **Functional core / imperative shell**：`strict.cluster/adjudicate` 是纯函数，`pool` 和 `aggregator` 是外壳。
- **能力式装饰器**：`Policy` 只有三个方法，可选能力靠接口断言。
- **证据本体论 + 诚实状态阶梯**：`Unknown / Corroborated / Selected / Forwarded`，并明确“provider evidence，不是共识/证明”。
- **Fixed-basis 不变量**：成员与门槛在发请求前冻结，分母不因失败收缩。
- **可执行架构**：依赖 interlock、方法 inventory、零测试 allowlist、moving-tag 矩阵。

主要偏差：

- 装配层的四份平行表示（见 3.4）；
- reference 链在业务 Composition Root 之外自成一套（见 3.1）；
- 共识门槛公式有两份实现（见 3.2）；
- 一次性执行模型无法覆盖 stream / verification / simulation。

---

## 3. 结构发现（按影响排序）

### 3.1 Reference 是第二套装配，且越过了拓扑边界

- `internal/reference` 导入 `rpc/methods`，直接用 `methods.BlockByNumberPolicy().Normalize` 解码 head。
- 拓扑终审稿规定 Reference → Pool/RPC，方法绑定只在 Composition Root。
- `aggregator.New` 有三个构造点：`runtime.Build`、root `anchor.go`、`internal/reference/custom.go`。
- `reference.Custom` 只被 `confirmedAnchor` 调用，而 `confirmedAnchor` 只在 `New` 已拒绝的 `WithAnchorResolver` 分支安装，生产不可达。

**方向（已定）**：仅抽 `HeadCodec` 不能移除请求绑定依赖。Reference 解耦采用三层：

- `reference.HeadReader`：只取原始 head 响应（finalized tag / 指定高度确认）；
- `reference.HeadCodec`：只把原始响应解码成 `HeadView`；
- `runtime.PoolHeadReader`：持有 pool 与方法名，把 runtime 的请求绑定注入 `HeadReader`。

`custom.go`、`finalized.go` 中的 `methods.BlockByNumberPolicy` 引用一并移除；`reference` 不再 import `rpc/methods`。`PoolHeadReader` 目前在 reference 内部（`internal/reference/head.go`），必须迁到 runtime 才能移除方法名绑定；迁移后成员快照由 `Invocation.Basis` 显式传入，不再读 `refcontext.Members`。

**Composition Root（2026-10-29 决定）**：生产装配只允许在 `internal/runtime`；root `anchor.go` 保留公共 facade，但只转发给 runtime-owned `ReferenceService`，不再调用 `aggregator.New`。Reference 建立是 Read invocation 的内部阶段，不是第二套组合根；迁移期允许旧 resolver 通过适配器接入，但不得新增第二个 aggregator 构造点。

**执行（2026-10-29 收敛）**：这是低成本、可独立并行的一项；先定义 HeadReader/HeadCodec 接口并把 `PoolHeadReader` 移到 runtime，再决定 `Custom` 去留；不要求等 transport 阶段完成。

### 3.2 共识门槛公式有两个所有者

- `rpc/acceptance.go`：`required = max(floor, basis/2+1)`。
- `internal/reference/finalized.go`：`required = max(2, quorum, len(emissions)/2+1)`，随后又手工检查一次。
- `client.go` 注释承认“reference paths keep their own formulas until T03 aligns them”。

**方向（待定）**：先定义语义，再谈去重。统一前必须写清：basis 的定义（成员集合是否包含未尝试/被调度门跳过的成员）、quorum floor、最小成员数、finalized/reference 是否有更高门槛、失败与成员缺失是否改变分母。用表驱动测试锁定两套公式在各自场景的预期，再判断能否合并；reference 的手工后检要么删除，要么作为同一计算器的断言。

**执行（2026-10-29 收敛）**：这是 Outcome/Invocation 之前的语义前置项；先写语义表与表驱动测试，不急着删除旧实现。

### 3.3 `aggregator.Aggregate` 是长流程单函数

约 240 行，混合 invocation 状态、四路采集、uncertain 捕获、jsonsafe、并发 admission、字段聚合、两条早退、resolve、agreement gate、projector、fault 装配和观测事件。阶段顺序只活在注释里。

**方向（待定）**：`Invocation` 值 + 命名阶段函数（`Prepare → ReferenceStage → Collect → Admit → Resolve → Project`）；先写一页设计，不中途改。

### 3.4 能力组合的“双胞胎”与 `MethodRule` 影子模型

- 审计后纯转发已收进 `rpc.Decorator` 基座，但“能力引入”仍靠孪生包装：`corroboratingAnchoredPolicy`、`corroboratingPendingPolicy`、`corroboratingHeightPolicy`、`corroboratingFieldsPolicy`、`corroboratingEIP1898Policy`、`corroboratingPendingLocalPolicy` 共 6 个。
- 能力提升不能统一放在最外层：包 Median 不得伪装成 corroborated，包 Strict 必须转发 snapshot；编译器必须由 `ResolverSpec.Kind` 决定能力，而不是按装饰层数提升。
- `MethodRule` 不是手写索引：`docs/design/method-rules.md` 由 `TestMethodRulesMatchIndex` 生成，风险在于 `inspectMethodRule` 逐层 type-assert——编译器扁平化链后，即使行为一致也会误报。

**方向（待定）**：
- 方案 A：保留装饰器，只补 `Invocation`/`Outcome` 等语义收敛；
- 方案 B：`MethodSpec` + `Compile`，由编译器一次性决定能力提升与 `MethodRule` 投影；编译结果必须保持 `MethodRuleSource` 链，或先提供行为级等价索引并迁移测试；
- 方案 C：`Policy` 改为函数字段 struct（能力记录），取消可选接口与装饰器。

### 3.5 Context 是隐式控制通道

`refcontext` 同时承载 reference、members、corroboration snapshot、anchor tag、corroboration-required。`Aggregate` 会从 ctx 继承 snapshot。

**方向（已定）**：五个 key 收敛为一个 `Invocation` 值；context 仅作过渡载体。Policy 签名保持不变；显式参数不作为本轮迁移前提。

### 3.6 观测层在做第二次语义判定

`usableAnswer` 为填 `SourceEvent.Failed` 会再次调用 `policy.Normalize`；对 call-revert/median/local 可能与 resolver 真实语义分叉。

**方向（待定）**：resolver 返回每源 verdict（`Outcome`），观测层只消费。

### 3.7 Root facade 的退役化石与两阶段构造

`CachedAnchor`、`confirmedAnchor`、`WithAnchorResolver`、`WithAnchorCaching` 仍公开存在；`New` 保留不可达分支；`FinalizedHeadAnchor` 的 `quorum/timeout` 在构造后就地修补；`AnchorReferenceForTag` 的 latest/safe 两段重复。

**方向（待定）**：审计周期后给正式退役决策；构造参数化；tag 解析合并。

### 3.8 Status/Error 不变量仍靠约定，interlock 覆盖小于文档

`Status Unknown iff error` 只写在注释；大量手工 Decision 字面量；架构 interlock 只检查 3 条规则，Reference → Methods 之类违例逃过检查。

**方向（待定）**：`Decision.Fail` 构造器；表驱动依赖矩阵 interlock。

---

## 4. 方法矩阵

图例：`R` resolver（S=Strict, M=Median, L=Local, C=CallRevert, W=Write）；`Anchor` `h(pos)`=hash、`H(pos)`=height、`F(pos,fields)`=字段高度；`Pending` `L(pos)`=local、`R(pos)`=reject；`Key→Value` `E`=encoder/规范字节、`N`=normalizer、`O`=canonical object、`X`=多维；`Class` D=纯数据、H=需要钩子、X=逃生口、W=界外。

| 方法 | R | Anchor | Pending | EIP-1898 | Admit | Key→Value | Class |
| --- | --- | --- | --- | --- | --- | --- | --- |
| eth_chainId | S | — | — | — | — | E→E | D |
| eth_getBlockByHash | S | — | — | — | block | N→O | H |
| eth_getBlockByNumber | S | —（anchor 角色） | L0 | — | block | N→O* | H |
| eth_getBalance | S | h1 | L1 | ✓ | — | E→E | D |
| eth_getTransactionReceipt | S | — | — | — | receipt | E→E | H |
| eth_getTransactionByHash | S | — | — | — | tx | N→O | H |
| eth_getBlockReceipts | S | h0 | R0 | — | receipts | E→E | H |
| eth_getTransactionCount | S | h1 | L1 | ✓ | — | E→E | D |
| eth_getStorageAt | S | h2 | L2 | ✓ | — | E→E | D |
| eth_getCode | S | h1 | L1 | ✓ | — | E→E | D |
| eth_call | C | h1 | L1 | ✓ | —（维度内建） | X | **X** |
| eth_estimateGas | M | h1 | L1 | ✓ | — | E→E | D |
| eth_getLogs | S | F0(from,to) | R-bounds | — | logs | E→E | H |
| eth_feeHistory | S | H1 | L1 | — | fee+range | E→E | H |
| eth_gasPrice | M | — | — | — | — | E→E | D |
| eth_maxPriorityFeePerGas | M | — | — | — | — | E→E | D |
| eth_blobBaseFee | M | — | — | — | — | E→E | D |
| eth_blockNumber | M | — | — | — | — | E→E | D |
| eth_createAccessList | S | h1 | L1 | ✓ | — | E→E | D |
| eth_peerCount | L | — | — | — | — | E→E | D |
| eth_syncing | L | — | — | — | — | E→E | D |
| net_version | S | — | — | — | — | E→E | D |
| net_listening | L | — | — | — | — | E→E | D |
| eth_sendRawTransaction | W | — | — | — | — | 本地 hash 绑定 | **W** |

\* `eth_getBlockByNumber` 的 header-only 响应保留源拼写，full 响应是 canonical object。

分类：**15 个纯数据**、**7 个需要钩子**、**1 个逃生口**（`eth_call`）、**1 个界外**（`eth_sendRawTransaction`）。

### 4.1 三个真正的结构性异常

1. **`eth_call`**：双维 resolver（execution + revert data），`RPCErrorBodies`、`RevertContext`、`Dimensions`、`DeletedFields`、`Provenance`。必须保留 `Custom`/`CallRevert` 逃生口。
2. **`eth_getBlockByNumber`**：业务读、anchor comparator、head decoder 三重身份；header-only 值与 full 值形态不同；`byHash` 影响 admission。**建议先抽 `BlockHeadCodec`**，否则 MethodSpec 会把 codec 藏进编译后的 Policy，reference 无法复用。
3. **`eth_getLogs` / `eth_feeHistory`**：Validate 必须先于 Anchor；logs 是字段级高度锚定 + 响应 admission；feeHistory 是三参数校验 + 高度锚定 + range admission。`Validate`、`Admit` 必须是 v1 钩子。

### 4.2 五个容易漏掉的维度

1. `KeySource` 三态：canonical-bytes / normalizer / multi-dimensional。
2. `Normalize` 与 `Encode` 是两个函数；`TestAdmissionAcceptanceDomain` 已钉住“encoder 接受 ⇒ normalizer 接受”。
3. `ResultForm` 是请求形状的函数（header-only 源拼写、call revert envelope、canonical）。
4. `Admit` 需要同源 `AdmissionCaller`（block uncle headers）。
5. `Projector` 目前在 runtime 的第二个 map；MethodSpec 用 `ObservationProjectorID` 引用它并由 runtime 绑定，否则装配仍分裂。

---

## 5. Read family：MethodSpec 提案

### 5.1 目标形态

```go
type MethodSpec struct {
    Method      string
    Resolver    ResolverSpec      // strict | median | local | call-revert | custom
    Normalize   Normalizer
    Encode      CanonicalEncoder
    KeySource   KeySource         // canonical-bytes | normalizer | multi-dimensional
    Anchor      *AnchorSpec       // request-shape positions/EIP-1898; no network callback
    Reference   *ReferenceRequest // declared need; network executed by ReferenceStage
    Pending     PendingMode       // none | local | reject
    Validate    RequestValidator  // before anchor
    Admit       AdmissionFunc     // after collect, with AdmissionCaller
    Result                   ResultForm      // canonical | source-header-only | representative | custom
    ObservationProjectorID   string          // runtime-bound rpc.ObservationProjector key
    // Single 不是字段：先从 Resolver/Policy 能力派生，原型阶段确认。
}

func Compile(spec MethodSpec, deps Deps) (rpc.Policy, rpc.MethodRule)
```

`Deps` 只携带静态运行时绑定（anchor、ObservationProjector registry 等），不携带 geth/typed 实现类型；`AdmissionCaller` 依赖当前 invocation/source/context，只能由 aggregator 在执行阶段注入，不能进入 `Deps`。`rpc/methods` 不 import `rpc/projection` 或 `internal/typed`。

编译器一次决定：

- 装饰器链顺序：`Validate → Anchor/EIP-1898 → Pending → Collect → Admit → Resolve → ResultForm`；
- 能力提升由 `ResolverSpec.Kind` 决定，不由装饰层数决定：包 Median 不得伪装成 corroborated，包 Strict 必须转发 snapshot；
- `MethodRule` 由 spec 纯投影；编译结果必须保持 `MethodRuleSource` 链，或先提供行为级等价索引；
- 孪生包装与纯转发样板消失；
- `Custom` 是受控逃生口：每个 Custom 必须有理由、负责人、测试和退出条件，不得成为第二套方法模型；
- 字段集封闭：新增维度先走 `Custom`，不为单方法扩 Spec 字段。

### 5.2 `Invocation` 值

```go
type Invocation struct {
    id                   InvocationID
    basis                Basis
    reference            *ReferenceEvidence // 内部副本；Reference() 返回只读副本
    requireCorroboration bool
    capture              *uncertain.Capture
}

func (in Invocation) ID() InvocationID
func (in Invocation) Basis() Basis
func (in Invocation) WithReference(ref ReferenceEvidence) (Invocation, error)
func (in Invocation) Reference() (ReferenceEvidence, bool)
```

归属与规则：

- `Invocation`、`Basis`、`InvocationID`、`SourceVerdict` 属于 `rpc`；`Basis` 只保存 `Members []SourceRef` + `Corroboration`，不保存 `*pool.Source`；
- 每次逻辑调用一个 `InvocationID`，用于 trace/观测关联；所有阶段共享同一个 ID；
- 成员顺序不是投票证据；诊断输出按 `Source.ID` 排序，单源转发的顺序由 `SingleSource` 策略单独决定；
- `Require` 改名为 `RequireCorroboration`，避免与一般一致性门混淆；
- `WithReference` 接收值并 `Clone` 内部切片；`Reference()` 返回副本，避免共享 slice；
- `Basis` 创建后不可变；`Reference` 只能在 anchor 阶段添加一次，重复添加返回错误；
- `Prepare`/`Anchor` 看到无 reference 的 Invocation，`Resolve` 看到完整 Invocation；其他阶段若需要 reference 必须显式声明；
- Invocation 按值传递，可安全复制到并发分支；`Capture` 的并发安全由 `uncertain.Capture` 自身保证。

**Reference ↔ Aggregate 边界（2026-10-29 决定）**：目标不是“两次 Aggregate 共享 Basis”，而是 **Reference 建立作为一次 Read invocation 的内部阶段**。`reference` 算法仍归 `internal/reference`，但装配与执行由 `runtime` / `aggregator` 统一拥有。两次 Aggregate 共享 Basis 只作为迁移步骤，不得成为目标架构。

目标执行图：

```text
Invocation / Basis freeze
→ Validate / Prepare
→ ReferenceStage.Establish（需要时；可含多轮网络）
→ PrepareWithReference
→ CollectVoting
→ Admit
→ Resolve Outcome
→ Project / Observe
```

“一次 Aggregate”不表示一次网络请求：移动标签至少两轮，finalized 还可能有确认轮。约束是：一个逻辑调用只有一个 Invocation、一个 Basis、一个 invocation coordinator；reference 轮次与业务轮次可以有不同的阶段语义，但不能有不同的成员、门槛或错误装配。

```go
type ReferenceRequest struct {
    Kind   ReferenceKind // latest | safe | finalized | custom
    Params []any
}

type ReferenceStage interface {
    Establish(context.Context, Invocation, ReferenceRequest) (Invocation, error)
}

// ReferenceRequirement 是可选 policy 能力：aggregator 在 Prepare 后读取
// 方法声明的 reference 需求，再调用 ReferenceStage。
type ReferenceRequirement interface {
    ReferenceFor(Request) (ReferenceRequest, bool)
}
```

归属：

- `ReferenceRequest`/`ReferenceKind` 属于 `rpc`，由方法 spec 声明；
- `ReferenceRequirement` 由 `rpc/methods` 生成的 policy 实现，只声明需求，不发起网络；
- `ReferenceStage` 接口由 `aggregator` 消费（consumer-owned），`runtime` 实现；
- `internal/reference` 只拥有 HeadView、HeadCodec seam、选择/确认/reorg 算法；不依赖 pool、rpc/methods，不构造 aggregator；
- `runtime` 构造 ReferenceStage（PoolHeadReader、方法名绑定、HeadCodec、策略选择），并拥有生产装配；
- `aggregator` 在同一 Invocation 内调用 ReferenceStage，接收带 reference 的新 Invocation；
- `anchor.go` 保留公共 facade，只转发给 runtime-owned `ReferenceService`，不再调用 `aggregator.New`；
- `runtime.ReferenceService` 是 facade 侧的转发目标，`ReferenceStage` 是 aggregator 侧的阶段接口；两者由 runtime 构造并共享同一算法与成员快照；
- `rpc/methods` 的 anchor decorator 不再通过 callback 发起网络请求，只声明需要哪一种 reference；
- `runtime.Inputs` 的 `AnchorHash`/`AnchorHeight` 网络回调在 R4 删除，由 `ReferenceStage` 取代。

目标：一次逻辑调用只有一个 basis；anchor 建立和业务读取共享同一个 `Invocation`，而不是靠五个 context key 隐式传播。

### 5.3 `Outcome`

```go
type SourceVerdict struct {
    Source rpc.SourceRef
    Kind   VerdictKind // supported | dissented | unusable | absent
    Err    error
}

type Outcome struct {
    Decision rpc.Decision
    Verdicts []SourceVerdict // 按 Source.ID 排序
}

// OutcomeResolver 是可选能力；Policy 的公开签名保持不变。
type OutcomeResolver interface {
    ResolveOutcome([]Observation) (Outcome, error)
}

// CorroboratingOutcomeResolver 镜像现有 ResolveCorroborated 的固定基拆分。
type CorroboratingOutcomeResolver interface {
    ResolveOutcomeCorroborated(Corroboration, []Observation) (Outcome, error)
}
```

目标：resolver 一次说清每源判定；`Decision`、`Faults`、`observe.SourceEvent.Failed` 都从 `Outcome` 投影，不再二次 `Normalize`。

过渡方式：aggregator 优先检查 `OutcomeResolver`；带固定基的 policy 检查 `CorroboratingOutcomeResolver`；有则消费 `Outcome` 并投影，没有则回退到 `Policy.Resolve`/`ResolveCorroborated`。这样方案 A 不破坏现有 `Policy` 接口，`SourceVerdict` 必须携带 source identity 与错误，否则无法可靠投影到 `SourceEvent`/`Faults`。

### 5.4 候选方案

| 方案 | 动什么 | 收益 | 代价 |
| --- | --- | --- | --- |
| A 收敛副本 | Invocation 阶段值、Outcome、单一门槛、reference `ReferenceStage`（HeadReader/HeadCodec） | 删并行真相，公共 API 稳定 | 孪生包装与逐层投影仍在 |
| B 装配数据化 | A + MethodSpec/Compile | 孪生与逐层投影消失，方法契约一眼可见 | 新编译器概念；特殊方法需逃生口 |
| C 能力记录化 | `Policy` 改为函数字段 struct | 概念最少，无类型断言 | 公共 API 破坏；链的可读性下降 |

**当前倾向：A 先行、B 为靶、C 只在允许破坏公共 API 时考虑。** 采用 B 前必须通过 **5 类纸上原型门禁**：

1. `eth_getBalance`：纯 fan-out + encoder/normalizer；
2. `eth_getLogs`：Validate-before-Anchor 钩子 + 字段级高度锚定 + 响应 admission；
3. `eth_call`：CallRevert 逃生口 + 双维 resolver + `RPCErrorBodies`；
4. `eth_getBlockByNumber` header-only：`ResultForm`/representative 双形态，同时覆盖 reference 的 head 解码需求；
5. `eth_chainId` 或 `net_version`：local single-source，验证单源执行路径（`SingleSource`）。

`eth_feeHistory` 作为可选第六类，覆盖 range admission 与三参数校验。门禁要求：现有矩阵测试不改即通过；能力提升与 `MethodRule` 投影有等价性说明；特别检查 ObservationProjector 所有权（`ObservationProjectorID` + runtime 绑定，factory 不进 Spec）与 Custom 边界；原型未通过不得开写编译器。门禁通过后再冻结 `MethodSpec` 字段集，字段集一经冻结，新增维度只能走 `Custom` 逃生口。

### 5.5 风险

- `MethodSpec` 可能长成吸收所有怪癖的 god struct；必须保留 `Custom` 逃生口与每方法 escape hatch，字段集只减不增。
- 编译器改变装饰器组合顺序可能影响能力断言；现有 `admission_suite`、`moving_tag_matrix`、`representative`、`method_rules` 是安全网。
- 能力提升必须由 `ResolverSpec.Kind` 显式决定；包 Median 不得凭空获得 `CorroboratingPolicy`。`rpc/corroboration_composition_test.go` 已钉住该不变量，编译器必须复用同一测试。
- `MethodRule` 的逐层投影在编译后可能消失；必须保留 `MethodRuleSource` 链，或先落地行为级等价索引并迁移 `method_rules_test.go`，否则测试矩阵会在迁移期失真。
- `Projector` 若以 factory 进入 Spec，会把 `rpc/methods` 重新拉向 projection/typed 边界；固定用 `ObservationProjectorID` + runtime 绑定。
- `Custom` 若无约束会成为第二套方法模型；每个 Custom 必须有理由、负责人、测试和退出条件。

---

## 6. ethclient 覆盖与执行家族

### 6.1 覆盖现状（go-ethereum 1.17.5，仅作当前 pin）

`ethclient` 公开 47 个方法（不含 `Client()`）；`ethadapter` 现有 20 个。核心 19 个已覆盖。缺口 27 个：

| 类别 | 方法 | 数量 | 现有模型 |
| --- | --- | --- | --- |
| Stateless 变体 | HeaderByHash/Number、Balance/Storage/Code/NonceAtHash、CallContractAtHash、EstimateGasAtBlock/AtBlockHash、PendingBalance/Storage/Code/NonceAt、PendingCallContract、PendingTransactionCount、TransactionCount(blockHash)、TransactionInBlock、NetworkID、BlobBaseFee、FeeHistory | 20 | 候选（MethodSpec + facade，需安全契约评审） |
| Streams | SubscribeNewHead、SubscribeFilterLogs、SubscribeTransactionReceipts | 3 | 不能 |
| Write sync | SendTransactionSync、SendRawTransactionSync | 2 | 写管线扩展 |
| Simulation | SimulateV1 | 1 | Custom resolver |
| 本地推导 | TransactionSender | 1 | facade 本地签名恢复 |

其中 3 个需要新注册方法：

- `eth_getBlockTransactionCountByHash`
- `eth_getTransactionByBlockHashAndIndex`
- `eth_getBlockTransactionCountByNumber`

### 6.2 执行家族

```text
Read family        一次性 fan-out + 固定基投票 + MethodSpec      stateless 全量
Stream family      订阅 + per-source 错误 + per-event 决策        newHeads/logs/...
Verification       读方法 + 本地密码学验证（正交证据维度）       eth_getProof
Write family       广播 + 相对信任；sync 模式是扩展              SendRawTransaction[Sync]
Derivation family  本地推导，不产生 RPC 请求                      TransactionSender、header 投影
```

`MethodSpec` 的 scope 应冻结为 **stateless read only**。Stream/Verification 各自独立。

### 6.3 Catalog 扩展

`MethodDescriptor` 增加 `Family`（Read/Stream/Write/Derivation/Verification）、`Alignment`（最近审阅的 ethclient 版本）、`Coverage`。方法覆盖矩阵与测试 inventory 按家族跟踪。`Coverage` 是独立分类（Registered/Unsupported/Forwarded），不扩展 `MethodKind`；`Forwarded` 表示未注册透明转发，不是 `RegisteredMethod` 的子类。Registered 的准入仍以安全契约为准；ethclient 对齐数量只作结果，不作目标。

---

## 7. Stream family（暂时搁置）

### 7.1 ethclient 订阅面折叠了什么

`ethereum.Subscription` 把错误折叠成一个终态 `Err()` 通道，并丢掉：

- 来源身份；
- 错误分类（传输/协议/解码/溢出/服务端错误/clean close）；
- `ErrClientQuit` 被转成 nil；
- `requestUnsubscribe` 错误被丢弃；
- 原始负载（解码失败直接终止）；
- generation / resume point；
- 背压预警；
- reorg 语义；
- 静默源与空链的区分。

### 7.2 目标形态

- `SubscriptionTransport` 作为可选能力，仅 geth/WSS adapter 实现；native HTTP 不实现；基础 `Transport` 不变。
- `SourceSubscription`：原始 `json.RawMessage` 通道 + 自己的解码 + 错误分类 + generation + cursor + unsubscribe 结果。
- `StreamPipeline`：独立状态机，不经 aggregator。
- 双表面：root `Client` 暴露 `StreamEvent` + `StreamFault`；`ethadapter` 暴露 `ethereum.Subscription` 形状。

### 7.3 订阅类型矩阵

| 订阅 | 链上确定 | 跨源语义 | Reorg | 回填 |
| --- | --- | --- | --- | --- |
| newHeads | 是 | 按 hash/number 投票 | 是 | 缺失高度区间 |
| logs | 是 | 按 log identity 去重 + 投票 | `removed` 提升 | `eth_getLogs` |
| newPendingTransactions | 否（mempool 本地） | per-source Local | 不适用 | 不可靠 |
| syncing | 否（节点本地） | per-source Local | 不适用 | 不适用 |
| transactionReceipts | 是 | 按 receipt hash 投票 | 可能 | 缺失 tx 区间 |

### 7.4 前置条件

在“WSS 搁置 + 不交叉渠道”的前提下，native HTTP 无法承载订阅；订阅只能由 geth 渠道提供（违反不交叉），或等待 native WSS。**Port 现在不要加 `Subscribe`。**

---

## 8. Verification family：eth_getProof（待场景数据）

### 8.1 信任链

```text
corroborated block hash H
→ 从任一源取 H 的 header
→ 验证 header.Hash() == H
→ 用 header.StateRoot 验证 account proof
→ 用账户 storageHash 验证 storage proof
```

block hash 承诺 header，header 承诺 state root；只要 hash 对，单源 header 是可信的。proof 本身自验证，不需要跨源一致；需要 corroboration 的是 reference hash。

### 8.2 证据模型（正交）

```go
type VerificationEvidence struct {
    Kind            string   // account-proof / storage-proof
    Verified        bool
    BlockHash       string
    StateRoot       string
    StateRootSource string   // corroborated-reference / selected / same-source
    Faults          []error
}
```

`Decision.Status` 仍表达 RPC 结果的来源一致性；`Verification` 表达密码学校验。**在数据出来前不新增顶层 `Verified` status。**

### 8.3 场景矩阵

| 维度 | 场景 |
| --- | --- |
| 节点状态 | archive / full / pruned |
| 高度 | latest / safe / finalized / historical |
| proof 类型 | account / storage / 不存在账户 / 空 slot |
| 客户端方言 | geth / erigon / nethermind / besu / reth |
| 失败模式 | unavailable / malformed / root mismatch / key mismatch / reorg |
| 信任 | corroborated hash / selected header / same-source |
| 输出 | balance / nonce / codeHash / storageHash / storage values |

初始倾向：finalized/safe 走强模式（corroborated hash），latest/historical 允许显式弱模式（selected root）并标记证据；不冻结为最终语义。

---

## 9. Transport 渠道与 contract

### 9.1 当前倾向

- native HTTP 为战略默认；ethclient 因行为随版本变化且无法每次强制检查而弃用。
- link-time 隔离：默认构建不链接 `ethclient`。
- 不新增渠道选择参数；ws/wss 目前由 native dialer 在 dial 时拒绝，不做构造期特判；等 native WSS 设计时再处理。
- contract 基线翻转为 EthGuard-native；geth 只作为兼容实现。

### 9.2 当前耦合点

1. Port 与 geth adapter 同在 `internal/transport`；`internal/transport/http` 通过该包间接 import `ethclient`，拆包第一步是让 `internal/transport` 只保留 Port、geth adapter 移到 `internal/transport/geth`。
2. `pool/collect.go` 使用 `upstream.IsClientQuit`（定义在 `internal/transport/transport.go`），geth sentinel 泄漏到 core。
3. `ErrBodyTooLarge` 放在 geth adapter 包，native 与 pool 引用它；`pool` 又以 type alias 转发 `Transport`/`Dialer`/`ErrBodyTooLarge`，Port 独立后这些别名应随 Port 移动或删除。
4. geth HTTP `Close` 是 no-op；native `Close` 拒绝新调用；Port 未规定 Close 后行为。
5. native 只接受 http/https，但 `New` 允许 ws upstream，直到第一次 dial 才失败。

### 9.3 目标拓扑

```text
internal/transport        Port only：Transport / Dialer / ErrBodyTooLarge / ErrConnectionClosed
internal/transport/geth   geth adapter：ethclient/gethrpc 只在这里
internal/transport/http   native HTTP：不 import ethclient
pool                      只消费 Port
ethadapter                ethclient-shaped facade：gethrpc 类型作为 API 形状允许
ethguard/gethcompat       显式构造 geth 通道（过渡期；import internal/transport/geth）
```

**所有权（待冻结）**：`pool` 在语义上拥有 Transport seam；为避免 adapter 与 pool 循环依赖，Port 的物理包位于 `internal/transport`（保持 Port-only；需要独立版本化时再拆 `internal/transport/port`）。实现前必须同步修订 `codebase-topology.md`——其当前写的是“Transport Port 由 pool 定义”。

依赖 interlock：

- 只有 `internal/transport/geth` 可以 import `ethclient`。
- `gethrpc` 只允许 `internal/transport/geth`、`ethadapter`、`cmd/*`。
- `pool`、`aggregator`、`rpc`、`internal/reference` 不得 import 任何 geth client 包。
- 默认构建 `go list -deps github.com/codermaybe/ethguard` 不得包含 `ethclient`。

### 9.4 link-time 候选方案

| 方案 | 做法 | 优点 | 代价 |
| --- | --- | --- | --- |
| A build tag | 默认 native；`-tags geth` 引入 geth adapter 与选项 | 不新增公开参数；默认二进制干净 | 公共 API 随 tag 变化；CI 需跑两套；同一 import path 行为随 tag 漂移 |
| B 独立兼容包 | `ethguard/gethcompat` 显式 import geth adapter，并提供独立构造入口 | 无 build tag；导入图清晰 | 新增一个显式构造 API（不是渠道选择参数） |
| C 彻底删除 geth | native-only | 最简单、真正隔离 | 失去 geth fallback；native WSS 前无法订阅 |

**当前倾向（2026-10-29 修正）：B 作为过渡，C 作为终态；A 仅作短期回退，并必须写明删除版本。**

B 的形态：默认 `ethguard` 只链接 native HTTP；`ethguard/gethcompat` 显式 import geth adapter 并提供构造入口；root facade 不增加渠道选择参数。无论 A/B，公共签名不随 tag 变化；若必须用 A，tag 只决定链接哪个 adapter，不得让同一 import path 在不同 tag 下承担不同公共语义。`WithHTTPAdapter` 去留单列决策。

### 9.5 Port contract

contract 分三层，避免把实现细节写成架构契约：

- **L1 外部行为（所有 adapter 必须一致）**：调用语义、错误类别、context 优先级、Body 上限、非 2xx/429/非 JSON/空 body 的错误分类、Close 幂等与 Close 后调用、ChainID、并发、不参与方法语义。
- **L2 额外限制（仅资源/诊断，且必须文档化）**：非 2xx body 截断长度、诊断字段长度、连接池参数；不得拒绝 L1 认为语义有效的响应，也不得改变 L1 错误类别。
- **L3 诊断差异（允许不同）**：错误文本、trailing data/重复 key 的具体描述；但必须映射到同一 L1 分类。

L1 条款（写死）：

- 调用语义：一次调用一个 JSON-RPC 请求；不自动重试/重放；`result` 只接受 nil、`*json.RawMessage`、指针。
- 错误能力：`rpc.ProtocolError`、`rpc.DataError`、`rpc.ErrorDataPresence`、`rpc.HTTPStatusError`、`rpc.RateLimitError`。
- 上下文优先级：取消/超时优先于响应；非 2xx body 读取同样遵守。
- Body 上限：`limits.MaxBodyBytes`；超限返回中立 sentinel。
- HTTP：非 2xx、429、非 JSON、空 body、trailing data、重复 key 的**错误类别**必须一致；具体诊断文本属 L3。
- 生命周期：`Close` 幂等；Close 后新调用返回中立 `ErrConnectionClosed`；在途调用由自己的 context 决定。
- ChainID：只调用 `eth_chainId`，错误走同一套能力。
- 并发：一个 Transport 可并发调用。Pool 是否全进程共享属于 pool 生命周期策略，不属于 adapter contract。
- 无方法知识：Transport/Dialer 不参与 anchor、quorum、admission。

contract suite 验 L1 可观察行为与错误类别；L2/L3 只记录差异，不要求两个实现内部字节一致。

### 9.6 contract suite 与基线翻转

- 新增 `internal/transport/contract`（测试 harness，不 import ethclient）。
- native adapter 默认跑全套；geth adapter 通过 `gethcompat` 显式构建跑同一套；`-tags geth` 仅作回退。
- 现有 differential test 保留为诊断，不再作为 native 的规范性 gate。
- 文档上把 `http-transport-differential.md` 的 “EthGuard 自有 HTTP contract” 提升为独立规范；geth 的 “audited baseline” 降级为兼容实现说明。
- bump go-ethereum：跑 contract suite + 全量测试；不承诺 ethclient 行为自动对齐。surface manifest 只作为 facade 方法覆盖的可选 bump-time 工具。

---

## 10. 迁移路线

> 进入阶段 0 前先完成原则冻结（见 §11 决策时机）；阶段 4 前必须先通过 5 类原型门禁（见 §5.4）。

| 阶段 | 内容 | 退出条件 |
| --- | --- | --- |
| 0 | 冻结 contract 文档 + `internal/transport/contract` harness | native 全套通过 |
| 1 | 拆包：Port / native / geth；默认 native；client 去掉 geth import；`gethcompat` 过渡（tag 仅回退） | 默认构建 deps 不含 ethclient |
| 2 | link-time interlock + import 矩阵测试 | 默认与 `gethcompat`（或 tag 回退）两套 CI 通过 |
| 3 | geth adapter 对齐 Port contract（Close、错误、body） | 同一套 contract 通过，偏差有记录 |
| 4 | MethodSpec + 通过安全契约评审的 stateless 变体集合（预期约 20 个，数量只作结果）+ 3 个新注册方法（先过 5 类原型门禁） | 每个方法有 Registered/Unsupported/Forwarded 分类与安全契约证据；现有矩阵全绿；能力/MethodRule 等价 |
| 5 | SimulateV1（Custom）、write sync、TransactionSender | 各自契约测试通过 |
| 6 | 订阅（native WSS 完成后） | 设计稿先于实现 |

依赖关系：阶段 0–3 是渠道与契约基础；阶段 4 是读模型主工程；阶段 5–6 是家族扩展。**2026-10-29 收敛：阶段 0、阶段 1 的拆包、ReferenceStage 迁移、5 类原型可并行；阶段 4 只以原型门禁为准入，不等待阶段 0–3 全部完成。方法数量不是退出条件；未通过安全契约评审的方法保持 Unsupported/Forwarded（architecture-record 方法覆盖策略）。**

### 10.1 并行轨道与硬门禁（2026-10-29）

| 轨道 | 内容 | 门禁 |
| --- | --- | --- |
| S | Invocation/Basis/Outcome 归属 + 阶段值 + 门槛语义表 | Basis/SourceVerdict 在 `rpc`；不可变/并发/Clone 规则写死；`OutcomeResolver` 兼容回退；门槛表驱动测试通过 |
| T | Transport contract harness + 文档基线（阶段 0） | native 全套 L1 contract 通过 |
| R | ReferenceStage 迁移 R0–R5（见下） | 生产无第二套组合；一次 trace 一个 Invocation/Basis |
| P | 5 类纸上原型，不写编译器 | 现有矩阵不改即通过；能力/MethodRule/Projector/Custom 边界评审通过 |
| L | 拆包 + link-time + interlock（阶段 1–2） | 默认构建 deps 不含 ethclient；双构建 CI 通过 |
| G | geth adapter contract（阶段 3） | 同一套 contract 通过，偏差有记录 |

启动顺序：原则冻结后 S/R/P/T 同时启动；P 以 S 的语义结论为输入（不要求 S 全部实现完成）；L 依赖 T；G 随 L 之后；S 的语义规则 + P 的评审结果共同冻结 `MethodSpec` 形态，之后才启动阶段 4。

#### R 轨道：ReferenceStage 迁移

| 步骤 | 内容 | 门禁 |
| --- | --- | --- |
| R0 | HeadReader / HeadCodec / runtime PoolHeadReader 三层拆分 | `reference` 不 import `rpc/methods`；成员由 `Basis` 传入 |
| R1 | 新增 ReferenceStage，包装现有 Strict/Finalized 算法 | 旧路径行为不变 |
| R2 | aggregator 在同一 Invocation 内执行 reference 与业务阶段 | 一次 trace 一个 Invocation、一个 Basis、多阶段 |
| R3 | 删除 root `anchor.go` 生产 `aggregator.New`，保留公共 facade；`reference.Custom` 退役或迁入 runtime | 生产代码无第二组合点 |
| R4 | 删除 `AnchorHash`/`AnchorHeight` 网络 callback | 方法 spec 只声明 `ReferenceRequest` |
| R5 | 冻结 MethodSpec 的 reference 字段 | 原型与 trace 门禁通过 |

阶段门禁：生产代码中除 `internal/runtime` 外，不得出现 Reference / Method policy / aggregator 的第二套组合；一次移动标签读取的 trace 必须显示一个 Invocation ID、一个 Basis 和多个显式阶段。

### 10.2 可分配任务拆解

以下任务按“一个任务对应一个 Module 或一个明确迁移 seam”拆分。任务可以由不同
agent 并行，但每个任务只能修改自己列出的目录；跨目录改动必须在任务说明中列出
依赖和迁移理由。每个任务完成后应留下一个可独立审阅的提交，不把行为迁移、目录
移动和文档重写混在同一提交中。

| ID | 任务 | 主要目录 | 前置 | 交付物 | 验收标准 |
| --- | --- | --- | --- | --- | --- |
| S0 | 冻结 `Basis` / `Invocation` / `SourceVerdict` / `Outcome` 值模型 | `rpc/` | 无 | 值类型、Clone/只读规则、阶段状态测试 | `go test ./rpc/...`；并发复制、重复 `WithReference`、slice 不共享测试通过 |
| S1 | 门槛语义表与计算器决策 | `rpc/`、`internal/reference/` | S0 | basis/分母/floor/最小成员数语义表，表驱动测试；决定统一计算器或保留显式算法 | acceptance 与 finalized/reference 场景全部有表格行；旧公式行为有差异记录 |
| S2 | `OutcomeResolver` 兼容层 | `rpc/`、`aggregator/` | S0 | 可选 resolver 能力、旧 Policy fallback、Outcome 到 Decision/Faults/SourceEvent 投影 | 有接口和无接口两条路径输出等价；观测层不再调用 `Normalize` 判定 Failed |
| R0 | Reference 三层拆分 | `internal/reference/`、`internal/runtime/` | S0、S1 | `HeadReader`、`HeadCodec`、`HeadView`；runtime `PoolHeadReader` | `internal/reference` 不 import `pool`/`rpc/methods`；Basis 显式传入；reference 测试全绿 |
| R1 | ReferenceStage 适配器 | `internal/runtime/`、`aggregator/` | R0、S1 | runtime-owned `ReferenceStage` 与 `ReferenceService`，包装 Strict/Finalized 算法 | 旧 Strict/Finalized 输出、错误、ObservedAt 和 evidence 等价；无新增 aggregator 构造点 |
| R2 | 单 Invocation 阶段编排 | `aggregator/` | R1、S0 | `Prepare → ReferenceStage → PrepareWithReference → Collect → Admit → Resolve → Project` | moving-tag trace 只有一个 Invocation ID、一个 Basis；允许多轮网络但无嵌套 Aggregate |
| R3 | root anchor facade 迁移 | `anchor.go`、`client.go`、`internal/runtime/` | R2 | root anchor 只调用 `ReferenceService`；删除 root 生产 `aggregator.New` | `rg 'aggregator\\.New'` 的生产结果只剩 `internal/runtime`；公共 anchor 方法行为不变 |
| R4 | 删除网络型 anchor callback | `rpc/`、`rpc/methods/`、`internal/runtime/` | R2、R3 | `ReferenceRequirement` 声明式能力；移除 `AnchorHash`/`AnchorHeight` runtime 输入 | methods 不再持有会发起网络请求的 callback；moving-tag 矩阵不改即通过 |
| R5 | 冻结 MethodSpec reference 字段 | `rpc/methods/`、`internal/runtime/` | P0–P5、R4 | `ReferenceRequest`/`ReferenceKind` 进入 spec；字段说明和 inventory | 五类原型通过；每个 Registered 方法 reference 字段有显式值或明确 none |
| T0 | Transport L1 contract harness | `internal/transport/contract/`、`docs/design/` | 原则冻结 | harness、L1 规范、native adapter contract 测试 | native 全套 L1 通过；L2/L3 差异不改变错误类别 |
| L0 | Port 与 adapter 拆包 | `internal/transport/`、`pool/` | T0 | Port-only transport、`geth/` adapter、pool 去 alias/去 geth sentinel 依赖 | `pool`/`aggregator`/`rpc`/`reference` 不 import geth client 包；默认 root deps 不含 ethclient |
| L1 | `gethcompat` 构造 seam | `gethcompat/`、root facade、`internal/transport/geth` | L0、T0 | 显式 geth 构造入口；不增加 root 渠道选择参数 | native root 与 gethcompat 均可构造；公共 root 签名不随构建标签改变 |
| L2 | link-time/import interlock | 根测试、CI、`docs/design/` | L0、L1 | 依赖矩阵测试、默认/gethcompat 两套 CI | 默认 `go list -deps .` 不含 ethclient；兼容构建通过同一架构 interlock |
| G0 | geth adapter L1 对齐 | `internal/transport/geth/` | L1、L2 | Close、错误、body、context 行为适配 | native 与 gethcompat 运行同一 contract suite；偏差只在 L2/L3 报告 |
| P1 | `eth_getBalance` 纸上原型 | `rpc/methods/`、设计记录 | S0 | spec、compile、projector 和 MethodRule 投影草图 | 现有测试无需改动即可说明行为等价 |
| P2 | `eth_getLogs` 纸上原型 | `rpc/methods/`、设计记录 | S0、S1 | Validate-before-Reference、fields anchor、admit 草图 | moving-tag/admission/field projection 维度完整 |
| P3 | `eth_call` 纸上原型 | `rpc/methods/`、设计记录 | S0、S2 | Custom/CallRevert 双维 resolver 草图 | RPCErrorBodies、revert data、provenance、Outcome 语义无丢失 |
| P4 | `eth_getBlockByNumber` 纸上原型 | `rpc/methods/`、`internal/reference/` | R0、S0 | header-only/full ResultForm 与 HeadCodec 复用草图 | reference head 解码不依赖编译后 Policy；source spelling/canonical value 等价 |
| P5 | 单源读取纸上原型 | `rpc/methods/`、设计记录 | S0 | local/single-source resolver 草图 | 明确 Selected/Forwarded 语义，不引入已删除的 `Single` 字段 |
| M0 | MethodSpec 编译器 | `rpc/methods/`、`internal/runtime/` | S0–S2、R5、P1–P5 | spec registry、Compile、MethodRule/能力等价测试 | 只生成一套 policy；Custom inventory 完整；现有矩阵全绿 |
| M1 | 安全契约通过的方法迁移 | `rpc/methods/`、`internal/runtime/` | M0 | 按 catalog 评审结果逐个迁移 Registered 方法 | 每个方法有 Registered/Unsupported/Forwarded 证据；不得以数量作为退出条件 |
| V0 | Catalog/Architecture interlock | `rpc/methods/`、根测试、文档 | M1、L2 | Family/Coverage/Alignment 字段和依赖/组合门禁 | 方法 inventory、目录依赖、零测试 allowlist、组合根检查全部通过 |

任务约束：

- R0–R4 不得顺手迁移 MethodSpec 编译器；M0 不得重新设计 ReferenceStage。
- T0/L0/G0 不得改变 quorum、Decision 或方法 wire 语义。
- P1–P5 只产出纸上原型和等价性说明，不写生产编译器。
- M1 每次只迁移一个安全契约明确的方法簇；失败方法保持 Unsupported/Forwarded。
- 任何任务发现需要新增 context key、第二个 registry 或第二个 projector map，必须退回设计审阅。

命名硬规则：任务 ID 只存在于提案、issue、分支和提交说明中。生产文件、测试文件、
类型、函数、测试名和 fixture 名必须使用真实业务语义命名，例如
`reference_stage.go`、`basis_immutability_test.go`、`latest_reference_trace_test.go`；
不得创建 `r2.go`、`phase_r2_test.go`、`s0_fixture.go` 或其他以阶段/任务编号为业务
名称的文件和测试。一个提交可以对应一个任务，但提交内容和文件名必须表达实际
行为，不得把迁移阶段当作领域概念。

### 10.3 任务依赖与推荐分配

```text
P0 原则冻结
├── S0 → S1 → S2 ───────────────┐
├── T0 → L0 → L1 → L2 → G0      │
├── R0 → R1 → R2 → R3 → R4      ├── R5
└── P1/P2/P3/P4/P5（依赖 S0；P4 另依赖 R0）
                                  │
                                  └── M0 → M1 → V0
```

推荐分配为六条独立工作流：

1. **语义流 S**：S0–S2，维护 `rpc` 与 aggregator 的执行契约。
2. **Reference 流 R**：R0–R5，维护 `internal/reference`、runtime 和 root facade。
3. **Transport 流 T/L/G**：T0、L0–L2、G0，维护 Port、adapter 和 CI interlock。
4. **原型流 P**：P1–P5，只维护设计记录、等价性表和测试映射。
5. **编译流 M**：M0–M1，在 S/R/P 完成后维护 `rpc/methods` 与 runtime registry。
6. **治理流 V**：V0，维护 catalog、架构 interlock、文档同步和迁移报告。

每条工作流必须以自己的目录为主要修改范围；跨流协作通过小型 seam 提交完成，
不通过共享未完成的半成品类型来耦合。一个 agent 可以连续领取同一工作流中的多个
任务，但不能跳过前置任务直接修改后续 Module 的实现。

### 10.4 统一验收标准

所有阶段都必须满足以下共同标准，阶段特有标准见任务表：

```text
gofmt -l .                                  # 无输出
go test ./...                                # 默认构建全绿
go vet ./...                                 # 无新增诊断
go test -race ./pool/... ./rpc/... \
  ./aggregator/... ./internal/reference/... \
  ./internal/runtime/...                     # 执行核心并发面
go test . -run 'TestArchitecture|TestPackage' # 架构与测试 inventory 门禁
```

Transport 完成后还必须执行：

```text
go list -deps . | rg 'go-ethereum/ethclient'   # 默认 root 无输出
go test ./internal/transport/... ./pool/...    # native contract
go test ./gethcompat/... ./internal/transport/geth/...  # 兼容 adapter
```

Reference 完成后必须提供一个可读 trace fixture，至少覆盖：

- latest moving tag 的 strict reference + 业务读取；
- finalized 的候选选择 + confirmation + 业务读取；
- reference 失败时业务 fan-out 为零；
- 成员在两轮之间发生变化时，Basis、required 和 participants 不变；
- 同一调用中所有阶段共享一个 Invocation ID，且不存在嵌套 Aggregate。

MethodSpec 完成后必须提供一份生成或手写的 inventory，逐方法列出：

```text
resolver / reference / anchor / pending / key source / result /
admit / observation projector / typed projector / coverage state
```

验收失败时，任务只能回退到本任务的最后一个稳定 seam；不得通过放宽 interlock、
增加 allowlist 或把方法降级为透明转发来掩盖结构不一致。

---

## 11. 待定决策表

| 决策 | 候选 | 当前倾向 | 主要权衡 | 需要的证据 |
| --- | --- | --- | --- | --- |
| link-time 机制 | build tag / gethcompat 独立包 / 删除 geth | gethcompat 过渡，最终删除；build tag 仅回退 | API 随 tag 变化 vs 新增构造入口 | 迁移用户是否仍需要 geth |
| geth adapter 去留 | 长期 tag / 最终删除 | 最终删除 | 兼容性 vs 稳定性 | native 在真实 provider 的覆盖 |
| `WithHTTPAdapter` | 保留（默认构建报 not built）/ 删除 | 保留一个周期 | 迁移冲击 vs 清洁度 | 现有用户数量 |
| MethodSpec 形态 | 装饰器 / spec 编译器 / 能力记录 | spec 为靶，先做 A | 抽象成本 vs 概念数 | 5 类原型 + 矩阵测试 + 能力/MethodRule 等价性 |
| Read 覆盖目标 | 固定方法数量 / 安全契约评审集合 | 安全契约集合，数量只作结果 | 表面覆盖率 vs 安全深度 | catalog 的 Registered/Unsupported/Forwarded 证据 |
| Invocation | context envelope / 显式参数 / 阶段值 | 阶段值，context 仅过渡 | API 破坏 vs 可读性 | 是否允许破坏公共 Policy |
| Outcome | 独立 verdict / 现有二次 Normalize | 独立 verdict（可选 `OutcomeResolver`） | 契约变大 vs 单一真相 | call-revert/median 的语义 |
| Basis/SourceVerdict 归属 | `rpc` / aggregator | `rpc`（只含 SourceRef + Corroboration） | 依赖方向 vs 语义完整 | import 矩阵与测试 |
| Reference 边界与组合根 | Aggregate 内部阶段 / 两次 Aggregate 共享 Basis | Reference 作为 Aggregate 内部阶段；root anchor 移入 runtime；两次 Aggregate 仅迁移 | 改动范围 vs 单一真相 | R0–R5 门禁 + trace |
| Transport Port 所有权 | pool 语义拥有 / transport 物理包 | pool 语义拥有，物理包 `internal/transport` | 拓扑一致性 vs 循环依赖 | topology 修订 + import 矩阵 |
| 订阅范围 | 3 个 ethclient / 全部 eth_subscribe | 先 3 个 | 工作量 vs 完整性 | 真实 provider 支持矩阵 |
| Stream 语义 | corroborated 事件 / per-source + 证据 | 待定 | 产品哲学 vs 灵活性 | 调用方需求 |
| proof 状态 | 正交 Verification / 顶层 Verified | 正交 | 证据诚实 vs API 简洁 | 大规模 RPC 形态数据 |
| contract 文档 | 新建 transport-contract / 提升 differential 章节 | 新建 | 文档重复 vs 基线清晰 | 维护者共识 |
| surface manifest | CI gate / bump-time 工具 | bump-time | 强制对齐 vs 脆弱性 | ethclient 版本节奏 |

**已定（2026-10-29）**：Reference 作为 Read invocation 的内部阶段；root `anchor.go` 装配移入 runtime；两次 Aggregate 共享 Basis 仅作迁移。

**决策时机（2026-10-29）**：原则项（link-time 方向、geth 去留方向、Read 覆盖目标、`WithHTTPAdapter`、contract 文档、surface manifest）可在 P0 冻结；`Invocation` 的不可变/阶段规则与 `Outcome` 的 verdict 形态在轨道 S 先行冻结；`MethodSpec` 字段集等 5 类原型后冻结；Stream 语义与 proof 状态保持待定。GPT 复核聚焦：link-time 的公共 API 影响、`MethodSpec` 字段集是否封闭、`Invocation`/`Outcome` 是否允许破坏公共签名、Read 覆盖率是否按安全契约集合、transport-first 与原型-first 的并行边界。

---

## 12. 验证与风险

### 12.1 现有安全网

- `rpc/methods/admission_suite_test.go`：canonical-value 方法的 encode/normalize 域与确定性。
- `rpc/methods/moving_tag_matrix_test.go`：moving-tag × anchor × pending 矩阵。
- `rpc/methods/representative_test.go`：representative、Support/Reject 组合。
- `rpc/methods/method_rules_test.go`：`MethodRule` 逐层遍历 + 生成式索引。
- `rpc/corroboration_composition_test.go`：外层装饰不得凭空发明 `CorroboratingPolicy`。
- `rpc/canonical_key_source_test.go`：`KeySource` 三态。
- `internal/reference/finalized_phases_test.go`：候选选择、basis 冻结、reorg。
- `pool/collect_test.go` / `voting_test.go`：fan-out verdict 优先级。
- 架构 / 覆盖 interlock：依赖方向、零测试 allowlist。

### 12.2 新增安全网

- 默认构建 deps 不含 ethclient。
- import 矩阵 interlock。
- `internal/transport/contract` 双 adapter 验收；只验 L1，L2/L3 记录差异。
- Invocation 阶段/不可变测试：重复 `WithReference`、并发复制、`Prepare`/`Anchor` 无 reference。
- Basis/SourceVerdict 归属与确定性：`Source.ID` 排序、`Clone` 不共享 slice、verdict 带 source identity 与错误。
- `OutcomeResolver` 兼容投影：有/无接口两条路径的 Decision/Faults/SourceEvent 一致。
- Transport Port 所有权 import 矩阵：pool 语义 owner、`internal/transport` 物理包、`reference` 无 `rpc/methods`。
- 单一组合门禁：生产代码除 `internal/runtime` 外不得出现 `aggregator.New`/ReferenceStage 第二套构造；root `anchor.go` 只转发。
- Trace 门禁：一次移动标签读取只出现一个 Invocation ID、一个 Basis，且阶段顺序显式可读。
- ReferenceStage 适配器兼容：旧 Strict/Finalized 算法经 stage 接入后行为不变；`AnchorHash`/`AnchorHeight` callback 最终移除。
- 门槛语义表驱动测试：acceptance 与 finalized/reference 的 basis、分母、最小成员数。
- Catalog 覆盖分类：Registered/Unsupported/Forwarded 的显式证据。
- MethodSpec 维度 inventory：每个注册方法必须声明 resolver/anchor/pending/keysource/result/admit/projector。
- 编译器等价性：Compile 输出与现链在能力断言、`MethodRuleSource` 投影上等价；不等价时先迁移测试。
- Transport sentinel/lifecycle contract：`ErrBodyTooLarge`、`ErrConnectionClosed`、Close 后调用、body 超限。
- 订阅：per-kind 决策矩阵、错误分类、reorg/backfill 场景。
- proof：真实 provider capture + 本地验证回归。

### 12.3 风险

- `MethodSpec` 变成 god struct；
- build tag 造成 API/CI 双路径；
- stream 的错误/重连/回填复杂度被低估；
- proof 的真实 provider 形态数据不足；
- 迁移期间 geth 与 native 行为分叉。

---

## 13. 附录：当前分支与验证

- `master` @ `a6aa940`：`fix/audit-findings` 已于 2026-10-28 fast-forward 合并，随后吸收远端 CI 依赖提交（checkout/setup-go）；临时 `AUDIT-FIXLIST.md` 已删除。
- 2026-10-29 追加提交 `a6aa940`：`livecanary` workflow 契约测试不再写死 `upload-artifact@v4`，改为版本无关断言，已在 Dependabot v7 分支上验证通过。
- 已验证：`gofmt -l .` 无输出；`go build ./...`、`go vet ./...`、`go test ./...`、`go test -race ./pool/ ./rpc/... ./internal/reference/` 全部通过（2026-10-29）。
- 本提案仍未跟踪（`git status` 显示为 untracked）；建议单独提交或在独立分支维护，避免与已接受契约混淆。
- 待同步的已接受文档：`docs/design/codebase-topology.md` 的 Transport Port 所有权、Reference Head-Reading/ReferenceStage 边界、唯一 Composition Root 三处需在提案冻结后修订；冻结前不改已接受契约。
- 文档拆分计划（冻结后）：`transport-contract-adr.md`、`invocation-outcome-adr.md`、`method-spec-spike.md`、`execution-families.md` 与一份短路线图；冻结前保持单文件，避免局部决策改动导致全文复审。
- 依赖告警：`GO-2026-5158`（`go.opentelemetry.io/otel@v1.41.0`，geth 传递依赖，不可达）按 2026-10-28 决定暂不修复；等设计基本完成后随 geth 版本统一升级。
- 本提案中所有“当前倾向”均为讨论记录，不是决定；最终选择等待 GPT 复核与真实数据。
