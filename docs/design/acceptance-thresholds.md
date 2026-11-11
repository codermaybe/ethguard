# Acceptance & Reference Threshold Semantics（门槛语义表）

> 状态：现行算法语义表。本文记录业务裁决与 reference 建立的分母、下限、
> 最小成员数和失败语义，不授权合并公式或改变行为。

## 1. 两个公式

### 1.1 业务轮 admission —— `rpc`（acceptanceCalculator，decision 0028/0029）

| 维度 | 语义 |
| --- | --- |
| 公式 | `Q = max(H, ⌊n/2⌋ + 1)` |
| 分母 `n` | resolve 收到的 observation 数 = 整个投票基：每个成员占位，包括被调度门跳过（gated）与已失败的成员。分母不因失败收缩。 |
| floor `H` | 调用方配置的完整性下限（client 默认 H=2；Forwarded 模式 H=0 且不裁决）。 |
| 空集 | `n<=0 → Q=1`（历史契约；空集实际以 ErrNoValidResponse 失败）。 |
| 单一支持者 | 即便 floor 允许，也降级 Selected（Corroborated 声明需要 ≥2 支持者）。 |
| Unanimity | 同一 Q 之上附加「所有 admissible 答案一致」。 |
| 失败/未尝试 | 计入分母；不影响 Q。 |

### 1.2 Reference 建立轮 finalized —— `internal/reference`（selectCandidate）

| 维度 | 语义 |
| --- | --- |
| 公式 | `required = max(2, quorum, ⌊|E|/2⌋ + 1)`（quorum>0 时）；`quorum=0 → required = |fresh|` |
| 第一轮分母 `|E|` | 第一轮 finalized-tag 读取的全部 emissions（含失败与不可解码）。多数界以它为分母：**失败计入多数界，不使 Q 缩小**。 |
| `fresh` | 通过 lower-median 滞后过滤（`maxLag`）后的可解码视图。失败与滞后视图使 `fresh` 缩小；`len(fresh) < required` 时失败。 |
| 最小成员数 | 硬下限 2：`required < 2` 或 `|fresh| < 2` 都失败。 |
| 候选高度 | quorum>0 取 `heights[len(fresh)-required]`（第 required 高）；否则取最低 fresh 高度。候选高度出现 ≥2 个不同 hash → ErrConflict（fleet dissent，先于确认轮失败）。 |
| 确认轮 | 支持不足时向缺失成员在候选高度发起确认；hash 与候选集合不一致 → reorg 证据。分母仍是第一轮冻结的成员集；确认只能补位，不能缩基。 |
| resolve 后置检查 | `len(decision.Support) >= required && Status == Corroborated`，使用的 `required` 与 selectCandidate 冻结值同源（`sel.required`）——它就是同一计算值的断言，不是第二个公式。 |

### 1.3 Reference 建立轮 latest/safe —— strict head（anchorHeadPolicy）

latest/safe 的建立与业务轮共用 strict 解析：存在 corroboration snapshot 时走
decision-0028 裁决（同 §1.1），否则走 legacy 全体一致 + 单源降级 Selected；
外层 `requireCorroborated` 门把任何非 Corroborated 结果判失败。

## 2. 行为锁定

- `rpc/acceptance_thresholds_test.go`：§1.1 的表驱动测试（含空集、floor 压过多数、
  单支持者降级、unanimity 拒绝分裂）。
- `internal/reference/finalized_thresholds_test.go`：§1.2 的表驱动测试
  （失败计入多数界、fresh 收缩导致失败、硬下限 2、quorum=0 行为、候选高度选择）。

## 3. 为什么保留两套算法

**保留两套显式算法，不合并。** 理由：

1. 分母语义不同：admission 的分母是「投票基（业务轮成员）」；finalized 建立的
   多数界分母是「第一轮 emissions」，而支持下限的候选池是「滞后过滤后的 fresh」。
   两者的失败处理（计入分母 vs 使 fresh 收缩）方向相反，统一公式要么改变业务轮
   行为，要么改变建立轮行为。
2. 建立共同参考包含候选、确认轮与 reorg 证据，业务轮是单次裁决；不能只因公式相似便共用算法。
3. 更换实现必须证明现有 Strict／Finalized 输出和错误等价；是否统一应另行评审，不能由结构迁移顺手改变。

后置检查仍使用同一冻结的 `sel.required`，不是第二次裁决。未决的新鲜度范围与样本规则不属于本文合同，历史轨道编号和迁移施工条件不再作为现行规则。
