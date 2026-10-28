# P3 纸上原型：`eth_call`

> 类别：受控逃生口（Custom/CallRevert）。双维 resolver（execution + revert data）、
> `RPCErrorBodies`、`RevertContext`、`Dimensions`、`DeletedFields`、`Provenance`。

## 现链（生产事实）

```go
inner := rpc.NewCallRevertPolicy(normalizeCallOutcome,
    WithRPCErrorBodies(), WithCanonicalValue(encodeCallSuccess), opts...)
return PendingLocal(1)(EIP1898BlockRef(anchor, 1)(rpc.WithAnchor(anchor, 1)(inner)))
```

`callRevertFunc` 是自足 resolver：多键聚类（ok|data / revert|state:selector）、
协议错误体进入值域（`IncludeRPCError`）、4e 规则的 reductive 投影（删字段 + 保存
被删字节到 Provenance）、`Dimensions`（execution/revert-data 的 Support/Reject/Abstain）、
`RevertContext`（Code/Message 与 Value 分离）。

## Spec 形态（冻结候选：Custom 逃生口）

```go
MethodSpec{
    Method:   "eth_call",
    Resolver: ResolverSpec{Kind: ResolverCallRevert}, // 编译器绑定 callRevertFunc 构造器
    Anchor:   &AnchorSpec{Kind: AnchorHash, Positions: []int{1}},
    EIP1898:  &EIP1898Spec{Pos: 1},
    Pending:  PendingLocalAt(1),
    // 双维、revert envelope、provenance、4e 全部内嵌在 CallRevert resolver 构造器中；
    // Spec 不为它们扩字段。
    ObservationProjectorID: "scalar-standard",
}
```

`Compile` 发射：`PendingLocal → EIP1898BlockRef → WithAnchor → NewCallRevertPolicy(...)`。
ResolverSpec.Kind=call-revert 的构造器是**受控逃生口**：每个 Custom 必须有
（a）理由：多键值域与 reductive 投影无法用单一 (Normalize, Encode, KeySource) 表达；
（b）负责人：rpc 包维护者；（c）测试：`eth_call_revert_test.go`、`call_revert_4e_test.go`、
`exec_object_test.go`；（d）退出条件：若未来值域简化（例如 4e 删除），可迁移回
Spec 字段。逃生口注册表 = ResolverSpec.Kind 枚举本身（不开放任意函数字段，防止
第二套方法模型）。

## 能力提升

- `callRevertFunc` **本身实现** `CorroboratingPolicy`（转发 snapshot 到自身的多维
  裁决）；S2 已核实它未实现 Outcome 能力 —— fallback 路径语义等价，Outcome 化
  列为 M-track 的后续项（需先钉住 call-revert 的 verdict 语义，提案 §11 已列）。
- `RPCErrorPolicy.IncludeRPCError` = true（构造器固定）。
- 编译器对 call-revert 不再叠加 corroborated 包装（resolver 自带），与现状一致。

## MethodRule 投影对照

| 列 | 现链 | 编译投影 | 一致 |
| --- | --- | --- | --- |
| Anchor | `hash@1` | spec.Anchor | ✅ |
| BlockRef | `eip1898` | spec.EIP1898 | ✅ |
| Pending | `local-observation` | spec.Pending | ✅ |
| Resolver | `call-revert` | spec.Resolver.Kind | ✅ |
| CompareKind | `method-specific` | call-revert 构造器声明（Spec 不记） | ✅ |
| KeySource | （空 → none） | call-revert 无单一 key source（多键） | ✅ |
| Result | （空） | call-revert 不声明 | ✅ |

## Outcome 语义（S2 的补充证据）

观测层对 call-revert 的 fallback 分类（`usableAnswer`：错误体被 policy 接受 ⇒ 可用）
与 P3 的期望一致：revert envelope 进入聚类 = 支持或异议；不接受的错误 = unusable。
迁移到 `OutcomeResolver` 时必须保持：revert 维度的 Abstain 不是 Reject —— 记为
M0 的等价性说明必备项。

## Projector / typed 所有权

`ObservationProjectorID: "scalar-standard"`（call 的扩展投影 = scalar-standard）。
`Provenance`/`DeletedFields`/`Dimensions` 是 Decision 字段，由 resolver 填充，
与 projector 无关。

## 测试映射（不改即通过）

- `eth_call_revert_test.go`（聚类与 revert envelope）、`call_revert_4e_test.go`
  （reductive 投影 + Provenance）、`exec_object_test.go`（对象参数）、
  `moving_tag_matrix_test.go`（anchor×pending）、`ethadapter/call_revert_4e_test.go`
  （typed 面）。
- 编译链逐层同构 ⇒ 行为等价不依赖新语义。

## 结论

P3 通过：`eth_call` 保留为 ResolverSpec.Kind=call-revert 的受控逃生口，字段集
不需要为它扩列；「每个 Custom 有理由/负责人/测试/退出条件」的约束可执行。
