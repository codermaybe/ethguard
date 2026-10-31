# P4 纸上原型：`eth_getBlockByNumber`（header-only 双形态）

> 类别：ResultForm/representative 双形态 + HeadCodec 复用。三重身份：
> 业务读、anchor comparator、head decoder；header-only 值保源拼写、full 值 canonical；
> `byHash` 影响 admission。

## 现链（生产事实）

```go
PendingLocal(0)(blockPolicy{Decorator: rpc.Decorator{Policy: rpc.Strict(normalizeBlockIdentity,
    compareOpts(RuleCompareMethodSpecific, WithCanonicalObject(blockobject.EncodeByNumber), opts...))}, byHash: false})
```

- `blockPolicy`（byHash 区分 by-number/by-hash）自带：`RequiresAgreement`、
  `ResolveCorroborated` + `ResolveOutcome`（S2：restore 后返回）、`Admit`
  （full-only：`AdmitBlock`；header-only no-op）、`restoreHeaderOnlyValue`
  （Corroborated + header-only 响应时 Value 保 representative 源拼写）。
- `KeySource`：WithCanonicalObject ⇒ KeySource=`normalizer`、Result=`canonical`
  （key 是 number|hash，窄于值）。

## Spec 形态（冻结候选）

```go
MethodSpec{
    Method:    "eth_getBlockByNumber",
    Resolver:  ResolverSpec{Kind: ResolverStrict, CompareKind: RuleCompareMethodSpecific},
    Normalize: normalizeBlockIdentity,
    Encode:    blockobject.EncodeByNumber,   // CanonicalObject 语义：EncodeCheck 进域、key 留 normalizer
    KeySource: KeySourceNormalizer,           // 窄 key：显式声明而非 Encode 推导
    Anchor:    &AnchorSpec{Kind: AnchorHash, Positions: []int{0}}, // anchor 角色由 runtime 输入决定（见下）
    Pending:   PendingLocalAt(0),
    Result:    ResultRepresentative,          // header-only 保源拼写 / full 保 canonical
    Admit:     admitFullBlockOnly,            // byHash/full 形态参数化
    ObservationProjectorID: "block",
}
```

`Result: ResultRepresentative` 是新列：编译器发射 `blockPolicy.restoreHeaderOnlyValue`
的等价后处理（representative 源拼写恢复）。这一列的冻结决策点：**representative 恢复
是 ResultForm 的属性，不是装饰器** —— 因为它作用于「决策值如何拼写」，不作用于
请求准备。

## HeadCodec 复用（提案 §4.1.2 的核心）

HeadCodec 已在 R0 落地为 `reference.HeadCodec` 接口 + `runtime.MethodHeadCodec`
生产绑定（验证与比较键 = `normalizeBlockIdentity`，高度/哈希提取归 codec）。
本原型验证的等价关系：

- **reference head 解码不依赖编译后 Policy**：`runtime.MethodHeadCodec` 直接绑定
  方法的 identity 函数（`methods.NormalizeBlockHead`），不经装饰链，也不临时
  构造 legacy policy。
  因此 MethodSpec/Compile 重排或扁平化装饰链，reference 解码不受影响
  （`internal/runtime/pool_head_reader_test.go` 的 codec 契约测试钉住）。
- **比较键共享**：business 聚类键（number|hash）与 reference 聚类键是同一个
  identity 函数 —— spec.Normalize 即两者之源，双消费不产生第二真相。

## 能力提升

- `ResolverStrict` ⇒ corroborated 转发；`blockPolicy` 的
  `ResolveCorroborated`/`ResolveOutcome` 转发 + restore 由编译器统一（现手工）。
- byHash 差异：admit 形态参数（full-only 检查中 `req.Params[1] == true` 与
  byHash 传入 `AdmitBlock`）—— 属 Admit 钩子的闭包参数，不是 Spec 新字段。

## MethodRule 投影对照

| 列 | 现链 | 编译投影 | 一致 |
| --- | --- | --- | --- |
| Anchor | `hash@0`（仅当按 anchor 角色装配） | spec.Anchor（runtime 可覆盖为 none —— anchor 角色由装配点决定） | ✅ |
| BlockRef | （空） | spec 无 EIP1898 | ✅ |
| Pending | `local-observation` | spec.Pending | ✅ |
| Resolver | `strict` | spec.Resolver.Kind | ✅ |
| CompareKind | `method-specific` | spec.Resolver.CompareKind | ✅ |
| KeySource | `normalizer` | spec.KeySource 显式 = normalizer（WithCanonicalObject 语义） | ✅ |
| Result | `canonical` | spec.Result=representative ⇒ Result 列仍为 `canonical`（值域 canonical，拼写 representative） | ✅ |

KeySource 的三态对照（`canonical_key_source_test.go`）：WithCanonicalValue ⇒
canonical-bytes；WithCanonicalObject ⇒ normalizer + canonical result；无 encoder ⇒
normalizer + raw。Spec 用 `KeySource` 显式列表达，编译器不再从 Encode 字段推导 ——
**这是 P4 对字段集的关键修正**：KeySource 是声明，不是推导。

## 测试映射（不改即通过）

- `eth_getblockbynumber_test.go` / `block_encoding_test.go`：header-only 源拼写、
  full canonical 提交。
- `representative_test.go`：Support/Reject + representative 语义。
- `internal/runtime/pool_head_reader_test.go`：HeadCodec 复用与 reference 独立性。
- `internal/verification/integration/reference_test.go`：anchor comparator 语义
  （finalized 候选选择）。
- `moving_tag_matrix_test.go`：anchor 角色。

## 结论

P4 通过：`Result` 列加入字段集（canonical | source-header-only | representative），
KeySource 由显式声明表达；HeadCodec 复用成立 —— reference 解码与编译器解耦。
