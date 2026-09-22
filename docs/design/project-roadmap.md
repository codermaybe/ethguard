# EthGuard 后续推进规划

本规划记录 T02 之后的方向。文档目录当前按本地工作流保留，不作为 Git
提交的一部分；代码与测试提交仍是唯一可执行状态来源。

## 当前基线

- EthGuard 是多源 Ethereum Execution RPC 客户端，不是轻客户端。
- `Corroborated` 表示 provider-backed cross-source evidence，不是 Ethereum
  consensus、canonical proof 或 cryptographic finality。
- `Unknown=0`；错误返回不得携带成功置信状态。
- T02 已完成 EIP-1898 第一切片、tag-aware negative block handling 和支持方法
  的 pending local observation。
- `eth_getLogs` 的 pending 范围现在本地拒绝，避免进入普通 corroboration。
- 类型化 `BlockByNumber(-1)` 现在本地拒绝，不支持 pending block typed projection。

## T03：Execution RPC 语义收口

1. 为 `eth_getLogs` 评估 field-level pending observation；T03-A 保持本地拒绝，该观察尚未实现。
2. `BlockByNumber(-1)` 的 pending typed projection 已在 T03-A 正式保持本地拒绝。
3. 采集并脱敏 EIP-1898 object 形态的真实 provider 样本。
4. 补齐 `eth_estimateGas`、`eth_createAccessList` 的对象形态契约。
5. 固定 `eth_getLogs` 的 hash、pending 和范围字段语义。
6. 重审 `method-contracts.md` 与 moving-tag matrix 的一致性。

## T04：内容与错误兼容性

- 完成 revert data 的 provider 差异采样和保留规则。
- 完成完整 block、receipt、transaction 的真实字段兼容验证。
- 区分 wire normalization、admission、content commitment 和 typed projection。
- 不把同值响应描述成密码学证明。

## T05：运行期证据与可观测性

- 明确 provider-backed reference 的新鲜度和失败语义。
- 保持 `DecisionEvent` 与 `rpc.Status` 的双模型，不把 `failed` 加进置信状态。
- 记录 `corroborated` label 迁移对本地 dashboard 和 alert 的影响。
- 评估是否需要结构化的 evidence validity helper，而不是依赖裸状态比较。

## 暂不推进

- Ethereum light client、BLS、sync committee、trusted checkpoint。
- provider 权重、信誉和 failure-domain 模型。
- pool 大规模重构。
- 跨调用 anchor cache。
- 仅为扩大方法数量而新增 RPC 方法。

## 每个新方法的进入条件

新增 RegisteredMethod 必须同时提供：

1. 官方请求和响应契约；
2. moving-tag / block-reference 处理矩阵；
3. admission 与 normalization 边界；
4. 成功、冲突、无效、pending 和 provider error 测试；
5. 明确的 provider sample 状态；
6. 对外说明它提供的是 corroboration evidence，而非 Ethereum consensus proof。
