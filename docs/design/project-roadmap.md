# EthGuard 当前路线图

状态：**拓扑迁移与架构收敛边界已完成，进入证据驱动的维护阶段**（2026-10-25）

本文件只描述当前事实、下一阶段和验收出口。已经完成的功能任务、历史审计过程和
被否决的候选方案以 Git 历史及各专题设计文档为准，不在这里重复展开。

恢复上下文请先读 [架构恢复记忆](architecture-recovery.md)，再读
[代码库拓扑与模块所有权](codebase-topology.md)。本文只负责路线和阶段出口。

## 当前基线

EthGuard 的运行时语义已经冻结在现有实现中：

- `Corroborated` 是 provider-backed cross-source evidence，不是 Ethereum
  consensus、canonical proof 或 cryptographic finality；
- Decision、quorum、reference policy 和方法契约不因目录迁移改变；
- T09 的方法场景矩阵、字段生命周期和 typed 边界已完成；
- T10 的离线/live 证据分层已完成，live observation 不被写成普遍兼容性证明；
- WSS 无法区分 error data 缺失与 JSON `null`，这是当前 transport 限制；
- reference policy 统一方案仍是未立项的候选，不授权实现。

拓扑迁移已完成：

- root 外部验证位于 `internal/verification/integration`；
- `internal/runtime` 集中方法、projector 和 aggregator 的生产装配；
- `internal/typed` 是唯一生产 typed projection；
- `pool.Transport` 是 pool 消费者拥有的最小 transport seam；
- `internal/reference.HeadReader` 是 reference 消费者拥有的最小 head-reading seam；
- root facade 与 `ethadapter` 不存在第二套 typed decoder 或平行执行链；
- 架构依赖互锁已进入根测试并由 CI 执行。
- T09 测试入口已完成语义化重命名，专项门使用 `^TestVerification`；
- verification 当前保持单一 `integration` Module，不创建 `testkit` 或浅层验证 package；
- `rpc/methods` 保持单一生产 Module，已有对象、wire 和 policy 深模块继续拥有共享语义；
- root facade 与 `ethadapter` 的对接边界已审查，暂无真实重复转换或第二个 Adapter seam。

正式拓扑规格见 [codebase-topology.md](codebase-topology.md)，迁移执行记录见
[codebase-topology-migration.md](codebase-topology-migration.md)。

本阶段提交：

```text
cb86a34  verification topology
72d2153  runtime composition
eca3534 typed projection
8f9c4e1 transport/reference seams
807e0ba reference member snapshot preservation
058f52c topology guards and README
ec1fe66 roadmap pointer
```

架构收敛审查记录：

```text
4cffdeb verification topology split timing
04ee039 rpc/methods boundary review
31f60b3 adapter boundary review
```

当前基线命令：

```text
go test ./... -count=1
go vet ./...
go test ./... -race -count=1
go test ./internal/verification/integration -run '^TestVerification' -race -count=2 -shuffle=on
```

## 当前阶段：证据驱动的维护

架构收敛已完成决策收口。后续变更只有在出现具体证据时才开始：

- 已证明的重复转换、第二份事实来源或局部维护负担；
- 第二个验证 Module 或第二个 Adapter 实现，形成真实可替换 Seam；
- 新的 Interface 能显著增加 Depth、Leverage 或 Locality；
- 现有测试或静态守卫暴露出明确缺口。

没有这些证据时：

- 不创建 `internal/testkit`、`contracts`、`scenarios` 或新的 forwarding package；
- 不按文件数量拆 `rpc/methods`；
- 不为了“减少行数”抽取 root/adapter 公共转发层；
- 不把 agent 分配到需要重新定义并发、错误、连接生命周期或 evidence 语义的工作。

验证拓扑的拆分条件见[验证拓扑拆分决策](verification-topology-decision.md)，
`rpc/methods` 的边界条件见 [rpc/methods Module 边界审查](rpc-methods-boundary-review.md)，
对接层条件见 [root facade 与 `ethadapter` 对接边界审查](adapter-boundary-review.md)。

## 不进入下一阶段的内容

以下事项保持明确排除：

- 新增 RPC 方法或扩大方法覆盖；
- provider 权重、信誉和 failure-domain 模型；
- Ethereum light client、BLS、sync committee 或 trusted checkpoint；
- 把 provider-backed evidence 宣传为 consensus、canonicality 或 finality；
- 修复 WSS missing/null 能力缺口；
- 重新定义 quorum、Decision、reference policy 或 `expect/v1`；
- 为视觉整齐拆出浅层转发 package；
- 在普通测试中联网或把 live 结果写入 capture/golden/testdata。

## 执行规则与出口

每个结构切片独立提交，只包含一个结构意图。提交说明记录：

- 变更的 Module 和所有权边界；
- 保持不变的运行时语义；
- 实际验证命令和失败路径；
- 是否触碰样本、golden、testdata 或网络；
- 尚未迁移的文件和剩余风险。

每个切片至少运行：

```text
gofmt -w <changed-go-files>
go test ./... -count=1
go vet ./...
git diff --check
```

阶段出口还必须通过：

```text
go test ./... -race -count=1
go test ./internal/verification/integration -run '^TestVerification' -race -count=2 -shuffle=on
```

官方 `gopls` Rename 用于 package path 的闭合移动；同一 package 内的文件移动使用
`git mv`。任何无法证明为纯结构变化的操作都单独设计、单独提交、单独审计。
