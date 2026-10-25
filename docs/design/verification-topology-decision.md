# 验证拓扑拆分决策

状态：**当前保持单一 integration Module，暂不创建 testkit 或拆分验证 package**
（2026-10-25）

本文记录架构收敛阶段对 `internal/verification` 下一步拆分的判断。它只决定验证
代码的 Module、Interface 和拆分时机，不改变生产代码、方法契约、quorum、Decision、
typed projection 或 reference policy。

## 当前事实

`internal/verification` 目前只有一个目录和一个 Go package：

```text
internal/verification/integration/
    package ethguard_test
```

20 个测试文件都在这个 Module 内。T09 的 7 个语义化文件与其余 integration 测试
共享 package 级 fixture，但目前没有跨 verification Module 的消费者。

当前共享关系有两种不同形状：

- 非 T09 integration 测试共同使用 `fleet`、anchor、receipt 和 decision-event
  fixture。`fleet` 由 `integration_test.go` 定义，服务多个 anchor、reference、
  voting、write 和 return-value 测试；
- T09 测试共同使用 `t09Fleet`、`t09NewChain`、field rows、typed probes、JSON
  变体和 scenario harness。T09 的 field、mutation、conservation、scenario 和
  typed 文件之间存在大量双向使用关系。

`rpctest` 是仓库级的 HTTP 测试节点设施，已被多个生产 Module 的验证包使用。
`internal/rpcsample/replay` 是独立的 replay Module；当前 verification package
不消费它，也没有需要新建的 verification/replay 目录。

## 决策

本切片保持 `internal/verification/integration` 作为一个深 Module，不做以下操作：

- 不创建 `internal/verification/contracts`、`scenarios` 或 `replay` package；
- 不创建 `internal/testkit`；
- 不移动 T09 文件；
- 不把 `t09Fleet`、`t09Decode*` 或 typed probe 导出成测试公共 API；
- 不让生产代码导入任何 verification package。

理由是当前只有一个验证 Module，尚不存在可替换的外部 seam。现在创建 testkit 会把
一个带有 `testing.T`、root Client、adapter 和 `rpctest` 细节的浅层转发层暴露给未来
调用者，接口几乎等于实现，既没有足够 Depth，也没有形成新的 Leverage。保留单一
integration Module 能让 fixture、场景和断言保持 Locality，删除或修改一条测试设施
时不会把复杂度散落到多个 package。

## 未来候选分组

这些是未来的验证职责候选，不是本次授权的文件移动：

| 候选 Module | 可能内容 | 当前阻碍 |
| --- | --- | --- |
| `contracts` | typed 一致性、schema 路径、编码/准入边界 | 目前依赖 T09 fixture、field rows 和真实 Client；尚无独立 Interface |
| `scenarios` | 方法决策矩阵、字段生命周期、mutation、evidence 守恒 | scenario harness、fleet、chain 和 typed probe 仍跨文件共享 |
| `integration` | root facade + pool + aggregator + reference 联合验证 | 当前 20 个文件整体就是这个 Module |
| `replay` | capture/golden 离线回放 | 已由 `internal/rpcsample/replay` 拥有，不应复制 |

T09 的 `method_scenarios_test.go`、`scenario_harness_test.go`、
`field_lifecycle_test.go`、`protected_mutations_test.go`、
`evidence_conservation_test.go`、`typed_projection_test.go` 和
`verification_fixtures_test.go` 可以作为未来 `scenarios`/`contracts` 的候选素材，
但不能按文件名直接搬运。文件的物理归属必须服从新的 Module Interface 和 fixture
所有权。

## 允许开始实际拆分的条件

后续切片必须同时满足：

1. 至少存在两个有不同公开验证 seam 的 verification Module，而不只是两个目录名；
2. 共享 fixture 的消费者至少跨越两个 Module，并且其 Interface 可以缩小到稳定的
   测试 Adapter，不暴露生产内部状态；
3. `testkit` 若成立，必须隐藏真实节点、Client/adapter 构造和清理生命周期，让调用方
   通过小 Interface 驱动测试；
4. `contracts` 与 `scenarios` 的断言可以分别通过公开 seam 验证，不需要互相调用
   对方的未导出 helper；
5. replay 继续复用 `internal/rpcsample/replay`，除非出现独立的 verification replay
   Interface；
6. 迁移不要求生产代码导出测试专用符号，也不建立第二套 typed decoder 或执行链。

## 下一步

在满足上述条件前，继续在当前 integration Module 内做语义化命名和局部 helper 收敛。
下一次真正的拓扑变更应先提交一个只描述 Module Interface、Adapter 和迁移顺序的设计
切片，再进行文件移动。任何无法证明为纯结构变化的操作都要单独设计、单独提交和
单独验收。
