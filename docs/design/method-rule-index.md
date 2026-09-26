# 方法规则索引（生成投影）

状态：已实现（2026-09-26）。生成物 `docs/design/method-rules.md` 由 `TestMethodRulesMatchIndex` 与代码逐字节锁定。本节仍是设计记录，不是第二份规则。
关联：`method-verification-pipeline.md` 第 6 节、`docs/decisions.md` 0040、`rpc/policy.go`、`rpc/methods/catalog.go`

---

## 1. 要解决的问题

方法规则的验收是集中的，规则的索引是没有的。

`rpc/methods` 的测试按规则维度组织：归一化与锚定在 `methods_test.go`，内容绑定在 `content_compat_test.go`，pending 在 `pending_test.go`，EIP-1898 在 `eip1898_test.go`，revert data 在 `eth_call_revert_test.go`。这种组织适合验收一条规则，不适合回答"一个方法的规则是什么"。一个方法的规则散在多个文件里，没有任何一处按方法名给出全貌。

`catalog.go` 的 `MethodDescriptor` 是唯一按方法逐条登记的结构，但它只登记到 Policy 函数名（`Policy: "CallPolicy"`），函数内部的构成不可见。

这是可见性问题，不是验证问题。验证已经存在并且组织良好。缺的是导航。

## 2. 为什么用生成的表，而不是目录或手写文档

目录结构只能表达归属，表达不了跨文件的关系。把测试按方法重新分目录，会打散现在按规则维度集中的验收，并制造第二份规则断言。这两件事都和决策 0040 冲突。

手写文档会立即成为第二事实源：代码改了文档没改时，无法判断以谁为准。

所以索引必须是代码的只读投影。人读它，不写它。它和 `expect.json` 地位相同，差别只在 `expect.json` 投影的是回放结果，这张表投影的是规则构成。

## 3. 数据从哪来

不解析 Go 源码。`*Policy` 的构成已经是声明式的：

```go
PendingLocal(1)(EIP1898BlockRef(anchor, 1)(rpc.WithAnchor(anchor, 1)(inner)))
```

缺的是这些装饰器没有把"我做了什么"留在可查询的地方。

做法：给 `rpc.Policy` 体系加一个只用于内省的可选接口，返回锚定方式、区块引用形态、pending 处理、resolver 类型、比较键种类。每个装饰器实现它，填上自己那一层并委托内层；最内层的 resolver 填自己的类型。测试遍历 `methods.Catalog()`，对每个 `RegisteredMethod` 构造其 Policy 并内省。

这样事实源就是构造代码本身。改了装饰器顺序或换了 resolver，内省结果自动跟随，不存在单独维护的一份记录。

选择可选接口而不是 `Policy` 的必选方法，是因为 `rpc.Policy` 目前全是可选能力的组合（`BlockRefProvider`、`LocalObservationPolicy`、`AgreementPolicy`、`CorroboratingPolicy` 等）。内省沿用这个模式，不强迫 `ForwardPolicy` 之类与方法规则无关的实现去填数据。未实现该接口的注册方法不得记为"未声明"，也不得静默跳过；遍历测试失败并点名方法与具体类型（第 8 节）。

## 4. 表的列与边界

一方法一行，只含能从构造中读出的列：

| 列 | 取值示例 | 来源 |
|---|---|---|
| 方法 | `eth_call` | `Catalog()` |
| 锚定 | `hash@1`、`height@1`、`height@0.fromBlock,toBlock`、`none` | `WithAnchor`、`WithAnchorHeight`、`WithAnchorHeightFields` |
| 区块引用形态 | `eip1898`、`none` | `EIP1898BlockRef` |
| pending 处理 | `local-observation`、`local-reject`、`none` | 拥有该语义的层，不是装饰器的有无 |
| resolver | `strict`、`call-revert`、`median`、`local` | 最内层 Policy |
| 比较键种类 | `quantity`、`object`、`method-specific` | resolver |

`pending 处理`只取下面三种。取值由拥有该语义的层填写，不由“链上有没有某个装饰器”推断。同一个词不能同时表示放行和拒绝。

- `local-observation`：pending 作为节点本地观察，Selected 封顶，永不 Corroborated。`PendingLocal` 只表达这一种语义。
- `local-reject`：pending 本地拒绝，返回 `ErrInvalidRequest`，零上游、不触 anchor。`RejectPending` 与 `eth_getLogs` 的请求校验是这种语义，它们不是 `PendingLocal`。
- `none`：该位置不存在 pending 形态。

比较键这一列只标种类，不展开内部分类。`eth_call` revert data 的五种状态、`normalizeCallOutcome` 对 code 与 message 的判断，是普通 Go 控制流，没有可提取的结构。这些不进入表。

表的职责是说明规则落在哪里，不是把规则重写一遍。它把读者导航到 `CallPolicy`，`CallPolicy` 再导航到 `normalizeCallOutcome`。显化到此为止。

## 5. 如何锁定

生成与比对放在 `rpc/methods` 的一个测试中，与 Policy 同包。

- 测试收集内省结果，渲染为 `docs/design/method-rules.md`，与已提交版本逐字节比对。
- 不一致则失败，并打印期望与实际的差异。
- 更新必须显式触发（环境变量），测试不得默认改写文件。
- 生成文件顶部写明"由该测试生成，勿手动编辑"。该文件人只读。

手改这个文件即违反本方案。一旦允许手改，它就变成决策 0040 所防止的第二事实源。

## 6. 不解决的事

- 比较键的内部分类细节。
- 根包的文件数量。那是物理布局问题，与本索引的可见性问题分开处理。先有索引，目录是否搬迁可以之后单独判断。
- 不新增任何裁决行为。内省接口只报告构成，不参与 `Prepare`、`Normalize`、`Resolve`。

## 7. 接口选择：可选接口，测试强制完备

内省是关于行为的元数据，不是行为。`rpc.Policy` 只承载行为，能力一律以可选接口附加（`BlockRefProvider`、`LocalObservationPolicy`、`AgreementPolicy`、`CorroboratingPolicy`）。内省沿用此模式，不进入 `Policy` 本体。

不采用必选方法。必选会迫使 `ForwardPolicy`、`SelectedPolicy`、`FinalizedHead` 等不参与方法规则的实现为索引表态，并把一个可能演化的可见性需求焊死在核心接口上。此后调整表的列就成为对全部实现的破坏性改动。一个辅助需求不应对核心接口拥有否决权。

不采用无约束的可选。可选的失败是静默的：新装饰器忘了实现内省，表只是少一层，且与"该层本来就没有此属性"无法区分。这种不完整不在 CI 中爆发，因此永远排不进修复优先级，索引会退化为不可信的文档。

取舍：完备性的强制点放在测试里，而不是类型系统里。遍历 `Catalog()` 的每个 `RegisteredMethod`，构造其 Policy，沿装饰器链逐层内省；任何一层未实现内省接口，测试失败，并指明是哪个方法的哪一层。非注册 Policy 不受此约束。

由此完备性与必选等价，而接口代价从未产生。方法数量增长时，必选的成本线性累加到每个新类型上；此方案的约束集中在一处，成本恒定。

## 8. 两条不可放宽的约束

1. 生成的 `docs/design/method-rules.md` 人只读，不得手改。手改即使其成为第二事实源，违反决策 0040。
2. 第 7 节的遍历测试不得为任何注册方法开"允许未声明"的例外。一旦放行，静默缺层的口子重新打开，索引的可信度不可恢复。

## 9. 实现顺序

先只让 `eth_call` 一条链（`PendingLocal`、`EIP1898BlockRef`、`WithAnchor`、`NewCallRevertPolicy`）实现内省并生成表的一行，验证机制。确认列的形状后再铺到其余注册方法。比较键内部分类不进表；若要求表包含该类细节，本方案不成立，因为那是普通 Go 控制流，没有可提取的结构。
