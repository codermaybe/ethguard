# P1 纸上原型：`eth_getBalance`

> 类别：纯 fan-out + encoder/normalizer。代表 15 个纯数据方法
> （getBalance/getTransactionCount/getStorageAt/getCode/estimateGas/createAccessList/
> chainId/gasPrice/maxPriorityFeePerGas/blobBaseFee/blockNumber/…）。

## 现链（生产事实）

```go
PendingLocal(1)(EIP1898BlockRef(anchor, 1)(rpc.Anchored(1, wire.NormalizeWireQuantity, anchor,
    compareOpts(rpc.RuleCompareQuantity, append([]rpc.StrictOption{rpc.WithCanonicalValue(wire.EncodeQuantity)}, opts)...))))
```

外→内：`pendingLocalPolicy` → `eip1898Policy` → `anchoredPolicy` → `strictFunc`。
能力：strictFunc 暴露 `CorroboratingPolicy`、`OutcomeResolver`、
`CorroboratingOutcomeResolver`、`AgreementPolicy`、`CanonicalValue`；三个装饰层经
孪生包装保留 corroborated 能力，经 `Decorator` 保留其余。

## Spec 形态（冻结候选）

```go
MethodSpec{
    Method:    "eth_getBalance",
    Resolver:  ResolverSpec{Kind: ResolverStrict, CompareKind: RuleCompareQuantity},
    Normalize: wire.NormalizeWireQuantity,
    Encode:    wire.EncodeQuantity,          // KeySource = canonical-bytes
    KeySource: KeySourceCanonicalBytes,
    Anchor:    &AnchorSpec{Kind: AnchorHash, Positions: []int{1}},
    EIP1898:   &EIP1898Spec{Pos: 1},         // 字段集评审点：EIP1898 并入 AnchorSpec 还是独立字段
    Pending:   PendingLocalAt(1),
    ObservationProjectorID: "scalar-standard",
}
```

`Compile` 发射（顺序固定）：`PendingLocal → EIP1898BlockRef → WithAnchor → Strict`。
与现链逐层相同，因此行为等价性不依赖任何新语义。

## 能力提升

- `ResolverStrict` ⇒ 编译器给整链挂 corroborated 转发（现状 = 三个孪生包装类型）。
  编译后孪生类型消失，能力来自编译器构造的同一个可 corroborated 的组合类型；
  `rpc/corroboration_composition_test.go`（包 Median 不得凭空发明能力）必须原样通过。
- `AgreementPolicy.RequiresAgreement` = true（strict 固有）。

## MethodRule 投影对照

| 列 | 现链 | 编译投影 | 一致 |
| --- | --- | --- | --- |
| Anchor | `hash@1` | spec.Anchor → `hash@1` | ✅ |
| BlockRef | `eip1898` | spec.EIP1898 → `eip1898` | ✅ |
| Pending | `local-observation` | spec.Pending → `local-observation` | ✅ |
| Resolver | `strict` | spec.Resolver.Kind | ✅ |
| CompareKind | `quantity` | spec.Resolver.CompareKind | ✅ |
| KeySource | `canonical-bytes` | spec.Encode != nil → `canonical-bytes` | ✅ |
| Result | `canonical` | Encode 非 nil → `canonical` | ✅ |

`method_rules_test.go` 的逐层遍历断言要求 `MethodRuleSource` 链存在：编译器必须为
每个发射的装饰层保留一个 source 节点（或先迁移该测试为行为级等价索引——决策点记入
M0）。

## Projector / typed 所有权

`ObservationProjectorID: "scalar-standard"`；runtime 的 projector registry 绑定
`projection.ScalarStandardProjector()`。Spec 不 import projection/typed。

## 测试映射（不改即通过）

- `admission_suite_test.go`：getBalance 在 canonical-value 域内 —— spec 的
  Encode/Normalize 声明直接满足。
- `moving_tag_matrix_test.go`：anchor×pending 矩阵 —— 编译链与现链同构。
- `canonical_key_source_test.go`：KeySource 三态 —— spec.KeySource = canonical-bytes。
- `pending_test.go`：pending 局部观测 —— PendingLocalAt(1) 同位。

## 结论

P1 通过：纯数据方法的 Spec 字段集为
`Method/Resolver/Normalize/Encode/KeySource/Anchor(+EIP1898)/Pending/ObservationProjectorID`，
无逃生口需求。
