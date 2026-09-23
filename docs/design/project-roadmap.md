# EthGuard 后续推进规划

本规划记录 T02 之后的方向。`docs/` 已纳入版本管理；moving-tag matrix
的可执行来源仍是测试，文档只作对照。

## 当前基线

- EthGuard 是多源 Ethereum Execution RPC 客户端，不是轻客户端。
- `Corroborated` 表示 provider-backed cross-source evidence，不是 Ethereum
  consensus、canonical proof 或 cryptographic finality。
- `Unknown=0`；错误返回不得携带成功置信状态。
- T02 已完成 EIP-1898 第一切片、tag-aware negative block handling 和支持方法
  的 pending local observation。
- `eth_getLogs` 的 pending 范围现在本地拒绝，避免进入普通 corroboration。
- 类型化 `BlockByNumber(-1)` 现在本地拒绝，不支持 pending block typed projection。
- `eth_getBlockReceipts` 的 pending 本地拒绝覆盖对象形态（大小写字符串、首尾空白、JSON 字符串、`{blockNumber: pending}`）；`ErrInvalidRequest`，不触 anchor、不接触上游。

## T03：Execution RPC 语义收口

1. 为 `eth_getLogs` 评估 field-level pending observation；T03-A 保持本地拒绝，该观察尚未实现。
2. `BlockByNumber(-1)` 的 pending typed projection 已在 T03-A 正式保持本地拒绝。
3. EIP-1898 object 形态样本已收录在 `internal/rpcsample/testdata/eip1898-object`。端点已脱敏。离线测试只重放形状，不把样本写成兼容性结论。
4. `eth_estimateGas`、`eth_createAccessList` 的对象形态已与 T02 五个 state-read 对齐：移动标签对象改写为 `{blockHash}`，显式对象和 pending 透传，非法对象在 anchor 之前本地拒绝。
5. `eth_getLogs` 的 hash / pending / 范围字段保持 T03-A：pending 本地拒绝，`blockHash` 与范围字段互斥。没有改成已支持 pending。
6. `method-contracts.md`、moving-tag matrix 与上述代码一致。matrix 的可执行来源仍是测试；文档只作对照。

## T04：内容与错误兼容性

1. revert data 比较键已区分缺失、null、空字节、非空 hex 与非 hex JSON。样本 `internal/rpcsample/testdata/revert-data`。测试：`TestNormalizeCallOutcomeSeparatesRevertDataStates`、`TestCallRevertDataConflictIsNotSwallowed`、`TestRevertSamplesUseDistinctDataKeys`、`TestRevertDataSamplesReplayWithoutEndpoints`。
2. block / receipt / transaction 的真实形态在 `internal/rpcsample/testdata/block-receipt-tx`。白名单外字段不制造分歧，缺必填字段不进入准入，另一块内容绑定失败。测试：`TestRealShapesAgreeAfterPruneAndCaseFold`、`TestMissingRequiredFieldsStayOutOfAdmission`、`TestTypedRealHeaderRejectsAnotherBlocksContent`。
3. wire、admission、content check 与 typed projection 分开。空 uncle 列表和空 receipt 数组保持合法。测试：`TestEmptyBlockUnclesAreLegalAndForeignBlockIsRejected`、`TestAdmitDoesNotChangeComparisonKey`、`TestWireNormalizeDoesNotRejectUnknownFields`、`TestTypedReceiptFromAnotherHashIsMalformed`。
4. 同值响应仍只写成 provider-backed evidence。`TestMethodContractsForbiddenClaims` 与 matrix 的 forbiddenClaims 互锁。

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
