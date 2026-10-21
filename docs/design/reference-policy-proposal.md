# Reference Policy 参考方案：选择不对称，确认对称

状态：**临时参考方案（2026-10-21）**。不是决策，不是路线图承诺，不授权实现。
目的：记录当前 tag 中心参照语义的缺口、一个候选收敛方向，以及仍需比较的替代形状。

关联：`decisions.md` 0032/0036/0037、`moving-tag-matrix.md`、
`capture-golden-contract.md` §2.1bis、`anchor.go`、`rpc/strict.go`、
`internal/reference/finalized.go`。

---

## 1. 问题

当前 docs、capture 和 replay 把 `safe`/`finalized` 讲成了参照标准，但它们
只是 **provider-reported tag**：

- 不是所有链、节点、provider 都支持；L2、测试网、旧节点和部分公网源可能
  返回 `null` 或错误；
- 不是 consensus、finality proof 或 light-client 验证；
- 语义由 provider 决定，可能滞后、分叉或不可用。

跨链真正通用的原语只有：

- block number；
- `eth_getBlockByNumber` / `eth_getBlockByHash`；
- 调用方定义的确认深度（`depth` / confirmations）。

因此需要区分两件事：

1. **Selection**：这次 reference 的目标是怎么选出来的；
2. **Confirmation**：选出的目标是否被多源确认。

当前代码把两者混在 anchor 类型里：

- `StrictHeadAnchor` 的公开无参构造是 legacy unanimity；生产客户端用
  `newStrictHeadAnchor(..., shared)`，走快照 majority/floor；
- `FinalizedHeadAnchor` 的 `safe`/`latest` 是 strict + shared majority，
  `finalized` 走 `reference.Finalized` 的 reported-finalized 候选与确认；
- `reference.Finalized` 已经包含“候选高度 + 哈希确认 + 新鲜度”的通用骨架，
  只是输入固定成了 `finalized` tag；
- 没有通用 `depth(D)` 策略。

结果：`strict` 一词同时表示单键聚类、legacy unanimity 和 majority 快照；
replay 无法从 `batch.json` 判断应该套哪种 anchor 语义。这是“取向模糊”的
根源。

---

## 2. 候选模型：一个引擎 + 两个选择参数 + pinned hash

### 2.1 Selection

| 逻辑策略 | reported tag | depth | 目标 |
| --- | --- | --- | --- |
| `head` | `latest` | 0 | 多源佐证的 latest head |
| `depth(D)` | `latest` | D | 佐证 head 后取 `H-D` |
| `safe` | `safe` | 0 | provider-reported safe |
| `finalized` | `finalized` | 0 | provider-reported finalized |
| `pinned hash` | — | — | 调用方给定 hash |

Selection 只回答“目标高度/hash 是什么”，不判断它是否安全。`reportedTag`
是 provider 报告，`depth` 是本地确认深度假设，`pinned hash` 是调用方输入。
三者的可信来源不同，这是**选择层的不对称**，必须在 evidence 中显式记录。

### 2.2 Confirmation

Confirmation 完全统一：

- 对目标高度/哈希收集所有成员，固定分母与 mode/floor；
- 在目标高度上比较 `number|hash`（或直接确认 pinned hash）；
- hash 不一致 → 冲突；支持不足 → `Unknown`；
- 不换高度、不换 tag、不剔除来源后重投、不跨调用缓存；
- 每个来源的 support/reject/fault 进入 evidence。

### 2.3 Evidence

建议把 evidence 拆成两段，而不是一个混合状态：

```text
SelectionEvidence
  kind          head | depth | tag | pinned-hash
  reported_tag  latest | safe | finalized | ""
  depth         uint64
  candidate     provider-reported height (when applicable)
  target_height uint64
  target_hash   hash (pinned or selected)

ConfirmationEvidence
  height, hash
  status        corroborated | selected | unknown
  support, reject, faults
  observed_at
```

最终 reference 只有在 Selection 与 Confirmation 都成立时才对外声明
`Corroborated`。provider 的 tag 声明与多源 hash 确认是两件事，不能用一个
状态词吞掉。

### 2.4 语义

- `head` 与 `depth(D)` 共享同一实现；`depth(0)` 就是 head。
- `safe`/`finalized` 是 `reported_tag` 的特例，`depth=0`。
- `pinned hash` 跳过选择，只做确认。
- tag 不支持时 fail closed；不为成功映射成 `latest` 或 depth。
- depth 目标不可达/不足时 fail closed；不换更老的高度重试。
- 以上都不构成 finality 声明；depth 是确认深度，tag 是 provider 报告。

---

## 3. 为什么不是其他形状

### 3.1 四个并列 policy

`head`/`depth`/`tag`/`pinned` 四个独立 policy 会把选择层的不对称原样复制成
四条代码路径、四套测试和四组 evidence。它只是把问题摊开，没有收敛。

### 3.2 只有 depth

简单、跨链，但会丢掉 mainnet 上 provider-reported finalized 这个额外信号，
而且 D 是链相关/启发式；把 depth 叫成 finality 是错误表述。

### 3.3 自动回退

tag 不支持就自动用 depth、或用 latest 补成功，会改变请求语义，违反
决策 0032/0036 的“不静默降级”。宁可 `Unknown`，不制造成功。

### 3.4 完全对称的信任

要消除 selection 层的不对称，只能引入 light client / zk / trusted checkpoint，
这不是当前产品范围。当前方案不假装输入对称，只保证确认规则对称。

---

## 4. 仍需比较的替代方案

1. **通用 `TargetSelector` 接口**：`Latest`/`Depth`/`Tag`/`PinnedHash` 四个
   selector 返回候选目标，确认引擎统一。它比“两个参数”更可扩展，但可能
   重新引入 policy 数量；需要比较概念数与测试矩阵。
2. **只保留 pinned target**：调用方提供 height/hash，库只负责多源确认；
   selector 只作为便利构造。它最对称、最可靠，但把选择责任外移。
3. **两段 evidence vs 单状态**：是否把 selection/confirmation 状态分开，
   还是在 `ReferenceEvidence.Status` 里保留一个总状态并附 detail。
4. **复用业务 aggregator**：reference 的选择/确认是否应复用普通聚合管线，
   而不是 `reference.Finalized` 的平行实现。可复用性 vs 特殊候选规则。
5. **候选规则**：当前 finalized 用“Q 个报告者覆盖的最高高度”；depth 应对
   candidate 减 D，还是对每个源高度先减 D 再选？lagging 源应 fault 还是
   reject？需要单独定义并测试。
6. **默认策略**：库是否应有默认？`head`（显式波动）/`depth(D)`（需配置）/
   强制调用方选择？默认值本身就是语义承诺。
7. **能力探测**：启动时探测 tag 支持，还是请求时 fail closed？探测会引入
   缓存/漂移，必须显式记录。
8. **命名**：`Stable`/`Reported`/`Depth`/`Head` 的命名是否比 `Finalized`
   更准确地表达“不是 finality”。

---

## 5. 对 capture/replay 的影响

当前 `batch.json` 的 `reference_context` 是 tag 专用：

```text
tag: safe | finalized
logical_request, members, reference_records, expected_reference
```

参考方案要求记录 **selection policy**，而不是从 tag 推断：

```text
reference_policy:
  kind: tag | depth | head | pinned-hash
  reported_tag: latest | safe | finalized
  depth: N
  target_height, target_hash
```

历史批次默认 `kind: tag`（safe/finalized）；组 3 批次保持不变。新增 depth
批次用于验证跨链路径。replay 按 `reference_policy` 选择 resolver，不再从
tag 或 anchor 类型猜测。组 3 的真实数据不需要重采。

---

## 6. 若升级为正式方案的验收

- 一个确认引擎；`head`/`depth` 共享实现，tag/pinned 只作为 selection 输入。
- selection evidence 与 confirmation evidence 完整，且相互独立。
- `depth(0)`、`depth>0`、`safe`、`finalized`、pinned hash 全部有测试。
- tag 不支持、depth 不可达、hash 冲突、lagging/fault 源均 fail closed。
- 不静默回退，不换高度，不缩分母，不跨调用缓存。
- 新增方法/字段不改变 reference contract；method 层只消费 height/hash。
- moving-tag matrix 与 capture 契约改述为“reference policy”，tag 只作
  可选 selection。

## 7. 非目标

- 不引入 light client、zk、trusted checkpoint 或 finality proof。
- 不新增 RPC 方法，不改变裁决门槛/分母。
- 不把 depth 或 provider tag 宣称为 consensus finality。
- 不自动回退 tag→depth/latest。
- 不在本方案中实现；代码与批次变更必须另立任务。
