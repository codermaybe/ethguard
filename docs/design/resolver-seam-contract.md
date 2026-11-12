# Resolver、方法计划与 reference seam

本文件拥有 `ReadPlan` 与 resolver 深模块之间的契约，以及方法声明如何消费 reference。
它合并原 resolver 盘点、reference 字段冻结、纸上原型与 anchor transport seam 中仍
适用的规则，不保留已完成迁移的执行顺序或 legacy 装配图。历史裁决见
[decisions.md](../decisions.md)（尤其 0063）；当前模块地图见
[codebase-topology.md](codebase-topology.md)，逐方法声明的只读投影见
[method-rules.md](method-rules.md)。

## 1. 单一事实来源与所有权

默认 runtime registry 从 `Specs()` 编译 ReadPlan；计划持有 resolver 值。
“走计划”不意味着 resolver 已被扁平化：Strict、Median、Local、CallRevert 与
block-specific resolver 仍是深模块。保留 `rpc.Policy`，不另建 ResolverPlan，
不为了“零 decorator”拆分 Normalize、Outcome、CanonicalValue、MethodRule 或能力。

| 所有者 | 负责 | 不负责 |
| --- | --- | --- |
| `rpc/methods` | 请求形态、Validate/Admit、对象规则绑定、resolver 装配、Catalog/Specs 完整性 | pool 生命周期、transport、typed projection、reference 选择算法 |
| ReadPlan / aggregator | 校验与 pending 路由、reference requirement、改写、collect、同源 admission caller、调用级 evidence/执行顺序 | 解释 Ethereum 对象字段、第二套 resolver |
| resolver 值 | Normalize、Resolve、可选 corroborated/outcome 入口、canonical encoding、resolver 域错误、能力与 MethodRule resolver 列 | reference 建立、请求改写、网络回调、嵌套 Aggregate |
| `internal/runtime` | 唯一生产装配；projector ID 与实现绑定、ReferenceStage 和 head adapter | 第二份方法 inventory、方法字段表 |
| `internal/reference` | HeadReader/HeadCodec seam 与 reference 算法 | transport 类型、方法 policy 组装 |

方法声明只带 `ObservationProjectorID`，不带 factory、不导入 projection/typed；
空 ID 保持未投影，不回落为方法名。runtime 按业务 ID 绑定；resolver 的可选
ObservationProjector 不是第二份 registry。计划期 pin 由改写结果记录，不从 legacy
BlockRefProvider 反向猜测。

`rpc` 不导入 `rpc/methods`；aggregator 只消费通用 rpc/collection 接口，不解释
方法字段。对象 requiredness、commitment、encoder 归 `rpc/internal/objects/*`，
wire normalization 归 `rpc/internal/wire`。`rpc/methods` 维持一个生产 Module：
文件数量、namespace 或业务类别不是拆包理由，不建立浅层转发 package/第二份 catalog。
新增方法的请求绑定、wire/object 规则、admission 与离线验证在同一变更意图内审查。

## 2. 固定阶段与 reference 声明

```text
Validate / 本地 pending 拒绝
  → ReferenceRequirement
  → ReferenceStage（需要时，在同一 Invocation/Basis 内建立）
  → PrepareWithReference
  → Collect → Admit（coordinator 注入同源 caller）
  → Resolve Outcome → Project / Observe → typed projection
```

校验先于 reference；reference 失败不发业务请求；计划不携带网络 callback，也不
通过读 context 建立 reference。AdmissionCaller 是 invocation/source 级运行时依赖，
不能放进静态 spec 或编译依赖。一次逻辑调用一个 Invocation、一个冻结 Basis，
reference 轮次与业务轮次共享该身份，不嵌套 Aggregate。

### 2.1 `ReferenceSpec` 的单一声明

以 `rpc/read_plan.go` 的实际值类型为准，不使用旧原型的 `*ReferenceSpec`、
独立 EIP1898 shape 或编译发射 decorator 链的草图：

| 字段 | 含义 |
| --- | --- |
| `Kind` | 空值（none）、hash-at、height-at、height-fields-at |
| `Positions` | hash/height 字符串参数位置 |
| `ParamPos`、`Fields` | 对象参数内的高度字段组 |
| `EIP1898` | 同一 anchored 位置接受对象形态；不是第二份 reference 声明 |

- 零值表示无需 reference（stateless、显式 pin 或 local）；不靠隐式默认猜测。
- requirement 与 PrepareWithReference 来自同一声明：一个位置要么声明并改写，
  要么都不做。具体 latest/safe/finalized level 来自请求，不是 spec 常量。
- EIP-1898 的 `{blockNumber: movingTag}` 改写为 `{blockHash}`；对象校验先于
  字符串位置处理。显式 hash、数字、earliest、pending 按各自方法契约处理，不
  因 moving-tag 机制改写；contradictory/malformed 对象本地拒绝。
- 高度字段缺省值按 moving tag 处理；logs 的 blockHash pin 与范围字段互斥。
  mixed moving tags 是请求错误，不能选一个 tag 将就成功。
- reference evidence 必须服务请求的 tag 且满足既有佐证要求；safe/finalized
  不复用 latest evidence。不匹配或缺少 ReferenceStage 时在业务 fan-out 前失败。
- 新维度或 reference 形态先评审受控逃生口，不为单一方法随意扩列。逃生口需要
  理由、所有者、回归测试与退出条件；不开放任意函数字段作为第二套方法模型。

### 2.2 保留的结构性方法实例

| 实例 | 不可丢失的规则 |
| --- | --- |
| balance/nonce/code/storage/call 等 state read | 声明相应 hash 参数位置，并在同一位置支持 EIP-1898；pending-local 与 moving reference 正交 |
| `eth_getLogs` | pending/malformed bound 先于 reference 拒绝；字段级高度改写，collect 后才做响应 admission |
| `eth_feeHistory` | 三参数请求校验先行；只有 newestBlock（位置 1）改写为高度，不是 hash；响应区间 admission 独立 |
| `eth_getBlockByNumber` | registered business plan 不声明 moving reference；facade 建立 reference/head 读取是独立角色，不能因同一 wire 方法而递归建立 |
| block head | business 与 reference 共用方法 identity（number/hash）语义；HeadCodec 直接绑定该语义，不临时构造旧 policy 链 |
| local 与 Forwarded | local resolver、pending-local 和显式 Forwarded 是不同维度；Forwarded 是 invocation/snapshot/执行通道属性，不新增 SingleSource spec 字段 |

## 3. Resolver 能力与编译互锁

能力以 resolver 值自身的 Go 方法集为准，不遍历 decorator，不新增 marker 标签
代替实际行为。`corroboratingResolvers(kind)` 是协议级类别声明。

| 能力 | Strict | blockPolicy | CallRevert | Median | Local |
| --- | --- | --- | --- | --- | --- |
| Resolve / corroborated 入口 | 两者 | 两者，保留 block result 转换 | 两者 | 仅普通，Selected 封顶 | 仅普通，Selected 封顶 |
| Outcome / corroborated Outcome | 两者 | 两者，对 Decision 做同名转换 | 两者 | 无 | 无 |
| RequiresAgreement | 是 | 是，来自 strict | 是 | 否 | 否 |
| IncludeRPCError | 显式配置时 | 未开启 | 构造即开启 | 无 | 无 |
| CanonicalValue | 按 KeySource | block 编码/恢复 | success 编码；revert 按专属规则 | 数量编码，可返回 ErrNotCanonicalScalar | local 编码 |
| MethodRule resolver 列 | strict、compare、key source、result | 内层 strict 的规则 | call-revert/method-specific | median/quantity | local/compare kind |

装配期 `methods.validatePlanBindings` 将 kind 与 `CorroboratingPolicy`、
`CorroboratingOutcomeResolver`、AgreementPolicy 双向对账，并核对声明的 key source；
不一致则编译失败，不注册。执行期 aggregator 只读一次 plan 的类别与 resolver
自身接口。median/local 即使有 corroboration snapshot 也只走普通入口，不能获得
agreement 或 Corroborated。测试锚点为 methods/aggregator 各自的
`resolver_capability_interlock_test.go`。

KeySource 是显式声明，不从 Encode 是否存在推导：canonical-bytes 以规范编码作键；
normalizer 配 encoder 允许窄键与较宽 canonical result；local/median 不聚类，
不因其编码器存在而声明比较键。域错误与 Fault attribution 仍归 resolver，
包括 ErrConflict、ErrNoValidResponse、ErrNotCanonicalScalar 与 encode/normalize cause。

## 4. CallRevert 与 block 的深模块语义

### 4.1 CallRevert 的 per-source Outcome

CallRevert 使用通用 Outcome，不另建 usability seam：答案通过 execution
admission（含 classify/normalize/encode）才进入裁决。

- 胜出 execution 分组为 VerdictSupported，落败分组为 VerdictDissented；
  transport、值域外错误、normalize/encode 失败为 VerdictUnusable，带逐源 cause。
- data 的 missing/null 弃权与 `0x`/hex/raw JSON 断言不塞入 execution verdict；
  仍由 Dimensions[data] 的 Support/Reject/Abstain/Returned、Revert、DeletedFields
  和 Provenance 陈述。data 弃权不是 Reject。
- ResolveOutcome 与 Resolve、corroborated 两入口共享同一裁决核心；Decision、
  Faults、Dimensions、Provenance 保持同一语义。verdict 覆盖每份 observation，
  按 Source.ID 排序用于诊断确定性。
- execution 冲突时保留与 Resolve 相同的错误/降级 Decision，verdict 仍按分组
  陈述。aggregator 不再用 Normalize 二次猜测 eth_call usability；pending-local
  的 ResolveLocalObservation 不受此规则替代。

`call_revert_outcome_test.go` 保留入口等价断言。双维裁决、reductive 投影和 revert
context 留在该模块，不为其专属形状扩展所有方法字段。

### 4.2 Block result 与 admission

blockPolicy 只拥有 block-specific result 语义：窄 number/hash 键下，header-only
恢复代表源 spelling，full-block 保持 canonical Value；不是额外的执行阶段。
按 hash/number 的身份由编译事实 `ByHash` 绑定，不从请求方法名反推。
full-block admission 使用共享 `admitBlockResponse`；caller 仍由 coordinator 注入。

不能从比较键反序列化构造 Value，也不从不同来源拼接。header-only/full/null、
canonical Value、admission、hash/number 身份和 pin 都是 block 编码/准入回归面。
feeHistory 的计划 resolver 是完整的 plain Strict Outcome 能力，历史 wrapper
自行声明哪些方法不构成现行 seam 规则；旧 plan/chain 等价日志保留在 Git。

## 5. Reference 的 transport 中立性

单次 reference RPC 经 runtime 的 head adapter 与 pool 的 endpoint 调用进入
Transport Port。native HTTP/raw WSS 和显式 gethcompat 是 adapter；resolver 不读
URL scheme、不持有 HTTP response/WebSocket frame，也不负责连接复用或拨号。

- reference 请求保留 latest/safe/finalized 原 tag；状态读取用 hash，logs/feeHistory
  用 height，两者来自同一次 reference establishment，不另行裁决。
- transport/dial、JSON-RPC error、无法归一的 head、quorum/conflict、取消与超时
  都阻止业务 fan-out；Decision 与 reference evidence 不伪装成功，保留 Faults
  和该次 ObservedAt。空 head 仍是 normalize fault/ErrNoValidResponse
  （missing number or hash），不是换成 ErrMalformedResponse。
- 每次 invocation 重新建立 reference，无跨调用缓存或 TTL；WSS 连接复用不等于
  复用上次 reference。adapter 不组装 evidence、不做 quorum、reference 选择或缓存。
- quorum、floor 与冲突分类只看既有 snapshot，不因 transport 而选择另一套规则。

原始响应捕获与 reference 重放的对应关系见 capture/golden 契约；本节不增加第二套
resolver、evidence 或 quorum。presence 的 adapter 限制见 WSS wire 契约，不由
reference seam 改成业务字段规则。

## 6. 重新设计的触发条件

只有新的实际证据才重新评审：resolver wrapper 再次携带 Prepare/reference/网络
控制或请求副作用；结果矩阵出现共享业务 helper 无法消除的分叉；能力无法由值的方法
集表达而必须遍历 chain；可选接口增长使单点 capability read 失去清晰边界。
`MethodRuleSource` 最终归属、agreement/RPC-error 接口是否合并不在此预设计。

新的 methods Module 还需证明稳定而独立的 assembly Interface（如多个实现需要
替换、对象有外部真实消费者或知识所有权/变更节奏已分离），并能减少调用者知识；
否则保持现有对象/wire 深模块，不按文件数拆包。任何后续设计不得把历史原型当作
已授权的运行时变更。

## 7. Canonical 单源处理路线（2026-11-13 试点落地）

本节记录 2026-11-13 接入的处理 seam 合同；类型定义在 `rpc/canonical.go`，路由冻结在
aggregator，方法装配约束在 `methods` 编译验证。该 seam 是受控的处理入口，不是注册表：
无 processor registry、无方法名 switch、无 vendor 配置、无第二份 key 模式。

**类型与不变量。** `CanonicalProcessor`（冻结语义请求 + 来源原文 + 同源 caller →
`CanonicalResult`）是方法唯一的受控处理入口。`CanonicalResult` 携带不可变规范字节与
准入 evidence，不携带来源身份、请求或 invocation token；空值即无成功结果，合法根 null
是非空 `"null"`；getter 返回拷贝。`CanonicalObservation` 绑定一个来源槽位：失败来源保留
槽位且无结果，成功来源必须有结果；配对错误、构造器非法输出、非零 result+error、
成功缺结果都是内部合同 sentinel `ErrInvalidCanonicalResult`——coordinator 必须先于
provider 归因识别它：整轮失败、不写入来源 SourceFaults、不对 provider 计健康、其他
已知 transport 故障归因保持、最终健康只执行一次。结果只在当前 invocation 内使用，
禁止全局/跨 invocation/按交易 hash 缓存，禁止把 evidence 当候选。

**路线冻结。** invocation 在 prepare 末尾从 plan 声明、prepared 请求与 captured
corroboration 模式一次性冻结处理路线（`frozenProcessorRoute`）：plan 有 processor 且
当前是普通 strict 轮 → canonical 路线；pending-local 形状与 Forwarded 模式保持完整
原路线。这是按执行类别的判定，不是方法名注册维度；路线冻结后 admission、resolve、
fault/event、health 四个出口共同消费，不再分别重判。

**消费与提交。** `CanonicalOutcomeResolver` 的两个入口与 raw 入口共用同一聚类、排序、
裁决、verdict 与提交内核（`rankClusters`、`legacyCoreClusters`、`commitProcessed`、
`verdictsFromSources`）；canonical 路线直接消费规范字节，绝不调用 raw
normalizer/encoder。raw 路线的 finish 保留其 encoder 声明门；canonical 路线无条件
走提交内核，不依赖任何 encoder 回调存在。admission 类失败（processor 错误）保持
`admission: %w` 的准入阶段归因与并发/槽位/同源 caller/dependency capture 语义；
每可用来源一次处理，失败后不重试、不回退 raw hooks，包括
finalizeHealth 的 validity fallback（raw 路线专属）与整体早退。

**装配约束。** 编译只接受 Strict + canonical-bytes + 消费能力配对，并要求 raw
兼容 adapters（Normalize/Encode）保留可用；试点方法的 Admit 不因 processor 存在而
置空。未来无 Admit 的简单方法不凭空补空 hook——不接入 seam 或另交装配设计。
KeySource 与 processor 的配对是元数据收敛：声明切换须有分簇等价反例
（试点见 `TestTransactionKeySourceSwitchKeepsClusters`）。

**试点边界。** 当前仅 `eth_getTransactionByHash` 与
`eth_getTransactionByBlockHashAndIndex` 接入；26 读 RPC 的其余族处置及未接入理由见
[method-transport-coverage](method-transport-coverage.md) 的处理处置表。CallRevert、
Median、Local、block 窄键与 pending-local/Forwarded 不在此 seam 的支持类别内。
