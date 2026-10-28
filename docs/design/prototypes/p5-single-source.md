# P5 纸上原型：单源执行路径（local / single-source）

> 类别：local single-source resolver（`eth_peerCount`、`eth_syncing`、`net_listening`）
> 与 intentional single-source 转发（Forwarded 模式）。验证 `SingleSource` 不进 Spec。

## 现链（生产事实）

```go
// eth_peerCount / eth_syncing
rpc.Local(wire.NormalizeWireQuantity, rpc.WithLocalRuleCompareKind(rpc.RuleCompareQuantity))
// net_listening / net_version 等
rpc.Strict(...)  // net_version 是 strict；listening 是 local
```

`localFunc`：`ResolveLocal` 不存在——local 策略本身实现 `Resolve`（首源即选，
Selected 封顶，永不 Corroborated）；pending 的 local 观测路径由
`LocalObservationPolicy`（PendingLocal 装饰）承担，二者不同。

Forwarded 模式（单上游 client）：corroboration snapshot Mode=Forwarded ⇒ aggregator
走 `pool.Forward` + `decision.Status = Forwarded` 覆写；`isPendingLocalRoute` 在
Forwarded 下不路由 pending-local（pending 形状仍转发）。

## Spec 形态（冻结候选）

```go
// eth_peerCount
MethodSpec{
    Method:    "eth_peerCount",
    Resolver:  ResolverSpec{Kind: ResolverLocal, CompareKind: RuleCompareQuantity},
    Normalize: wire.NormalizeWireQuantity,
    KeySource: KeySourceNone,   // local 不聚类，无 key source 列（现链即空）
    ObservationProjectorID: "", // 无 projector
}
```

`Compile` 发射：直接 `rpc.Local(normalize, compareOpts...)`，无装饰层。

## 单源执行路径的派生（原型核心结论）

`SingleSource` **不是 Spec 字段**（§5.1 注释的待验证项，此处定案）：

- 执行模式由**运行时 corroboration snapshot**（Mode=Forwarded）与 pool 的
  Forward 通道决定，是 invocation 属性，不是方法属性；
- 需要显式单源转发的方法（`SingleSourcePolicy`，如 ethadapter 的部分直通面）
  由 ResolverSpec.Kind 或显式执行族声明派生，不由 Spec 字段表达；
- Selected/Forwarded 语义保持现状：local 封顶 Selected；Forwarded 是刻意的
  单源转发标注，由 aggregator 在 Forwarded snapshot 下覆写 Status。

方法矩阵中 `eth_peerCount = L`（local）与 `net_listening = L` 均由
`ResolverSpec.Kind = local` 表达；`eth_sendRawTransaction = W` 属 Write 族，
不在 Read Spec scope（§6.2 冻结为 stateless read only）。

## 能力提升

- `ResolverLocal` ⇒ 无 corroborated 提升（包 Local 不得伪装 corroborated）；
  无 `AgreementPolicy`；无 `AdmissionPolicy`。
- `LocalObservationPolicy`（pending-local）与 local resolver 是两个正交能力：
  P5 确认 Spec 只表达前者为 `Pending` 列（本方法无 pending 形状），后者是
  Resolver.Kind=local 本身。

## MethodRule 投影对照

| 列 | 现链 | 编译投影 | 一致 |
| --- | --- | --- | --- |
| Resolver | `local` | spec.Resolver.Kind | ✅ |
| CompareKind | `quantity` | spec.Resolver.CompareKind | ✅ |
| KeySource | （空 → none） | local 无 key source | ✅ |
| Result | （空） | local 不声明 | ✅ |

## 测试映射（不改即通过）

- `local_shape_test.go`：local resolver 的 Selected 封顶与首源选择。
- `rpc/local_test.go`：local 语义。
- `ethadapter` 的单上游 Forwarded 路径（state_ref_wire_test 等）：
  Forwarded 覆写不变。
- `pool/voting_test.go`：Forward 通道。

## 结论

P5 通过：单源执行是 invocation/执行族属性，`SingleSource` 不进 Spec 字段集；
local resolver 与 pending-local 观测的正交性在 Spec 中由 Resolver.Kind 与
Pending 列分别表达。
