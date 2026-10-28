# P2 纸上原型：`eth_getLogs`

> 类别：Validate-before-Reference 钩子 + 字段级高度锚定 + 响应 admission。
> 附带演练（可选第六类）`eth_feeHistory` 的三参数校验 + range admission。

## 现链（生产事实）

```go
logsPolicy{Policy: rpc.WithAnchorHeightFields(height, 0, "fromBlock", "toBlock")(
    rpc.Strict(normalizeLogs, compareOpts(RuleCompareMethodSpecific,
        WithCanonicalValue(log.Encode), opts...)))}
```

`logsPolicy` 自带：`Prepare`（pending/malformed bound 先于锚定拒绝，ErrInvalidRequest）、
`Admit`（响应 schema 校验）、`ResolveCorroborated`（转发/回退）、`RequiresAgreement`、
`Normalize`（normalizeLogs）、`MethodRule`（Pending: local-reject）。

三个「必须先于锚定」的钩子次序：
1. `validateLogsRequest`（pending/malformed bound 拒绝）→ 2. `WithAnchorHeightFields`
（字段高度改写/缺省，blockHash 互斥检查）→ 3. fan-out 后 `Admit`（schema）。

## Spec 形态（冻结候选）

```go
MethodSpec{
    Method:    "eth_getLogs",
    Resolver:  ResolverSpec{Kind: ResolverStrict, CompareKind: RuleCompareMethodSpecific},
    Normalize: normalizeLogs,
    Encode:    log.Encode,                   // KeySource = canonical-bytes
    KeySource: KeySourceCanonicalBytes,
    Validate:  validateLogsRequest,          // v1 钩子：先于 anchor
    Anchor:    &AnchorSpec{Kind: AnchorHeightFields, ParamPos: 0, Fields: []string{"fromBlock", "toBlock"}},
    Pending:   PendingRejectAt(0),           // logsPolicy 的 local-reject 语义
    Admit:     admitLogsResponse,            // v1 钩子：collect 后
    ObservationProjectorID: "logs",
}
```

`Compile` 发射（顺序固定，验证了「Validate → Anchor → … → Admit → Resolve」）：
`logsValidate(Validate) → WithAnchorHeightFields → Strict`，`Admit` 由编译器包成
`WithAdmission`（rpc/internal/policy）。`pendingRejectingPolicy` 的 local-reject 由
spec.Pending 投影 —— 注意 logsPolicy 的拒绝逻辑在 Prepare 内与锚定耦合（先拒绝再锚定），
编译器发射顺序必须保持 Validate 先于 Anchor（这正是 P2 要证明的钩子必要性）。

## 能力提升

- `ResolverStrict` ⇒ corroborated 转发。现 logsPolicy.ResolveCorroborated 手工转发，
  编译后由统一组合类型承担。
- `RequiresAgreement` = true。
- `AdmissionPolicy`：由 spec.Admit 发射（等价于 `policy.WithAdmission` 包装，
  该包装已存在并经 admission 测试验证）。

## MethodRule 投影对照

| 列 | 现链 | 编译投影 | 一致 |
| --- | --- | --- | --- |
| Anchor | `height@0.fromBlock,toBlock` | AnchorSpec → 同串 | ✅ |
| BlockRef | （空 → none） | spec 无 EIP1898 | ✅ |
| Pending | `local-reject` | spec.Pending | ✅ |
| Resolver | `strict` | spec.Resolver.Kind | ✅ |
| CompareKind | `method-specific` | spec.Resolver.CompareKind | ✅ |
| KeySource | `canonical-bytes` | Encode 非 nil | ✅ |
| Result | `canonical` | Encode 非 nil | ✅ |

## Projector / typed 所有权

`ObservationProjectorID: "logs"`；runtime 绑定 `projection.NewSchemaProjector("eth_getLogs")`。

## 测试映射（不改即通过）

- `eth_getlogs_test.go`：pending bound 拒绝、blockHash 互斥、缺省字段改写 —— Validate
  与 Anchor 的次序由编译器固定为现序。
- `moving_tag_matrix_test.go`：fields 锚定维度。
- `admission_suite_test.go` / `block_admission_test.go`（logs schema 用例）：Admit 钩子。
- `representative_test.go`：Support/Reject 组合。

## feeHistory 附带演练

`Validate`（三参数校验 validateFeeHistoryRequest）+ `Anchor: height@1` +
`Admit`（range admission validateFeeHistoryRange）+ PendingLocal(1)。同一字段集
覆盖 —— 无需扩 Spec。这证明字段集对「validate/anchor/admit 三钩子 + 高度锚定 +
局部 pending」的组合是封闭的。

## 结论

P2 通过：v1 钩子集合 = `Validate`（先于 anchor）、`Admit`（collect 后、含
`AdmissionCaller` 依赖）恰好两个；字段高度锚定用 `AnchorSpec.Kind=height-fields`。
无 Custom 需求。
