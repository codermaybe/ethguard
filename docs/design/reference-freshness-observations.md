# Reference Freshness 观测记录（2026-10-31）

状态：**两次 live observation（3 pass 短窗口 + 8 pass 约 6 分 22 秒扩展窗口），
未产生参数决策**。本记录补充 [reference-freshness-policy.md](reference-freshness-policy.md)
的证据缺口，只登记 latest/safe/finalized 的高度分布、provider lag 和窗口稳定性；
不选择 lag 单位、不修改 `maxLag`/tip gate、不声称 canonicality 或 finality。

## 1. 方法

- 日期：2026-10-31，短窗口 14:20–14:21 UTC，扩展窗口 15:02:16–15:08:38 UTC。
- 4 个公开 HTTPS endpoint，记为 s1–s4；chain ID 1；无需凭据。
- 每个 pass 对每个 endpoint 发送 4 个只读请求：
  `eth_blockNumber`、`eth_getBlockByNumber("latest", false)`、
  `("safe", false)`、`("finalized", false)`。
- 短窗口 3 个 pass，间隔约 15 秒；扩展窗口 8 个 pass，间隔约 45 秒，
  共 128 条记录。
- 使用 `cmd/rpcsample` 的临时 out 目录，原始记录不进入仓库。
- 证据等级：live observation；不是普查，也不是长期 freshness 结论。

## 2. 高度观测

原始高度（十进制）：

| pass | latest（s1/s2/s3/s4） | safe（s1/s2/s3/s4） | finalized（s1/s2/s3/s4） |
| --- | --- | --- | --- |
| 1 | 26313301 / 26313300 / 26313301 / 26313301 | 26313242 / 26313242 / 26313273 / 26313242 | 26313211 / 26313211 / 26313083 / 26313211 |
| 2 | 26313302 / 26313302 / 26313302 / 26313302 | 26313242 / 26313242 / 26313273 / 26313242 | 26313211 / 26313211 / 26313083 / 26313211 |
| 3 | 26313304 / 26313304 / 26313304 / 26313304 | 26313273 / 26313242 / 26313273 / 26313273 | 26313242 / 26313211 / 26313083 / 26313242 |

观察：

- **latest**：三个 pass 内的最大差为 1 个 block；多数时间四个源一致。
  同一 pass 内，`eth_blockNumber` 与 `eth_getBlockByNumber("latest")`
  可能相差 1 个 block（双向都出现过），说明同一 provider 的两个
  “latest” 读取也不是原子快照。
- **safe**：pass 1/2 中 s3 领先其余源 31 个 block；pass 3 中 s1/s3/s4
  一致，s2 落后 31 个 block。provider 的 safe tag 更新节奏不同。
- **finalized**：pass 1/2 中 s3 落后 128 个 block；pass 3 中最大差为
  159 个 block（s1/s4 已前进，s2 仍落后 31，s3 仍落后 159）。
  finalized candidate 的更新节奏和 lag 明显不一致。
- **相同高度**：在相同高度上未观察到 hash 分歧；本轮没有观察到短程
  reorg 或 safe/finalized 回退。窗口只有约 49 秒，不能据此声称 reorg
  不会发生。
- 四个 endpoint 都返回了 safe/finalized 结果，没有 tag 不支持错误；
  这只是一个样本，不证明所有 provider 都支持。

### 2.1 扩展窗口（8 pass，约 6 分 22 秒）

表内是 `eth_getBlockByNumber` 的 block number；`—` 表示该请求失败
（pass 2 s3 返回 HTTP 503，见下）：

| pass | latest（s1/s2/s3/s4） | safe（s1/s2/s3/s4） | finalized（s1/s2/s3/s4） |
| --- | --- | --- | --- |
| 1 | 26313510 / 26313510 / 26313509 / 26313510 | 26313465 / 26313465 / 26313273 / 26313465 | 26313433 / 26313433 / 26313369 / 26313433 |
| 2 | 26313514 / 26313514 / — / 26313514 | 26313465 / 26313465 / 26313273 / 26313465 | 26313433 / 26313433 / 26313369 / 26313433 |
| 3 | 26313519 / 26313519 / 26313519 / 26313519 | 26313465 / 26313465 / 26313273 / 26313465 | 26313433 / 26313433 / 26313369 / 26313433 |
| 4 | 26313523 / 26313523 / 26313523 / 26313524 | 26313465 / 26313465 / 26313273 / 26313465 | 26313433 / 26313433 / 26313369 / 26313433 |
| 5 | 26313528 / 26313528 / 26313528 / 26313528 | 26313465 / 26313465 / 26313273 / 26313465 | 26313433 / 26313433 / 26313369 / 26313465 |
| 6 | 26313532 / 26313532 / 26313531 / 26313532 | 26313497 / 26313497 / 26313273 / 26313497 | 26313465 / 26313465 / 26313369 / 26313465 |
| 7 | 26313537 / 26313537 / 26313537 / 26313537 | 26313497 / 26313497 / 26313273 / 26313497 | 26313465 / 26313465 / 26313369 / 26313465 |
| 8 | 26313541 / 26313541 / 26313540 / 26313541 | 26313497 / 26313497 / 26313273 / 26313497 | 26313465 / 26313465 / 26313369 / 26313465 |

观察：

- **latest**：8 个 pass 的最大 spread 为 1 个 block；所有 source 的高度在
  窗口内单调不减，没有观察到回退。`eth_blockNumber` 与同 pass 的
  `latest` block 仍可能相差 1 个 block，确认同一 provider 的 latest 读取
  不是原子快照。
- **safe**：最大 spread 为 224 个 block。s1/s2/s4 在 pass 6 一起前进 32 个
  block（26313465 → 26313497），s3 在整个窗口固定在 26313273 不动；
  s3 的 latest 只落后 1 个 block，说明这不是 tip 落后，而是 safe tag
  的更新节奏/定义差异。
- **finalized**：最大 spread 为 96 个 block。s4 在 pass 5 前进 32 个 block，
  s1/s2 在 pass 6 前进 32 个 block；s3 固定为 26313369。s3 的 latest
  基本同步，因此不能用 latest 推断 finalized 的新鲜度。
- **相同高度**：8 个 pass 中相同高度没有 hash 分歧，也没有 source 高度
  回退；约 6 分 22 秒的窗口内没有观察到 reorg 或 safe/finalized 回滚。
  这仍不足以证明 reorg 不会发生。
- **瞬时可用性**：pass 2 的 s3 `latest` block 返回 HTTP 503，body 是
  `{"result":{"code":503,"message":"server unavailable"}}`（不是合法的
  block 对象）；同 pass 的 s3 `eth_blockNumber`、safe、finalized 仍返回。
  这是 provider 可用性/坏响应，不应被当成 freshness 或容量信号。
- 四个 endpoint 都返回了 safe/finalized 结果；仍只是样本，不是 provider
  普查。

## 3. 对 freshness 设计的含义

- 不能用一个全局固定 lag 常数覆盖 safe/finalized 的 provider 差异；
  至少需要区分 latest tip、safe candidate 和 finalized candidate。
- 不能用一个 provider 的最高高度单独判定其他 provider 过期；agreement
  与 freshness 仍必须分离。
- latest 读取的 1-block 快照差异提示：tip gate 和 reference 建立不应把
  “同一次调用内的多次 latest 读取”当成同一快照。
- safe/finalized 的 lag 可能来自 tag 定义、更新节奏或节点状态，本观测
  不能区分原因；不据此给任何 provider 加权或降级。
- 扩展窗口里 s3 的 safe/finalized 在全部 8 个 pass 固定不动，而 latest
  只落后 1 个 block；这进一步说明不能用 latest tip 推断 safe/finalized
  的新鲜度，也不能用一个全局 lag 常数同时覆盖两者。
- 扩展窗口的 safe spread（224）大于短窗口的 31，finalized spread（96）与
  短窗口的 159 也不同；观测值会随窗口长度和 provider 的 tag 更新周期变化，
  不能把任何单次 spread 写成默认阈值。
- 同 pass 的 s3 HTTP 503 是可用性/坏响应，不是 freshness 证据；freshness
  失败与 provider 故障必须继续分开归因。
- 显式历史读取仍应豁免 freshness；本轮没有测试显式历史读取，留待后续。

### 3.1 当前实现事实（2026-10-31 复核）

本轮同时复核了现有代码路径，只登记事实，不改变实现：

- corroborating 读取走 `pool.CollectVoting`：`votingGate` 按 source 的
  sampled tip 与 fleet lower-median 的 `staleFloor` 比较；被暂停的成员保留
  在投票 basis 中并携带 `ErrSourceGated`，不记录 health failure；
- 非 corroborating 的 `pool.Collect` 若带显式 `Sources`，按契约跳过
  freshness/cooldown gate；`pool.Broadcast` 明确不做 read freshness gating；
- tip gate 只读取 source tip，不读取本次请求的 tag/height。显式历史读取
  不会因为“历史”被拒绝，但落后 source 仍可能被暂停，从而改变可用 source
  集合；这是“显式历史读取豁免”的边界，尚需裁决；
- `sampledTip`/`staleFloor` 不按 `tipObservedAt` 的年龄过期样本；最后一次
  tip 采样会持续参与 floor 计算，样本过期语义仍未决；
- 现有离线证据：`TestTipTrackingQuarantinesLaggingSource`、
  `TestCollectVotingKeepsCoolingMemberInBasis`、
  `TestFinalizedAnchoringIsolatesStaleReporter` 和
  `TestBroadcastTrailingSourceStillContacted`。

这些事实不改变 §2 的高度观测，也不授权新的 lag 参数或自适应策略。

## 4. 未观察项

- 长窗口 reorg/回退（扩展窗口只有约 6 分 22 秒，未观察到回退）；
- tip 样本过期（停止采样或 provider 不更新时，调度与诊断的行为）；
- finalized candidate 与 reference confirmation 的交互；
- 显式历史读取不被 freshness 拒绝（本轮只验证了 provider 能返回历史块，
  没有验证 orchestrator 的 freshness 路径）；
- 多链、不同客户端实现和不同采样间隔；
- freshness 失败时的调度/诊断行为（代码事实见 §3.1，但缺少新的 live
  触发样本）。

在这些证据完成前，不新增默认 lag 常量，不修改 `maxLag`、tip gate 参数、
tag 语义或 finalized 候选规则。

## 5. 与现有文档的关系

- [reference-freshness-policy.md](reference-freshness-policy.md)：不变量和
  待裁决问题；
- [adaptive-congestion-control.md](adaptive-congestion-control.md)：freshness
  是内容判定，拥塞是调度容量，两者共享 source 预算但不共享决策语义；
- 本记录只提供 live observation，不改变 Decision、quorum、reference 或
  typed projection。
