# Reference Freshness（设计与不变量）

状态：**设计草案（2026-10-31），未实现新策略**。决策 0032/0033 已接受边界：
保持调用方请求的 tag 语义；freshness 与 agreement 分离；不预设固定 lag。
本文件把现状、两个 freshness 作用点和待裁决问题固定下来，不选择默认数值，
也不改变当前实现。

## 1. 两个 freshness 作用点（当前实现事实）

EthGuard 目前有两处与 freshness 相关的机制，它们不是同一个策略：

1. **调度侧 tip gate（pool）**：`WithTipTracking` 周期采样各 source 的
   block tip；`Collect` 在 applyFreshness 时跳过明显落后于 fleet tip 的
   source。被跳过的成员保留在投票分母中（决策 0062）。
2. **reference 候选 lag（finalized 算法）**：`reference.Finalized` 用
   `maxLag` 过滤落后于候选中位高度的 finalized 视图；过旧的视图被记为
   source fault，不参与候选/确认。`StrictHeadEstablishment` 的 latest/safe
   层级不使用 finalized 的 `maxLag`。

两者都是当前实现事实，不构成新的默认 freshness 承诺；具体数值、单位和
适用方法仍需专门决策。

## 2. 不变量（任何 freshness 方案必须满足）

1. **tag 语义不降级**：latest 不得静默变成 finalized；safe/finalized 不得
   降级为 latest；explicit height/hash 不得被改写。
2. **freshness 失败 = 拒绝**：不满足 freshness 时拒绝该调用或该 source，
   不得通过降低门槛、放宽 mode 或换 tag 来成功。
3. **成员/分母不变**：freshness 只影响调度或候选资格，不改变固定成员、
   投票分母或 acceptance threshold（0062）。
4. **显式历史读取豁免**：调用方显式请求旧块/旧高度时，不得因“旧”被拒；
   freshness 只对声明的移动 tag（latest/safe/finalized 等）有意义。
5. **agreement 与 freshness 独立**：多数同意不证明新鲜；一个 provider 的
   最高高度不能单独判定其他 provider 过期。
6. **provider evidence 不是共识**：freshness 判定基于 provider 观测，
   不声称 canonical finality 或轻客户端证明。
7. **可观测**：拒绝/跳过必须留下结构化原因（tip 落后、候选 lag、样本过期），
   不得只靠错误字符串。
8. **无默认常数**：在专门决策前，不新增固定的“最大 lag = N 块/秒”默认值。

## 3. 待裁决问题

### 3.1 单位与判定对象

- 以块高度、时间窗口还是两者组合判定 freshness？
- 判定对象是 source tip、reference 候选高度，还是方法声明的历史区间？
- 采样时间戳过期（tip 样本本身陈旧）如何处理：视为未知、暂停该 source，
  还是只影响调度不影响裁决？

### 3.2 方法适用范围

- 哪些方法对 freshness 有意义：latest/safe/finalized 的移动 tag 读取；
  explicit historical reads 豁免；
- pending 读取是否参与 freshness（当前 pending 是本地观测，不应被 tip gate
  改写）；
- 派生方法（blockNumber）是否复用 reference freshness，还是只复用已建立的
  reference。

### 3.3 调度与裁决的边界

- pool tip gate 是**调度**（source 不发送请求、仍占分母），reference lag 是
  **候选资格**（source fault、参与确认分母）；两者是否需要在同一决策中
  统一解释？
- 一个 source 因 tip gate 未发送请求时，诊断是否需要区分“未尝试”和
  “尝试后失败”；
- freshness 失败是否应触发 source health/cooldown（当前判断：不自动惩罚
  成员，除非属于可用性故障）。

### 3.4 参数与自适应

- 固定 lag 是否足以覆盖真实 provider 差异，还是需要自适应阈值；
- 阈值是否按 chain/方法/客户端类型区分；
- 与 T11 自适应拥塞控制的关系：freshness 是内容判定，拥塞是调度容量，
  两者共享 source 预算但不共享决策语义。

## 4. 证据计划（在实现前）

1. **真实 provider 观测**：多个 provider 在 latest/safe/finalized 上的高度
   分布、滞后分布、采样间隔与异常形态；记录日期与端点类别，不把一次观测
   写成普遍结论；2026-10-31 已完成一次 4 源、3 pass、约 15 秒间隔的短窗口
   观测：latest 最大差 1 block，safe 最大差 31 blocks，finalized 最大差
   159 blocks，未观察到 reorg；详见
   [reference-freshness-observations.md](reference-freshness-observations.md)；
2. **reorg/回退样本**：短程 reorg 下 finalized/latest 视图的变化，验证
   freshness 失败是否按拒绝处理；
3. **显式历史读取**：旧高度/旧 hash 不被 freshness 拒绝；
4. **tip 样本过期**：停止采样或 provider 不更新时，调度与诊断的行为；
5. **与 reference 算法的交互**：候选 lag 过滤、确认轮次、分母不缩小；
6. **离线回放**：将真实 capture 转成 replay，不联网运行。

在上述证据完成前，不修改 `maxLag` 默认值、tip gate 参数、reference tag
语义或 finalized 候选规则。短窗口观测只证明 provider 高度分布不一致，
不足以选择 lag 单位、适用方法或自适应阈值。

## 5. 与现有文档/决策的关系

- 决策 0032：保持 tag 语义、稳定共同参考优先；
- 决策 0033：freshness 与 agreement 分离，固定 lag 不是前置条件；
- 决策 0037：保留 Geth block-tag 映射；
- [reference-freshness-observations.md](reference-freshness-observations.md)：
  2026-10-31 的短窗口 latest/safe/finalized 高度与 lag 观测；
- [moving-tag-matrix.md](moving-tag-matrix.md)：pending/EIP-1898/tag 的方法
  级矩阵；
- [adaptive-congestion-control.md](adaptive-congestion-control.md)：调度容量
  与 freshness 的边界。

## 6. 非目标

- 不引入轻客户端、checkpoint 或 sync committee 证明；
- 不把 freshness 变成 canonicality/finality 结论；
- 不改变固定成员、分母、权重、模式或门槛；
- 不因某个 provider 更快就给它更高权重；
- 不在本阶段实现新的默认 lag 常量。
