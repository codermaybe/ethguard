# Method Execution Migration Report（M0–M8 实施与验收状态）

> 状态：已执行、按计划文档验收（2026-10-31）。本报告记录
> `method-execution-migration-plan.md` M0–M8 的提交、门禁证据，以及
> "摘掉 decorator chain"的准确当前边界。它不是最终方法模型的宣称；下一轮
> 程序边界梳理（resolver 内部 seam、WSS feasibility review）另行展开。

## 1. 提交对照

| 任务 | 提交 | 交付物 |
| --- | --- | --- |
| 计划冻结（文档） | `53366d0` | 执行迁移计划文档；阶段顺序只表达一套 |
| M0 值模型 | `6094a38` | `rpc.ReadPlan` 显式计划值模型：reference/pending 声明、引用要求派生、计划化改写、按声明投影 MethodRule |
| M1 编译器 | `f036045` | `Specs()` 冻结 23 方法 → `CompilePlan` 组装计划；编译期拒绝非法组合 |
| M2 aggregator | `0c05d1f` | 固定阶段管线执行计划路径；旧 Policy fallback 保留 |
| M3 getBalance | `4d6a73d` | 普通 strict + hash reference + EIP-1898 + pending-local，fleet 等价 |
| M4 getLogs | `bf91601` | height-fields reference、fields default、admission、projection 等价 |
| M5 eth_call | `317d7a9` | CallRevert 双维计划，revert data 与 Provenance 等价 |
| M6 getBlockByNumber | `d1f640b` | block wrapper 计划化：header-only/full ResultForm、HeadCodec 复用等价 |
| M7 普通方法 | `e79a3a5` | 读 catalog 逐簇迁移；Registered/Unsupported/Forwarded 状态显式 |
| M8 registry | `476e43d` + `038c421` | 默认 runtime registry 只由 plans 装配；不再生成或检查 decorator chain；方法/projector registry 包装为 assemblies |
| M8 补充（本轮） | 本提交 | `ObservationProjectorID` 从死字段变成真实绑定（见 §3） |

## 2. 门禁证据

```text
gofmt -l .                                  # 无输出
go test ./...                               # 通过
go vet ./...                                # 无诊断
go test -race ./rpc/... ./rpc/methods/... ./aggregator/ ./internal/runtime/   # 通过
git diff --check                            # 无输出
```

WSS 未进入本轮，符合计划的后置门；是否进入仍由 WSS feasibility review 的
证据决定，不由本报告决定。

## 3. M8 补充：projector 计划绑定

本轮验收指出的真实缺口：`ReadPlan.ProjectorID` 和
`MethodSpec.ObservationProjectorID` 定义了但没有任何方法填写；树看执行时
按 `req.Method` 查 projector，plan 只在编译时写字段、不参与 lookup——测试
通过只是因为 projector registry 恰好用方法名做 key。该方法别名、多方法
共享 projector 或 registry 改名都会静默失效。

已修复：

1. `rpc/methods/spec_inventory.go` 为有 projector 的方法声明明确的业务 ID
   （`ProjectorScalarStandard`/`ProjectorBlock`/`ProjectorReceipt`/
   `ProjectorBlockReceipts`/`ProjectorTransaction`/`ProjectorLogs`/
   `ProjectorFeeHistory`）；每个 ID 是一个投影族（观测形状），`scalar-standard`
   由 `eth_getBalance` 与 `eth_call` 共享，`block` 由两个 block 方法共享，
   证明 ID 与方法名可以不同、多方法可以共享同一 projector。
   没有 projector 的方法明确保持空值（不回退到方法名）。
2. runtime projector registry 改为按业务 ID 绑定 projector；ID → 实现的
   决定留在 runtime，`rpc/methods` 仍不接触 projection/typed 层。
3. aggregator 计划路径优先按 `plan.ProjectorID` 查找，再回退 resolver 自身
   的 `ObservationProjector` 能力；legacy 路径保持"方法名 + chain 能力"。
   计划路径不回退方法名。
4. 测试：
   - `TestProjectorRegistryBindsEveryDeclaredID`：声明 ⇄ 绑定双向 interlock。
   - `TestSharedProjectorIDIsNotTheMethodName` / `TestProjectorRegistryBindsOneInstancePerFamily`：
     共享 ID 与方法名解耦、每族一个绑定。
   - `TestPlannedReadResolvesTheDeclaredProjectorID`：projector 只注册在
     非方法名的别名下时计划路径仍能投影。
   - `TestPlannedReadWithoutProjectorIDStaysUnprojected`：空 ID 是事实，
     不回退方法名、不产生投影 fault。

## 4. "摘掉 decorator chain"的准确边界

本轮摘掉的是**方法准备和阶段编排**上的 decorator chain，不是所有 Policy
形态。当前状态按层次记准：

已计划化（不再走 decorator 遍历/能力断言）：

- 阶段顺序（validate/prepare → reference → prepare-with-reference →
  collect → admit → resolve → project）由固定管线拥有；
- reference 需求派生与请求改写（string/object/fields/EIP-1898）；
- pending 声明与本地拒绝/观测路由；
- admission 阶段化与同源 caller 注入；
- BlockRef 的改写期 pin 记录；
- MethodRule 的声明投影（锚、BlockRef、Pending 列）。

仍通过 `rpc.Policy` seam（resolver 内部，保留为深模块）：

- `ReadPlan.Resolver` 仍是一个 `rpc.Policy`；plan 只声明它的类别
  （`ResolverKind`），不替换它；
- capability 对 `rpc.OutcomeResolver`、`CorroboratingPolicy`、
  `AgreementPolicy` 的询问（`resolvePlannedInvocation`）；
- `ResolverBlock` 仍是 `blockPolicy` 包装的 strict resolver；
- `ReadPlan.MethodRule()` 仍从 resolver 的 `MethodRuleSource` 读取
  resolver 侧规则（CompareKind、KeySource、Result 列）；
- resolver 内部的归一化、聚类、CallRevert 与 block-specific resolution。

这可以描述为：**方法级阶段行为已由 ReadPlan 拥有；resolver 内部仍是一个
允许保留 resolver-specific implementation wrapper 的深模块。** 文档与
验收表述不再写成"所有行为都完全脱离 Policy/Decorator"。把 resolver 内部
是否继续扁平化摊开，属于下一轮程序边界梳理，不并入 M0–M8 的验收结论。

## 5. 决策治理

- `docs/decisions.md` 的 0040 增补了修订记录：其测试纪律、方法差异立场与
  "不做通用 DSL"约束保持有效；"decorator composition 是唯一可执行事实"
  的执行机制被 ReadPlan supersede；legacy constructor 保留为兼容与等价
  验证 seam。
- `next-phase-implementation-report.md` 增补了修订记录（历史行不回改），
  指向本报告说明 registry 组装方式的当前状态。
- "decision"一律是当前阶段的可审计约束，不是永恒真理。未来如果 resolver
  plan、ZK verification 或 WSS 的真实证据要求改变，按 0040 修订记录中的
  同一格式追加新修订：保留历史、给出触发证据、写明迁移范围。
