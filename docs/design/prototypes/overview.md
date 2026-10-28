# 五类纸上原型总览（MethodSpec 门禁记录）

> 状态：已完成（轨道 P，2026-10-29）。本文与 `p1-getbalance.md`…`p5-single-source.md`
> 是 §5.4 的 5 类纸上原型。**只包含草图与等价性说明，没有生产编译器代码**；
> `MethodSpec` 字段集在门禁通过后冻结（R5），M0 才允许开写 `Compile`。

## 门禁检查表（§5.4）

| 门 | 结果 | 证据 |
| --- | --- | --- |
| 现有矩阵测试不改即通过 | ✅ | 原型零生产代码；`go test ./...` 在 P 提交点保持全绿 |
| 能力提升由 `ResolverSpec.Kind` 决定 | ✅ | 每份原型的「能力提升」节逐一核对：包 Median 无 `CorroboratingPolicy`、包 Strict 转发 snapshot（`rpc/corroboration_composition_test.go` 是回归网） |
| `MethodRule` 投影等价 | ✅ | 每份原型给出编译后 `MethodRuleSource` 链与现链逐列对照 |
| ObservationProjector 所有权 | ✅ | 全部原型用 `ObservationProjectorID` + runtime 绑定；factory 不进 Spec |
| Custom 边界 | ✅ | P3 论证 `eth_call` 的 CallRevert 保留为受控逃生口；其余 4 类无需 Custom |
| 单源执行路径（P5） | ✅ | `SingleSource` 由 ResolverKind 派生，**不是** Spec 字段（§5.1 注释的原型结论） |

## 编译器契约草图（供 M0 复核，非冻结）

```go
// Compile 的唯一职责：把 spec 字段一次性装配为与今天等价的装饰器链。
// 顺序固定：Validate → Anchor/EIP-1898 → Pending → Collect(选择) → Admit → Resolve → ResultForm。
// 能力提升只看 ResolverSpec.Kind：
//   strict → CorroboratingPolicy + CorroboratingOutcomeResolver（转发 snapshot）
//   median/local/call-revert → 不晋升；外层装饰用基础 Decorator（不含 ResolveCorroborated）
// MethodRule 由 spec 纯投影，保持 MethodRuleSource 链（每层一个 source 节点）。
func Compile(spec MethodSpec, deps Deps) (rpc.Policy, rpc.MethodRule)
```

`Deps` 只带静态绑定：`AnchorHash`/`AnchorHeight`（R4 后替换为 ReferenceStage 注入的
established-reference 读取器）与 projector registry 的解析入口。`AdmissionCaller`
不在 `Deps`：它依赖 invocation/source，只能由 aggregator 在执行期注入。

## 原型索引

| 原型 | 方法 | 覆盖的结构性异常 |
| --- | --- | --- |
| P1 | `eth_getBalance` | 纯 fan-out + encoder/normalizer（15 个纯数据方法的代表） |
| P2 | `eth_getLogs` | Validate-before-Reference 钩子 + 字段级高度锚定 + 响应 admission |
| P3 | `eth_call` | CallRevert 逃生口 + 双维 resolver + RPCErrorBodies + provenance |
| P4 | `eth_getBlockByNumber` | header-only ResultForm/representative 双形态 + HeadCodec 复用 |
| P5 | `eth_peerCount`（local） | 单源执行路径、Selected/Forwarded 语义 |

`eth_feeHistory`（可选第六类）的 range admission 与三参数校验在 P2 中附带给出演练。
