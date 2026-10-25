# EthGuard 当前路线图

状态：**拓扑迁移、架构收敛与 T09 整体审计已完成，进入逐方法对接阶段**（2026-10-25）

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
- T09 整体审计已完成，当前 23 个已注册方法进入逐方法对接准备；
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

## 下一阶段：逐方法对接

逐方法对接的目标是把每个已注册 read method 的 policy、root facade、ethadapter
形状、typed projection、错误映射和验证入口闭合起来，不新增方法，也不改变裁决语义。

每个方法是一个独立切片和独立提交，至少核对：

1. `rpc/methods` catalog、root `Client` 和 `ethadapter` 是否有唯一对应关系；
2. 请求参数和 wire form 是否与 policy 的 Prepare/EIP-1898/pending 规则一致；
3. Decision.Value、typed projection、`ethereum.NotFound`、sentinel 和 revert/error
   形状是否在两种 facade 中一致；
4. anchor、pending、local/median/strict 状态和完整参与分母是否由现有 seam 处理；
5. 是否已有 root/adapter/verification 覆盖，缺口属于测试、文档还是实际实现。

建议顺序：

- 简单确定性与本地方法：chain ID、gas/fee scalar、net、peer/listening、block number；
- anchored state：balance、nonce、storage、code、call、estimate gas；
- 对象与数组：block、transaction、receipt、block receipts、logs、fee history、
  access list；
- 兼容性边界：revert data、pending、EIP-1898 object、NotFound 和错误形状。

agent 只接手已有契约下的局部 Adapter/测试切片；需要重新定义 reference、错误、
并发、Transport 或 evidence 语义的判断由主维护者完成。每个切片保持离线、可回滚，
并运行全量和 `^TestVerification` 专项验收。

## 后续阶段：WSS transport 设计

WSS 只在 HTTP 查询路径逐方法对接稳定、Adapter 边界没有未决语义后进入。第一步是
单独的 transport 契约和 Adapter 设计，不直接修现有 geth WSS 行为。

必须先明确：

- 单请求/单响应生命周期和 request ID demux；
- context cancellation、bounded message 和 error envelope 保真，尤其是
  error data 缺失与 JSON `null` 的区分；
- Close 时释放 pending call；
- `pool.Transport` 替换点与现有 aggregator/reference/Decision 的不变关系。

subscription、filter 生命周期、自动重连、自动重放和未知结果恢复不属于第一阶段，
需要另立契约。WSS 不得改变 quorum、method policy、typed projection 或 evidence
语义。

架构收敛记录仍见[验证拓扑拆分决策](verification-topology-decision.md)、
[`rpc/methods` Module 边界审查](rpc-methods-boundary-review.md)和
[root facade 与 `ethadapter` 对接边界审查](adapter-boundary-review.md)。

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
