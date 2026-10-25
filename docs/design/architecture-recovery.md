# EthGuard 架构恢复记忆

状态：**当前架构基线**（2026-10-25）

这份文档用于在新会话、上下文压缩或更换维护者后恢复设计思路。它不是历史日志，
也不是新增任务清单。需要知道当前方案时，先读本文；需要看精确目录和依赖规则时，
再读 [代码库拓扑与模块所有权](codebase-topology.md)；需要知道执行顺序和出口时，
最后读 [当前路线图](project-roadmap.md)。

## 项目角色

EthGuard 是多源 Ethereum JSON-RPC 聚合客户端。它把多个 provider 的响应变成可审计
的 provider-backed cross-source evidence。

`Corroborated` 不代表 Ethereum consensus、canonical proof、cryptographic finality
或 light-client verification。多个 URL 也不自动代表独立来源。

系统的核心顺序是：

```text
Prepare / optional Reference
    → Collect
    → Admit
    → Normalize / Resolve
    → Decision
    → Typed Projection
```

这是一条概念管线。不同方法可以有不同的物理路径，但不能绕过方法契约、准入、
裁决和 typed 边界来制造成功。

## 设计词汇

- **Module**：拥有一类知识、Interface 和验证责任的目录边界；
- **Seam**：可以替换实现而不改变调用方的 Interface；
- **Adapter**：处理外部协议、连接生命周期或形状转换，不做业务裁决；
- **Composition Root**：把 Module、policy、Adapter 和 seam 绑定成一次运行时执行图；
- **Depth**：小 Interface 隐藏大量实现行为；
- **Locality**：知识、变更和验证集中在拥有者；
- **Leverage**：一个稳定实现服务多个 facade、调用方和测试。

目录不是功能文件的收纳表，而是维护者理解、分配和审阅代码的地图。

## 当前拓扑

```text
root facade
    ↓
internal/runtime（方法、projector、aggregator 的唯一生产装配）
    ├── pool + Transport Port
    ├── aggregator
    ├── rpc/methods
    ├── internal/reference + Head-Reading Port
    └── internal/typed

ethadapter ──单向委托──> root facade
internal/verification ──消费公开 seam──> production Modules
```

### root facade

根 package 的 `Client` 是公共入口，负责：

- 配置和 Option 解析；
- 公共方法与 Decision 入口；
- reference resolver 的公共 facade；
- 生命周期和兼容性方法。

它不应出现第二套方法 policy、第二套 typed decoder 或平行 transport 执行链。

### internal/runtime

`internal/runtime` 是生产执行图的唯一装配点，集中：

- `rpc/methods` policy registry；
- `rpc/projection` projector；
- `aggregator`；
- anchor hash/height seam 的绑定；
- runtime 级装配验证。

它不暴露公共 Client，不拥有方法字段语义，不做 quorum 或 reference 业务裁决。

### pool

`pool` 拥有：

- source 生命周期；
- lazy dial、连接复用和关闭；
- 健康度、调度、tip 和 eligibility；
- Transport Port。

Transport Port 只表达：

```go
CallContext(ctx, result, method, args...) error
ChainID(ctx) (*big.Int, error)
Close()
```

它不承载 subscription、filter、方法 policy、完整 block policy、quorum 或业务裁决。

### rpc 与 rpc/methods

`rpc` 拥有通用 Request、Observation、Policy、Decision、Evidence 和裁决模型。

`rpc/methods` 是一个生产 Module，拥有 Ethereum 方法的：

- 请求形态；
- Prepare / admission；
- comparison key；
- encoding；
- policy constructor；
- catalog。

不要因为方法文件多就提前拆成浅层 package。拆分必须先证明存在独立 Interface、
稳定知识所有权和足够深的 Module。

### internal/reference

`internal/reference` 拥有 reference 建立算法，并由它定义 Head-Reading Port。

Head-Reading Port 只读取建立 reference 所需的 head 和确认结果，不绑定具体业务方法，
不做方法 policy 组装，也不改变 quorum。

reference 是调用内的证据。没有跨调用 cache 或 TTL；HTTP/WSS 长连接不能变成
reference 复用。

### internal/typed

`internal/typed` 是唯一生产 typed projection：

- block、receipt、transaction 等对象解码；
- Decision.Value 到公开 Go 类型的投影；
- root facade 和 ethadapter 共用同一实现。

不引入公共 `TypedValue`。每个方法继续保留自己的 concrete return type。

### ethadapter

`ethadapter` 只做 ethclient-shaped 调用形状和返回形状转换：

- 不直接访问 `internal/runtime`；
- 不维护第二套 decoder；
- 不做 quorum、admit、conflict 或 reference 选择；
- 需要高级能力时通过 root Client 的 `Unwrap` 暴露。

### verification

当前安全切片位于：

```text
internal/verification/integration/
```

它保留了共享 fixture 的同一测试 package，避免过早导出测试专用 API。后续在
`internal/testkit` 稳定后，再按 seam 细分为：

```text
contracts/
scenarios/
integration/
replay/
```

生产代码不得导入 verification 或 testkit。

## 不变量

任何结构迁移和实现优化都必须保持：

1. 方法 policy、quorum、Decision、reference policy 和方法数量不变；
2. 错误返回不能携带成功置信状态；
3. 失败不能缩小投票分母、降低门槛、改走单源值或跨来源拼装；
4. Value 是方法契约定义的聚合结果，不是代表源原文伪装；
5. adapter 不拥有业务裁决；
6. Transport、Head-Reading、Typed 三个 seam 可以替换实现而无需改变调用方；
7. root facade 和 ethadapter 不产生第二套语义路径；
8. provider-backed evidence 不写成 consensus、canonicality 或 finality；
9. 测试默认离线，不把 live 结果写入 capture、golden 或 testdata。

## 当前已完成的结构工作

以下提交构成当前拓扑基线：

```text
cb86a34  external verification → internal/verification/integration
72d2153  method/projector/aggregator → internal/runtime
eca3534  typed projection → internal/typed
8f9c4e1  Transport Port + Head-Reading Port
807e0ba  preserve reference member snapshot during confirmation
058f52c  dependency interlock + README topology
1b2dfec  roadmap reset around architecture convergence
```

当前代码通过：

```text
go test ./... -count=1
go vet ./...
go test ./... -race -count=1
go test ./internal/verification/integration -run '^TestVerification' -race -count=2 -shuffle=on
```

## 后续路线

架构收敛边界已经收口：

- verification 保持单一 `integration` Module；只有出现第二个真实验证 seam，才重新
  评估 `testkit`、`contracts` 或 `scenarios`；
- `rpc/methods` 保持单一生产 Module；对象、wire 和 policy 深模块继续由各自拥有者
  维护；
- root facade 与 `ethadapter` 保持单向委托和单一 typed projection；没有第二个 Adapter
  或已证明的重复转换时，不抽公共 forwarding 层；
- 后续工作以具体故障、重复事实来源、维护负担或新 Seam 证据为入口，不按目录整齐度
  继续迁移。

需要重新定义并发、错误、连接生命周期或 evidence 语义的核心 seam，由主维护者直接
设计和实现；有明确边界且可回滚的局部整理才交给 agent。

## WSS 的位置

WSS 暂不接入本阶段。

当前 geth WSS 路径继续作为现状 Adapter 使用，不在本阶段修复 error.data
missing/null 保真问题，也不新增 WSS golden。

未来若需要专有 WSS RPC Adapter，它应替换 `pool.Transport`，而不是改变
aggregator、reference、Decision 或方法 policy。专有实现只负责：

- 单请求/单响应 JSON-RPC；
- request ID demux；
- context cancellation；
- 有界 message；
- error envelope 保真；
- Close 时释放 pending call。

subscription、filter、自动重连、自动重放和写入未知结果恢复必须另立契约。WSS
只有在 HTTP 查询路径、adapter 优化和架构边界稳定后才进入路线图。

## 明确不做

- 新增 RPC 方法；
- provider 权重、信誉和 failure-domain；
- light client、BLS、sync committee、trusted checkpoint；
- 为视觉整齐创建浅层转发 package；
- 在普通测试中联网；
- 通过降低门槛、缩小分母或自动补字段让测试变绿。

## 执行纪律

每个切片只包含一个结构或实现意图，并记录：

- Module 和所有权；
- 不变的运行时语义；
- 命令和失败路径；
- 是否触碰网络、capture、golden、testdata；
- 未完成项和剩余风险。

结构移动优先使用官方 `gopls` Rename 或 `git mv`。任何无法证明是纯结构变化的
操作，都单独设计、单独提交、单独验证。
