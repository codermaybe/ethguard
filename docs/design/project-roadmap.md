# EthGuard 当前路线图

状态：**拓扑迁移完成，进入架构收敛阶段**（2026-10-25）

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

当前基线命令：

```text
go test ./... -count=1
go vet ./...
go test ./... -race -count=1
go test ./internal/verification/integration -run '^TestVerification' -race -count=2 -shuffle=on
```

## 下一阶段：架构收敛

下一阶段的目标是继续降低人类掌握和分配代码的成本，保持运行时语义不变。

### 1. 收敛验证所有权

先稳定共享 fixture，再细分验证目录：

1. 识别跨模块 fixture、测试 Adapter 和生产路径辅助函数；
2. 只有确实被多个验证 Module 复用时才建立 `internal/testkit`；
3. 将现有 integration 验证按公开 seam 逐步细分为：
   `contracts`、`scenarios`、`integration`、`replay`；
4. 必须访问未导出实现的白盒测试留在生产 package；
5. 生产代码不得导入 verification 或 testkit。

T09 验证测试的语义化文件结构（已应用）、helper/seam 消费者库存和验收边界见
[T09 验证代码迁移前置契约](t09-verification-migration.md)与
[验证测试 helper 与 seam 库存](verification-helper-inventory.md)。
当前是否创建 `testkit` 或拆分验证 package 的决策见
[验证拓扑拆分决策](verification-topology-decision.md)。

出口是维护者可以按 seam 分配测试任务，而不需要阅读整个 root 集成测试文件。
不为了填满目录而导出测试专用生产 API。

### 2. 收敛文档概念

以正式拓扑规格为唯一结构术语来源，清理 README、旧架构说明和方法验证文档中的：

- 过期目录名称；
- 已完成任务的执行日志；
- 重复的 quorum、typed、anchor 和 evidence 定义；
- 把历史观察写成当前承诺的措辞。

专题文档保留设计依据和当前边界；roadmap 只保留当前状态、下一步和出口。

### 3. 审查 `rpc/methods` 的真实功能边界

保持 `rpc/methods` 为一个生产 Module。只有在出现独立 Interface、稳定所有权和
明显更深 Module 时才拆 package。文件数量本身不是拆分理由。

本轮审查结论见
[rpc/methods Module 边界审查](rpc-methods-boundary-review.md)。

审查顺序：

1. 列出现有文件的知识所有者和共享字段表；
2. 找出真正跨方法共享的对象、wire 和 admission seam；
3. 删除重复的转发层或第二份事实来源；
4. 不改方法注册表、比较键、Value 编码和错误语义。

### 4. 优化对接实现层

在 runtime、typed、Transport 和 Head-Reading seam 稳定后，再优化 root facade 与
ethclient-shaped adapter 的实现层：

当前对接边界审查见
[root facade 与 `ethadapter` 对接边界审查](adapter-boundary-review.md)。

- adapter 只做调用形状和返回形状转换；
- typed projection 继续只有一条生产路径；
- transport 生命周期与方法 policy 分离；
- 为后续 RPC/ethclient seam 对接减少冗余；
- 不在这一阶段重新设计 reference policy 或新增网络能力。

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
