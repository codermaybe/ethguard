# 最终代码审计报告（切片 15）

状态：现行（2026-11-04）。本报告是切片 1–15 全部代码与文档提交的 Standards/Spec
双轴审计记录。审计对象为 82a6ae1（切片 1）至 f4eb5be（本审计的收尾清理）之间的
全部提交。

## 门禁证据（全部实际运行）

| 门禁 | 结果 |
| --- | --- |
| `gofmt -l .` | 无输出 |
| `git diff --check` | 通过 |
| `go build ./...` | 通过 |
| `go vet ./...` | 通过 |
| `go test ./... -count=1` | 全绿 |
| `go test ./... -race -count=1` | 全绿（切片 9/10/11/12/13 各自叠加相关包 race） |
| `go test . -run 'TestArchitecture\|TestPackage\|TestDefaultBuild'` | 通过 |
| `go test ./internal/verification/integration -run '^TestVerification' -race -count=2 -shuffle=on` | 通过 |
| `deadcode -test ./...` | 只剩公开 API（`UncertainCapture` 访问器、`Client.SendTransaction`）与接口测试桩（`portStub`/`dialerStub`） |

## Standards 轴

1. **业务命名**：无 A1.go、phase_*、task_* 类命名（`find` 扫描为零）。本轮新增
   文件均按业务命名：`composition.go`、`anchor.go`、`block_reference.go`、
   `anchor_decorators.go`（legacy 家族删除边界）、`field_aggregation.go`、
   `method_names.go`、`plan_behavior_test.go`、`plan_fixtures_test.go`、
   `round_phases_test.go`、`call.go`（typed）。
2. **依赖方向**：架构 interlock 全绿。新增两条约束并在本轮真实受益：
   root facade 不再导入具体 transport（组合根在 internal/runtime）；核心运行时
   禁止导入工具/证据包（livecanary/shapeprobe/rpcsample）。ethadapter 不再依赖
   internal/refcontext（typed 标记迁至 rpc 公开边界）。
3. **删除范围**：全部经 deadcode + grep 双重确认（切片 3 的化石；切片 9 的
   23 个 constructor、decorator 家族、Registry 路径、aggregator 双分支；
   切片 11 的 facade 私有 establishment；切片 12/15 的孤儿 helper）。相关
   历史决策以「Deletion record」追加，未改正文。
4. **测试真实性**：所有重写的测试直接验证 CompilePlan 产物与业务 fixture；
   replay 期望数据（capture/expect.json）未改动，仅重算臂改为 plan 语义；
   matrix 文档只把 Tests 列改指现行测试，未删行。
5. **每个提交单一意图**：1–15 共 16 个提交，逐一对应该切片声明的业务边界；
   三次因外来文档（analyze.md、test-framework addendum）被误卷入而 amend 移出。

## Spec 轴（不变量核对）

| 不变量 | 结论 |
| --- | --- |
| Decision 语义 | Status 集合与升级规则未变；forward 语义显式化（`forwardPlan`）后，未注册方法仍 Forwarded/Unknown，绝不 Corroborated |
| quorum 语义 | 0028/0029 快照冻结点未动；`TestDefaultCorroborationFloorIsTwo` 等原测试保持绿 |
| reference 语义 | facade 与 invocation 共用 ReferenceStage 后，evidence 的 participants/support/reject/faults/collection faults/部分失败可见性由 `round_phases_test.go` 固化；失败轮次证据保留（Establishment 失败仍发布到 capture） |
| typed 语义 | call/revert 投影迁入 internal/typed，数据状态（missing/null/empty/hex/object）断言逐项保留 |
| 写路径 | `Client.SendTransaction`/write pipeline 未触碰 |
| 方法数量 | 23 个注册方法不变（`TestRegisteredMethodCount`、catalog interlock、moving-tag matrix 全绿） |

## 已知边界（如实记录）

- `internal/rpcsample` 的 replay 期望仍基于 2026-09-23 捕获；切片 16–20 的测试
  框架（HTTP fault、WSS 生命周期、Anvil fork）按
  `external-rpc-test-framework.md` 与 addendum 另行实现，不在本轮。
- W4/provider-backed null golden 门仍未通过（真实 null 未观察），与
  roadmap 记录一致，未被合成数据关闭。
- `rpc/policy.go` 保留了 Policy 深模块与可选 capability 接口（0063 裁决），
  是有意的架构保留，非遗留遗漏。
