# `rpc/methods` Module 边界审查

状态：**保持单一生产 Module，不按方法文件数量拆包**（2026-10-25）

本文记录架构收敛阶段对 `rpc/methods` 的边界审查。结论只涉及 Module、Interface、
Seam 和所有权，不改变方法注册表、比较键、Value 编码、错误语义或方法覆盖范围。

## 当前 Module 形状

`rpc/methods` 是一个生产 Module，拥有 Ethereum JSON-RPC 方法的 wire semantics：

- 方法请求准备、请求绑定和方法级 policy assembly；
- 方法级 admission 入口及其与请求的绑定；
- 方法的比较规则选择和 canonical Value 编码绑定；
- `Catalog`、`RegisteredNames` 和 registry 完整性校验；
- block、receipt、transaction、logs 等跨方法对象的调用装配。

生产入口分成三类：

1. `eth_*.go` 和 `net_*.go` 是薄的 method assembly，负责把方法请求、共享
   normalizer、admission 和 policy decorator 组合起来；
2. `block.go`、`logs.go`、`pending.go`、`eip1898.go` 等是跨方法共享的 policy
   decorator 或对象绑定逻辑；
3. `catalog.go` 是唯一方法覆盖 inventory，不能被各方法子包分别维护。

生产代码的职责已经沿深模块拆开：

| Module | Interface/所有权 | `rpc/methods` 的关系 |
| --- | --- | --- |
| `rpc` | 通用 `Policy`、`Registry`、`Decision`、resolution strategy | 被 methods 消费，不反向依赖 methods |
| `rpc/internal/wire` | JSON wire normalization、pruning、spec primitive | 被方法 assembly 组合 |
| `rpc/internal/objects/*` | block、receipt、transaction、log、fee-history 等对象 requiredness、commitment 和 encoder | 被方法 admission/normalization 组合 |
| `rpc/internal/policy` | admission decorator 等通用策略装饰 | 被多个方法入口复用 |
| `rpc/methods` | Ethereum 方法请求形态、方法绑定、policy assembly、catalog | 当前审查对象 |
| `rpc/projection` | Decision 后的标准字段投影和 Carrier | 在 runtime 中按方法装配，不属于 methods |

这形成一条有深度的调用关系：方法调用方只需要 `Policy`/`Registry` 和方法构造器，
复杂的 wire、对象和 admission 行为隐藏在内部 Module 中。拆成按 namespace、方法
类别或文件数量划分的 package，会把 assembly 细节重新暴露给 runtime 和调用者。

## 为什么不拆 package

当前没有证据证明存在新的独立 Interface：

- `rpc/methods` 的生产方法都共享同一个 `rpc.Policy` contract 和 registry；
- block/receipt/transaction/log 的对象语义已经有独立且更深的 `rpc/internal/objects`
  Module；
- wire normalization 已经由 `rpc/internal/wire` 统一拥有；
- `pending`、EIP-1898 和 anchor decorators 横跨多个方法类别，按 namespace 拆分会
  产生循环依赖或重复装配；
- `catalog.go` 必须看到完整的方法集合，拆分后需要第二份 inventory 或额外的
  聚合层，都会降低 Locality；
- 绝大多数 `rpc/methods` 测试是白盒测试，直接验证方法级 admission、normalization
  和 decorator 组合。把它们按目录拆开不会形成更深的验证 Seam，只会复制 fixture
  和测试 setup。

文件数量不是拆分理由。当前 `eth_*.go` 文件是有意保持浅的 assembly 层，而共享对象、
wire 和 policy 已经承担真正的深实现。按文件数量继续创建 package 会得到浅 Module，
没有额外 Leverage。

## 保持的硬边界

后续改动继续遵守：

1. `rpc` 不导入 `rpc/methods`；
2. `aggregator` 只消费 `rpc.Policy` 和 collection Interface，不解释方法字段；
3. `internal/runtime` 是唯一生产装配点，负责将 methods、projector、aggregator 和
   reference seam 绑定；
4. `rpc/methods` 不拥有 pool 生命周期、transport、typed projection 或 reference
   选择；
5. `catalog.go` 是方法覆盖的唯一 inventory；
6. 不为视觉分组创建 `rpc/methods/<category>` 浅层转发 package；
7. 新增 RegisteredMethod 时，方法契约、request binding、wire/object 规则、admission
   和离线测试仍在同一变更意图内审查。

## 允许重新审查的条件

只有出现以下证据之一，才重新考虑新的 production Module：

- 两个以上独立实现需要替换同一稳定方法 assembly Interface；
- 某类对象语义需要被 `rpc/methods` 之外的生产 Module 直接消费，并且已有小而稳定
  的 Interface；
- `catalog`、method assembly 和对象语义出现不同的变更节奏与知识所有权，且拆分后
  能减少调用方必须掌握的 Interface；
- 现有 `rpc/internal/objects` 或 `wire` 的内部 seam 已不足以保持 Locality，需要
  设计一个更深的替换 Seam。

在满足条件之前，下一步只做局部重复清理、方法 assembly 的可读性改进和验证库存更新，
不改 package path，不创建第二份 registry，不改变运行时语义。
