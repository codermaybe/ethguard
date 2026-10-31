# Reference Freshness 观测记录（2026-10-31）

状态：**一次短窗口 live observation，未产生参数决策**。本记录补充
[reference-freshness-policy.md](reference-freshness-policy.md) 的证据缺口，
只登记 latest/safe/finalized 的高度分布、provider lag 和短窗口稳定性；
不选择 lag 单位、不修改 `maxLag`/tip gate、不声称 canonicality 或 finality。

## 1. 方法

- 日期：2026-10-31，14:20–14:21 UTC。
- 4 个公开 HTTPS endpoint，记为 s1–s4；chain ID 1；无需凭据。
- 每个 pass 对每个 endpoint 发送 4 个只读请求：
  `eth_blockNumber`、`eth_getBlockByNumber("latest", false)`、
  `("safe", false)`、`("finalized", false)`。
- 共 3 个 pass，间隔约 15 秒；使用 `cmd/rpcsample` 的临时 out 目录，
  原始记录不进入仓库。
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

## 3. 对 freshness 设计的含义

- 不能用一个全局固定 lag 常数覆盖 safe/finalized 的 provider 差异；
  至少需要区分 latest tip、safe candidate 和 finalized candidate。
- 不能用一个 provider 的最高高度单独判定其他 provider 过期；agreement
  与 freshness 仍必须分离。
- latest 读取的 1-block 快照差异提示：tip gate 和 reference 建立不应把
  “同一次调用内的多次 latest 读取”当成同一快照。
- safe/finalized 的 lag 可能来自 tag 定义、更新节奏或节点状态，本观测
  不能区分原因；不据此给任何 provider 加权或降级。
- 显式历史读取仍应豁免 freshness；本轮没有测试显式历史读取，留待后续。

## 4. 未观察项

- 长窗口 reorg/回退；
- tip 样本过期（停止采样或 provider 不更新）；
- finalized candidate 与 reference confirmation 的交互；
- 显式历史读取不被 freshness 拒绝；
- 多链、不同客户端实现和不同采样间隔；
- freshness 失败时的调度/诊断行为。

在这些证据完成前，不新增默认 lag 常量，不修改 `maxLag`、tip gate 参数、
tag 语义或 finalized 候选规则。

## 5. 与现有文档的关系

- [reference-freshness-policy.md](reference-freshness-policy.md)：不变量和
  待裁决问题；
- [adaptive-congestion-control.md](adaptive-congestion-control.md)：freshness
  是内容判定，拥塞是调度容量，两者共享 source 预算但不共享决策语义；
- 本记录只提供 live observation，不改变 Decision、quorum、reference 或
  typed projection。
