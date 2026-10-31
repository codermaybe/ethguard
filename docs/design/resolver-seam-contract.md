# Resolver Seam Contract（RS1：resolver 最小 seam 冻结）

> 状态：已交付（2026-10-31，RS1 交付物）。本文冻结计划管线依赖的
> resolver 最小契约：哪些行为属于 resolver 深模块，哪些必须由
> ReadPlan/aggregator 拥有。事实基线是
> [`resolver-behavior-inventory.md`](resolver-behavior-inventory.md)（RS0）；
> 本文不重新罗列逐方法清单，只裁决 seam 的归属、能力契约和剩余边界。
> 修订记录见 §7。

## 1. seam 归属总裁决

resolver seam（`rpc.Policy` 深模块，由 `ReadPlan.Resolver` 持有）只负责：

| seam 条目 | 内容 | 锚点 |
| --- | --- | --- |
| Normalize | raw response → comparison key | `Policy.Normalize` |
| Resolve | 普通 observation → Decision | `Policy.Resolve` |
| ResolveCorroborated | 在 invocation snapshot 下裁决 | `CorroboratingPolicy.ResolveCorroborated` |
| Outcome | 可选 per-source verdict（plain 与 corroborated 两个入口） | `OutcomeResolver` / `CorroboratingOutcomeResolver` |
| CanonicalValue | 可选 canonical value | `CanonicalOf` 读 `CanonicalEncoder`（WithCanonicalValue / WithCanonicalObject / WithMedianCanonical / WithLocalCanonical，或 call-revert 的 encode） |
| 错误 | resolver 域错误与 fault attribution | `rpc.ErrConflict` / `rpc.ErrNoValidResponse` / `ErrNotCanonicalScalar`；`skippedFaults` 渲染 encode/normalize cause |
| 能力开关 | `RPCErrorPolicy`（error body 值域）、`AgreementPolicy`（typed 边界的 agreement） | |
| MethodRule resolver 列 | Resolver / CompareKind / KeySource / Result 四列 | `MethodRuleSource.MethodRule()` |

resolver seam **不**负责（全部归 ReadPlan/aggregator/runtime 拥有）：

- **Prepare**：计划路径的请求改写是 `ReadPlan.PrepareWithReference`；
  resolver 的 `Prepare` 只剩 legacy 链上的恒等实现；
- **moving-tag / reference requirement**：`ReadPlan.ReferenceRequirement` +
  ReferenceStage + 既有 evidence 值（resolver 不持有任何网络回调）；
- **ReferenceStage**：runtime 的 reference stage 是唯一 establishment 路径；
- **pending 路由**：计划路径 `planPendingLocalRoute` + 共享的
  `rpc.ResolveLocalObservation`（它是计划管线的 pending 分支实现，
  不是诊断性 Normalize —— local-observation 的"第一个可归一化答案"语义
  归 pending 声明拥有）；
- **AdmissionCaller**：coordinator 注入的同源 caller + 计划 `Admit` hook；
- **BlockRef request rewrite**：改写期 pin 由计划记录（`state.planPin`），
  legacy 链的 `BlockRefProvider` 不回流 resolver；
- **projector registry**：runtime 按 `ReadPlan.ProjectorID` 业务 ID 绑定，
  resolver 的 `ObservationProjector` 只是计划路径的第二优先级回退；
- **重新发起 Aggregate**：invocation 内不再嵌套聚合；
- 任何网络 callback（RS0 §2 的 quarantine 边界维持原状）。

不变量：计划路径读能力只从 `plan.Corroborating()`（声明的类别）与
`plan.Resolver` 值自身的 Go 接口读取一次，从不遍历 decorator chain。

## 2. 能力矩阵（按 resolver 本体，RS1 后的冻结事实）

| seam 能力 | `Strict` | `blockPolicy` | CallRevert | `Median` | `Local` |
| --- | --- | --- | --- | --- | --- |
| Resolve / ResolveCorroborated | ✓ | ✓（内层 strict 能力 + wrapper 自身的 header-only 转换） | ✓ | ✓（Selected 封顶） | ✓（Selected 封顶） |
| OutcomeResolver / CorroboratingOutcomeResolver | ✓/✓ | ✓/✓（继承内层 + wrapper 对 Decision 的同名转换） | ✓/✓（RS1 新增，见 §3） | ✗ | ✗ |
| RequiresAgreement | ✓ | ✓（转发内层） | ✓ | ✗ | ✗ |
| IncludeRPCError | 仅 Yes 构造 | — | ✓（构造即开启） | — | — |
| CanonicalValue | ✓（随 KeySource） | ✓ | ✓（仅 success 分支使用） | ✓ 或 ErrNotCanonicalScalar | ✓ |
| MethodRule resolver 列 | strict + KeySource/Result | strict（来自内层 strictFunc） | call-revert/method-specific | median/quantity | local/compare kind |
| corroboration（kind 表） | ✓ | ✓ | ✓ | ✗ | ✗ |

类别表（`corroboratingResolvers`）是协议级声明：只有 strict、block、
call-revert 在 invocation 的冻结 corroboration snapshot 下解析；median 与
local 是采样/本地类，**永不获得 corroboration**——即便 snapshot 存在，
计划路径也只走它们的普通入口，状态封顶 Selected（见
`aggregator/resolver_capability_interlock_test.go` 的 invocation seam 测试）。

## 3. CallRevert 的 per-source Outcome（统一 Outcome，不另设 usability seam）

裁决：**复用通用 Outcome**，不新建 resolver-owned usability seam。理由：
CallRevert 的双维语义中，"答案可不可用"这个概念只在 execution 维度存在
——一个答案进入裁决当且仅当它通过了 admission（encode/normalize/
classify）；data 维度没有独立的"不可用"状态，任何通过 execution 的答案
都完整参与了 data 的弃权/断言投影。因此 per-source verdict 在 execution
维度就是完整且无损的：

- execution 胜出的分组 → `VerdictSupported`；
- 落败的 execution 分组 → `VerdictDissented`；
- admission 失败（transport fault、error body 值域外、normalize/encode
  fault）→ `VerdictUnusable` 并携带 per-source cause；
- missing / JSON null（`reverted|missing` / `reverted|null`）与具体 data
  （`0x` / hex / 非hex JSON）的**弃权与断言状态不进 verdict**，仍由
  `Decision.Dimensions[data]` 的 Support / Reject / Abstain / Returned、
  `Decision.Revert`、`Decision.DeletedFields` 与 Provenance 持有——这些
  字段一个未动。

语义保证（测试 `rpc/methods/call_revert_outcome_test.go`）：

- `ResolveOutcome` / `ResolveOutcomeCorroborated` 与 `Resolve` /
  `ResolveCorroborated` 是同一裁决（同一 `adjudicated` 核心）；
- Decision、Dimensions、Provenance、Faults 的既有语义逐字节保持；
- verdict 覆盖每一条 observation，按 Source.ID 排序（诊断确定性）；
- 历史 conflict cases 的误解：execution 冲突时 outcome 携带与 Resolve
  相同的错误与降级 Decision，verdicts 仍按胜/败分组陈述。

aggregator 计划路径据此消费该 seam：eth_call 的计划 resolver 现在实现
`OutcomeResolver` / `CorroboratingOutcomeResolver`，verdicts 非空，
`attemptClassifier` 不再回落到通用
`Normalize`（`usableAnswer`）对 eth_call 二次猜测 source usability。
pending 路由上的 `ResolveLocalObservation` 不受影响（§1 的 pending 语义）。

## 4. feeHistory 的计划/legacy 边界

裁决（以计划路径为事实来源）：

1. **计划路径**：eth_feeHistory 的 plan resolver 是 plain
   `Strict(normalizeFeeHistory; feehistory.Encode)`，由其自身方法完整
   拥有 `OutcomeResolver` 与 `CorroboratingOutcomeResolver`
   （`TestFeeHistoryPlanPathOwnsTheFullOutcome`）。
2. **legacy wrapper**：`FeeHistoryPolicy(...)` 组装的链是兼容与
   plan-equivalence 对照实现，默认 runtime 不装配它。wrapper 层自身
   **声明**的 outcome 方法只有 corroborated 变体
   （`FeeHistoryPolicy.ResolveOutcomeCorroborated`）；组装后的 chain 通过
   `rpc.Decorator` 的条件转发同样回答 plain `ResolveOutcome`——因此两个
   assembly 的公开行为等价（plain resolve、plain outcome、corroborated
   outcome 三路对比，`TestFeeHistoryPlanAndWrapperBehaveAlike`）。
3. **RS0 盘点的修正**：inventory §2/§3 记录的"wrapper 无
   `ResolveOutcome`（仅 corroborated 变体）"描述的是 wrapper 层自行
   声明的方法集；以 Go 接口为准的 chain 表面上该差异不可观察。seam 契约
   以计划路径为规范来源，这一差异不升格为 seam 规则，也不为legacy
   wrapper 的同形而扩大 resolver seam。
4. **兼容测试的纪律**：等价测试只验证公开行为等价，不把 legacy 的能力
   形态变成新规范；删除条件随 RS4 的 quarantine 一并裁决。

## 5. corroboration capability 的归属（switch，不是新接口）

裁决：保留 `corroboratingResolvers(kind)` 的类别表作为协议级声明，不
再新增运行时 marker 接口。理由：

- 三个 corroborating resolver 本体（strict、block、call-revert）已经以
  自身方法实现 `CorroboratingPolicy` 与（RS1 后）
  `CorroboratingOutcomeResolver`；median/local 不实现任何 corroboration
  入口。能力由 resolver 本体的方法集决定，不由 decorator 层数决定
  （`Decorator` 对 capability 的条件转发已有此约束，blockPolicy 的做法
  是对 Decision 应用同名转换而非无中生有）。
- 计划管线已经把"能力检查"集中到两处，不漂移、不遍历：
  1. **装配期**：`methods.validatePlanBindings` 在编译计划时读
     resolver 值的两个 corroboration 接口，与声明的类别双向对账；
     不一致 → 计划编译失败，方法不会注册（编译/装配失败）；
  2. **执行期**：aggregator 只读一次 `plan.Corroborating()` + resolver
     值自身的接口；corroborated 入口是 checked capability read。
- 为新增 marker 接口而被否决的理由：它可以被一个不持有对应行为的
  wrapper 断言满足，等于把能力从"方法集"移回"标签"，重复已否决的
  decorator 遍历。

能力 interlock（测试锚点）：

- `rpc/methods/resolver_capability_interlock_test.go`：inventory 全量
  走查 + 双向漂移的编译拒绝样例；
- `aggregator/resolver_capability_interlock_test.go`：snapshot 下
  call-revert 计划路径取得 verdicts；median/local 在 snapshot 存在时
  仍 Selected、verdicts 为 nil；
- `internal/runtime/legacy_quarantine_interlock_test.go`：legacy
  constructor 只能出现在声明的 establishment seam 文件
  （`strict_head.go` / `head_codec.go`，各带理由），跨包的构造调用
  会被按文件逐 AST 拒绝；计划 registry 只在 `method_registry.go`
  编译一次，不存在第二个 registry。

## 6. RS2 当前裁决与后续边界

- `blockPolicy` 在当前架构阶段保留为 resolver 深模块，不迁移成第二套
  resolver 或额外的 aggregator 阶段。它只拥有 block-specific result 语义：
  窄的 `number|hash` 比较键下，协商结果恢复代表源的 header-only spelling；
  full-block admission 仍通过共享的 `admitBlockResponse` 执行。
- 计划路径的 `Admit` 与 legacy policy 的 `Admit` 现在绑定同一个业务语义 helper，
  并由编译期 `byHash` 事实显式选择 hash/number 身份；不会再从请求方法名推断
  admission 形态。admission caller 仍是执行期注入，resolver 不持有网络 callback。
- 证据由 `rpc/methods/block_encoding_test.go`、`block_admission_test.go`、
  `internal/runtime/plan_block_equivalence_test.go` 和全量 plan/legacy
  equivalence 覆盖：header-only、full、null、canonical Value、admission、
  hash/number identity 与 BlockRef 均保持既有语义。
- 本裁决是当前阶段的可修订架构记录，不宣称永久不变。只有出现新的证据——例如
  block wrapper 再次携带 Prepare/reference/network 控制，或 plan/legacy 在上述
  结果矩阵出现无法由共享 helper 消除的分叉——才进入 RS5 修订，评估显式
  result hook 或继续拆分；在此之前不引入第二个 block resolver。
- `logsPolicy` / `feeHistoryPolicy` / 兼容 decorator 家族的删除条件归
  RS4（quarantine 已在编译层立柱）；
- out 展平与否的最终证据决策归 RS5；
- WSS 证据门（W0 仅做"missing/null 不可知"调用级诊断）与本 seam 无关，
  不在 RS1 范围内。

## 7. 修订记录

**RS1 修订（2026-10-31）**：

- **触发证据**：RS0 inventory 登记的三项不对称（CallRevert 无
  `ResolveOutcome`、feeHistory wrapper 与计划路径的 outcome 能力差异、
  `corroboratingResolvers()` 仍是类别旁 switch），加上 aggregator 计划
  路径在 verdicts 缺席时以通用 `Normalize` 猜 CallRevert usability 的
  实际回退路径。
- **迁移范围**：`rpc/call_revert.go`（outcome 两个入口 + 统一
  adjudicated 核心 + admission skips 分类）、`rpc/strict.go`
  （verdicts 分类共享化，行为不变）、`aggregator/read_plan.go`
  （checked capability read）、`rpc/methods/plan_validation.go`
  （capability 双向绑定检查）；新增四个 interlock/等价测试与本文。
- **保留的 Policy 深模块**：`rpc.Strict`、`rpc.Median`、`rpc.Local`、
  CallRevert、`blockPolicy` 及兼容 decorator 家族全部保留；本轮只把
  CallRevert 补进 Outcome seam，并冻结归属表。
- **回退方式**：Capabilities 全部增量（CallRevert 的两个新方法、
  绑定检查的两个分支、计划路径的 checked read），单次 revert 不影响
  Resolve/ResolveCorroborated 行为；defense：`call_revert_outcome_test.go`
  等价对比在回退后仍会通过（该断言早于改动成立）。
- **未决问题**：§6 登记（blockPolicy 归 RS2，wrapper 删除条件归 RS4，
  扁平化决策归 RS5；W0 诊断独立推进）。
