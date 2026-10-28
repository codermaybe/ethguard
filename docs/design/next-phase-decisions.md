# Next-Phase Decisions（原则冻结记录）

> 状态：已冻结（P0 原则冻结，2026-10-29）。本文件记录 `next-phase-architecture-proposal.md`
> §11「决策时机」中划为原则项的决定。方向性决定在此冻结；形态性决定
> （`MethodSpec` 字段集、`Invocation`/`Outcome` 细则、门槛合并）在各自轨道冻结，
> 记录点见各条。统一验收（GPT 复核 + 人工验收）可推翻单项，推翻时在本文件追加
> 修订记录，不改写历史条目。

## 1. 冻结的原则项

| # | 决策 | 冻结值 | 依据 |
| --- | --- | --- | --- |
| D1 | link-time 机制 | 采用 **B：`gethcompat` 独立兼容包** 作为过渡；终态 **C：删除 geth adapter**。不使用 build tag（A 不采用，无回退窗口需要）。 | §9.4 当前倾向（2026-10-29 修正） |
| D2 | geth adapter 去留 | 最终删除；native WSS 落地并经真实 provider 验证后执行。删除前 geth adapter 必须与 native 通过同一套 L1 contract suite。 | §11 待定决策表 |
| D3 | Read 覆盖目标 | 按**安全契约评审集合**推进；方法数量只作结果，不作退出条件。未通过评审的方法保持 Unsupported/Forwarded。 | §6.3、§10 阶段 4 |
| D4 | `WithHTTPAdapter` | 保留一个迁移周期。默认构建下选择 `HTTPAdapterGeth` 返回显式构造错误并指向 `gethcompat`；公共签名不随构建改变。 | §9.4 |
| D5 | contract 文档基线 | **新建** transport L1 contract 规范（`docs/design/transport-contract.md`）；`http-transport-differential.md` 降级为兼容性诊断说明，不再是 native 的规范性 gate。 | §9.6 |
| D6 | surface manifest | bump-time 工具，不作 CI gate。 | §9.6 |
| D7 | `Invocation` 形态 | 阶段值；context 仅作过渡载体。**不破坏公开 `Policy` 签名**（方案 A 约束）。不可变/Clone/单次 `WithReference` 规则在轨道 S 冻结（见 `rpc/invocation.go` 注释）。 | §5.2、§11 |
| D8 | `Outcome` 形态 | 可选 `OutcomeResolver` 能力 + 旧 `Policy.Resolve` 回退；`Policy` 公开签名不变。verdict 必须携带 source identity 与错误。 | §5.3 |
| D9 | `Basis`/`SourceVerdict` 归属 | `rpc` 包。`Basis` 只含 `Members []SourceRef` + `Corroboration`，不持有 `*pool.Source`。 | §5.2 |
| D10 | Reference 边界与组合根 | Reference 建立是 Read invocation 的**内部阶段**；root `anchor.go` 只转发 runtime-owned `ReferenceService`；生产组合点唯一在 `internal/runtime`；「两次 Aggregate 共享 Basis」仅作迁移步骤。 | §5.2（2026-10-29 已定） |
| D11 | Transport Port 所有权 | `pool` 语义拥有 Transport seam；物理包为 Port-only 的 `internal/transport`。实现拆包前同步修订 `codebase-topology.md`。 | §9.3 |
| D12 | 订阅范围 | 先 3 个 ethclient 订阅（newHeads/logs/transactionReceipts）；Port 现在不加 `Subscribe`。 | §7.4 |
| D13 | WSS / Stream | 暂时搁置；native HTTP 不承载订阅。Stream 语义（corroborated 事件 vs per-source）保持待定。 | §7 |
| D14 | proof 状态语义 | Verification 保持正交证据维度；数据出来前不新增顶层 `Verified` status。 | §8.2 |
| D15 | `MethodSpec` | spec 为靶、方案 A 先行；**字段集在 5 类纸上原型通过后冻结**，冻结后新增维度只能走 `Custom` 逃生口。 | §5.4 |
| D16 | 门槛公式 | 先语义表 + 表驱动测试锁定两套公式（轨道 S1）；合并决策后置。reference 的手工后检在合并前保留为断言。 | §3.2 |

## 2. 实现备注（非决策，防止误读）

- `rpc` 不得 import `internal/uncertain`（`uncertain` → `rpc` 依赖已存在）。提案 §5.2 中
  `Invocation.capture *uncertain.Capture` 在 `rpc` 侧落地为 `rpc` 自有的最小捕获接口，
  由 `uncertain.Capture` 结构性满足；并发安全仍由实现保证。
- 命名硬规则（§10.2）适用于全部轨道：生产/测试文件与标识符使用业务语义命名，
  任务编号只出现在提案、分支与提交说明。

## 3. 修订记录

（空；统一验收后的任何推翻在此追加。）
