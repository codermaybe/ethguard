# Method execution migration plan

状态：执行计划 / 2026-10-30

目标执行者：glm5.3flash。完成后由维护者按本文验收，不以“测试通过”替代架构验收。

## 目标

把 Read 方法的事实行为从 decorator chain 迁移到一个显式、可检查的执行计划。
`MethodSpec` 不再只是生成一条链，而是生成 runtime 注册的 `ReadPlan`（名称可在实现
时调整，但必须表达方法执行计划的业务语义）。aggregator 按固定阶段执行计划：

```text
Validate / Prepare
→ ReferenceRequirement
→ ReferenceStage（需要时）
→ PrepareWithReference
→ Collect
→ Admit
→ Resolve Outcome
→ Project / Observe
```

现有 `rpc.Policy`、decorator 和手写 constructor 保留为迁移期兼容层。它们不能继续
作为 runtime registry 的事实来源；迁移后的方法必须由显式计划驱动。

## 当前事实

- `rpc/methods/compile.go` 已有编译器，但仍通过 `compileResolver`、`applyReference`、
  pending、admission 和 validation decorator 组装链。
- `internal/runtime` 仍把 `Compile` 结果注册为 `rpc.Policy`。
- `CompileDeps` 仍带有 `AnchorHash` / `AnchorHeight`，只是消费型 callback，仍把
  reference 形状藏在链中。
- equivalence test 目前证明“编译链与手写链等价”，尚未证明“显式计划与链等价”。

## 目标目录

```text
rpc/
├── read_plan.go             # 通用计划值模型或消费 seam
├── invocation.go / basis.go / outcome.go
└── reference_request.go

rpc/methods/
├── spec.go                  # MethodSpec 与方法 inventory
├── compile.go               # Spec → ReadPlan
├── plan_validation.go       # 组合合法性检查
└── eth_*.go                 # 方法语义、Normalize、Encode、Validate、Admit

aggregator/
├── read_plan.go             # 计划执行 seam
├── phases.go                # 固定阶段编排
└── invocation.go            # 一个 Invocation、一个 Basis、多个网络轮次

internal/runtime/
├── method_registry.go       # ReadPlan registry
├── reference_stage.go       # 已有 ReferenceStage
└── projector_registry.go

rpc/legacy/                  # 只有确有需要时再建立；不提前创建
```

`rpc/methods` 不依赖 `aggregator` 或 `internal/runtime`。`aggregator` 定义它消费的
最小计划接口，`internal/runtime` 负责把方法计划、ReferenceStage 和 projector 装配
起来。不要为了迁移创建以任务编号命名的目录或文件。

## 工作波次

| 任务 | 内容 | 交付物 | 验收 |
| --- | --- | --- | --- |
| M0 | 冻结执行计划接口和阶段语义 | `ReadPlan`/消费 seam、阶段顺序、缺省行为和错误规则 | 文档和类型只表达一套阶段顺序；不再新增 decorator 能力 |
| M1 | 编译器改为生成显式计划 | `Compile` 返回计划；`MethodRule`、能力和 reference 需求从计划派生 | runtime 可注册计划；编译期拒绝缺失 resolver、非法 pending/reference 组合 |
| M2 | aggregator 消费计划 | 计划执行适配器，保留旧 Policy fallback | 迁移方法走计划路径；未知方法和未迁移方法仍可走旧路径 |
| M3 | 迁移 `eth_getBalance` | 普通 strict + hash reference + EIP-1898 + pending-local | 新旧路径在 Prepared、Reference、Decision、Faults、typed value 上等价 |
| M4 | 迁移 `eth_getLogs` | Validate、height reference、字段范围、admission、projection | moving-tag、字段锚定、admission 和 source evidence 全部保持等价 |
| M5 | 迁移 `eth_call` | Custom/CallRevert 双维计划 | RPC error body、revert data、Provenance、Outcome 和删除字段语义不丢失 |
| M6 | 迁移 `eth_getBlockByNumber` | header-only/full ResultForm、HeadCodec 复用 | reference head 解码不依赖编译后 Policy；source spelling 与 canonical value 等价 |
| M7 | 扩展普通方法 | 按 catalog 的安全契约逐簇迁移 | 每个方法有明确 Registered/Unsupported/Forwarded 状态；数量不是目标 |
| M8 | 移除 runtime 对 decorator chain 的依赖 | 旧 constructor 仅保留兼容测试或明确 legacy surface | 默认 runtime registry 不再生成或检查 decorator chain；equivalence test 改为计划级行为测试 |

M3–M6 是准入切片。任何一个切片未通过前，不得批量迁移其他方法。

## 每个任务的执行规则

每项任务必须：

1. 先读取现有方法测试、moving-tag matrix、admission suite、MethodRule 测试和对应
   integration test，列出行为不变量。
2. 只修改完成该任务所需的 Module；不要顺手重命名目录、删除 legacy constructor
   或改变 public facade。
3. 保留可回退 seam：迁移方法使用计划，其他方法继续使用旧路径。
4. 增加行为测试，不增加只验证实现细节的镜像测试。
5. 每个提交只表达一个真实业务行为，提交标题使用例如：
   `ref(methods): execute balance reads from an explicit plan`。

任务编号只能出现在 issue、分支、提交说明和本计划中。生产文件、测试文件、类型、
函数和 fixture 必须使用业务语义命名，不得创建 `m3.go`、`phase_m4_test.go` 或
`task_m5_fixture.go`。

## 计划级行为要求

`ReadPlan` 必须能表达以下事实，而不依赖 decorator 层数或 type assertion 遍历：

- 是否需要 reference，以及 reference kind/参数位置；
- pending 是 local、reject 还是 none；
- Validate 是否先于 reference；
- Admit 是否需要同源 `AdmissionCaller`；
- resolver 是 strict、median、local、call-revert 还是 custom；
- Normalize、Encode、KeySource、ResultForm 的关系；
- ObservationProjector 和 typed projector 的 runtime binding；
- 是否允许 `Corroborated`、`Selected` 或 `Forwarded`；
- `MethodRule` 的完整投影。

`AdmissionCaller` 仍是执行期依赖，不能进入静态 compile dependencies。reference
建立必须由同一个 Invocation、Basis 和 coordinator 执行；计划不能持有会重新发起
Aggregate 的网络 callback。

## 验收矩阵

每个迁移方法都要在以下维度有测试或明确的“不适用”结论：

```text
request validation
moving tag / explicit reference / pending
reference failure and zero business fan-out
fixed membership and threshold
admission and same-source dependency
per-source verdicts and fault attribution
Decision.Status / ReferenceEvidence
canonical value and comparison key
ObservationProjector / typed projection
Forwarded / Selected / Corroborated boundary
```

统一检查：

```text
gofmt -l .
go test ./...
go vet ./...
go test -race ./rpc/... ./rpc/methods/... ./aggregator/... ./internal/runtime/...
```

计划完成的最低条件：

- M3–M6 全部通过行为等价和 trace 验收；
- runtime 默认 registry 中迁移方法不再由 decorator chain 驱动；
- `CompileDeps` 不再暴露 `AnchorHash` / `AnchorHeight` 网络 callback；
- 旧 Policy 路径仍有明确边界，未迁移方法不会被静默改变；
- MethodRule、Outcome、ReferenceEvidence 和 typed projection 都从同一计划语义投影；
- 架构 interlock 没有新增 allowlist 或第二个 registry。

## WSS 后置门

本计划不实现通用 WSS 客户端，也不在方法计划迁移期间改 Transport。方法执行计划
完成后才允许做一次 service-specific WSS feasibility review，范围限于 EthGuard
需要的 Ethereum subscription/read service：原始 frame、错误分类、generation、
cursor、背压和 unsubscribe 结果。

只有在以下条件同时满足时才进入实现：

- ReadPlan 已稳定，Stream 不会重新侵入 ReadPlan；
- native HTTP 与基础 Transport contract 不被迫添加 `Subscribe`；
- WSS adapter 有独立 seam 和独立 contract；
- 至少一个真实 provider capture 能覆盖连接关闭、重连、reorg 和背压；
- 订阅事件的 evidence/status 语义已单独决策。

若 feasibility review 无法提供这些证据，WSS 保持设计任务，不进入生产实现。
