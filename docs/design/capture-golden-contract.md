# Capture/golden 契约

本文件拥有真实 RPC 捕获的 provenance、目录、reference 上下文、golden 内容与覆盖
等级。验证阶段和测试归属见 [method-verification-pipeline.md](method-verification-pipeline.md)；
当前方法覆盖见 [method-transport-coverage.md](method-transport-coverage.md)，不在此维护
“下一批未捕获方法”或施工阶段清单。规则的权威实现仍是 replay 测试。

## 1. 证据边界与传输

**G1：普通 `expect.json` 的 `transport` 必须为 `"http"`。**

`validateExpectHeader`（`internal/rpcsample/replay/expect_test.go`）拒绝非 HTTP。
决策 0056/0056.1 对普通 WSS golden 的限制仍然适用，不生成、不接受、不预留 WSS
变体。原生 WSS 帧保真、geth adapter 的 presence 限制与真实 provider 是否观察到
某形态是三个不同问题；不能因其中一项完成而自动关闭另外两项。

- `eth_call` 开启 `WithRPCErrorBodies()`，error body 是裁决值域的一部分；data
  缺失、JSON null、`0x`、具体值必须保留区别。
- `eth_getLogs`、`eth_feeHistory`、`eth_chainId`、`net_version`、`eth_gasPrice`、
  `eth_maxPriorityFeePerGas`、`eth_blobBaseFee`、`eth_blockNumber`、`eth_syncing`、
  `net_peerCount`、`net_listening` 的默认 policy 不把 error body 当作 revert 投票内容。
  HTTP 捕获可独立推进，不以 WSS 四态 golden 为先决条件。
- HTTP golden 不证明 WSS 的帧、生命周期或跨传输行为已经验收。以后若扩展 WSS
  证据路径，不需要因此重录已有 HTTP 观察；新的证据仍须单独满足自己的契约。
- 合成 fault 场景证明“若返回这种形态，客户端如何处理”，不证明 provider 返回过
  这种形态。Anvil 状态也不是 provider capture；外部观察流程见
  [live-canary-contract.md](live-canary-contract.md)。

## 2. 捕获目录、不可变记录与 golden

```text
internal/rpcsample/testdata/<feature>/<batch-id>/
├── batch.json       # ethguard.batch/v1；批次 provenance、可选 reference_context
├── index.json       # 每条 record 的来源、请求、时间与 SHA-256
├── expect.json      # ethguard.expect/v1；冻结观察的回放预期
└── records/
    └── <request-id>--<source-id>.json
```

- record 的 `request` 永远是实际发出的 wire 请求。请求参数不在 `batch.json`
  重复；逻辑请求与 reference 的关系按 §3 单独记录。
- `batch.json` 至少含 `feature`、`batch_id`、`network`、`captured_at`；记录捕获
  说明、fork、可得时的高度与 `web3_clientVersion`。客户端身份是上下文，不是方言
  归因：商业网关可能有异构后端，同一端点也可能返回不同形态。
- feature 是 EthGuard 的分组标签，不是官方枚举。**目录名、batch.feature、
  expect.feature 必须一致；`index.json` 没有 feature 字段。**
- 来源只用代号；清除 URL、API key、私网 IP、userinfo 和 query 值，遵循
  `cmd/rpcsample` 的脱敏规则。
- 已入库 record 和 index 除脱敏修正外不可修改。硬分叉/新格式新增批次，保留
  历史记录。SHA-256 按原始记录文件字节校验，不先重新序列化。
- 新增/放宽 normalization、pruning 或弃权规则所依据的社区样本视为不可信输入：
  应可复现（优先 state override、历史 hash 等确定性请求），或来自至少两个独立
  贡献者，或维护者能在线重采；不能凭不可核实样本放宽安全规则。

### 2.1 `expect.json` 是数据，不是测试语言

每个 case 至少包含：

1. `schema_version`、请求标识与实际 wire 请求、`transport`、成员数 `n`、mode、floor；
2. 每源比较键、准入结果和 Faults；
3. 每个受保护维度的 Support、Reject、Abstain、Returned；
4. 是否发生删减投影、被扣除字段、最终 Decision.Status/error。

不得引入条件表达式、继承、`Prefix*` 或自动生成测试逻辑。CI 回放与 expect 比较；
改变分类/裁决的变更必须附可解释的 golden diff，不能盲目 `-update`。

### 2.2 新批次必须接入实际执行入口

- feature 和 batch-id 随批次登记，不能改全局 `sampleBatchID` 而覆盖历史批次。
- 新 feature 同时加入 `TestRegisteredMethodSampleStatus` 遍历列表、其他 feature
  遍历入口及该批次的 expect 回放入口；不要在目录尚不存在时提前放空占位。
- 对已归档方法更新 `internal/rpcsample/replay/batch_expect_test.go` 的 `noCapture`。
  这个 map 的清空不证明必需 case 已全覆盖。
- `TestRegisteredMethodSampleStatus` 只证明 record 中出现方法，既不证明有 expect，
  也不证明 expect 被执行；必须同时接通真实回放和 case 覆盖断言。

**G4：回放离线。** 捕获可在受控环境访问真实 provider；提交的是脱敏冻结记录。
测试只允许进程内 `httptest`，不允许外部网络 I/O。

## 3. Reference 上下文与实际请求

**G2：只对声明了 block-reference 的方法建立相应 reference，不给无参数、
chain-local 或 variable 方法虚构 anchor。**

| 方法形态 | 捕获/回放要求 |
| --- | --- |
| `eth_getLogs` | 过滤对象 `fromBlock`/`toBlock` 按 reference **高度**改写 |
| `eth_feeHistory` | `[blockCount, newestBlock, rewardPercentiles]` 只有参数 1 是 block-reference，改写为**高度**，不是 hash |
| 无 block-reference 参数的 chain/local/median 方法 | 按自己的 wire 形态捕获，不新增一致性维度 |

`safe` 与 `finalized` 各自建立，不回落 `latest`。上游不支持时换源；所有可用源
都不支持则登记未覆盖，不伪造 record，不放宽要求，也不阻塞无关方法的捕获。

### 3.1 `batch.reference_context` 的载体

`cmd/rpcsample` 直接按 plan 发请求，本身不建立 EthGuard 共同 reference。
直接向多个源发送 `safe` 可能得到不同高度；不能用 replay 的手填高度声称已经验证
真实日志。因此新 logs/feeHistory 的 moving-tag case 必须提供 reference 上下文。

保持 record 和 `expect/v1` 不变：expect 的 request 仍为实际 wire 请求；
`batch.json` 的可选 `reference_context` 按业务 `request_id` 索引：

| 字段 | 含义 |
| --- | --- |
| `logical_request` | prepared 前的 method、params |
| `tag` | safe 或 finalized |
| `established_at` | 捕获时建立时间 |
| `members` | 有序来源 ID，包含失败成员 |
| `reference_records` | 实际 reference 请求/响应的引用，关联来源并经过 SHA-256 校验；有确认轮次也要保留 |
| `expected_reference` | 离线重算目标：height、hash、status、support |

`reference_records` 是输入，`expected_reference` 只是断言目标，**不能直接注入 resolver**。
离线恢复逻辑请求，用捕获的 reference 响应驱动真实 anchor 路径，再比较准备后的
业务请求与 record 的 method、params。JSON-RPC ID 是传输关联标识，不要求相同。

硬边界：

- 不得事后改 record，伪装成曾经过共同锚定。
- 两层使用相同成员顺序与既有 mode/floor 快照，不另建 quorum 配置。
- reference 辅助记录与业务 case 明确区分，不能被通用分组器误当业务 golden。
- 历史三批 `revert-data`、`block-receipt-tx`、`eip1898-object` 可以无上下文；
  新的 logs/feeHistory moving-tag case 缺上下文或引用无效必须失败。
- batch 读取结构同步支持该可选字段；`DisallowUnknownFields` 要求同步维护读取
  类型，不是禁止新增可选批次元数据的理由。

## 4. 按 resolver 断言，不统一套 quorum

**G3：golden 必须区分 wire/shape、Corroborated、Selected/local、Fault/Forwarded。**

| 方法示例 | 成功语义 |
| --- | --- |
| chainId、net_version、logs、feeHistory | 参与佐证，达到门槛后 Corroborated |
| gasPrice、maxPriorityFeePerGas、blobBaseFee、blockNumber | lower median，Selected |
| syncing、peerCount、listening | local 观察，Selected |

wire/shape 包括 syncing 的 false/对象与 logs 的空/非空数组。gas、tip、blob fee
和 block number 随时间变化，golden 检查冻结 record 的回放一致性，不把跨批数值
相等当契约。median spread 是诊断，不是佐证；Selected/Forwarded 不能冒充 Corroborated。

**G5：参与佐证方法的 `n` 是 Aggregate 入口冻结的 voting basis 成员数。**

- 捕获发生于 Prepare、准入之前；响应无效或被 gate 跳过的成员仍在分母。
- `expect.n == len(sources)` 是记录完整性约束，不表示所有 resolver 成功条件相同。
- 参与佐证的方法用 `Q = max(H, floor(n/2)+1)`，其中 H 为 floor；local/median
  不套此成功条件。达不到门槛保持 Unknown/Conflict，不降 floor、不缩分母。
- “录几个源”是产生 case 分母的手段，不把分母定义成成功返回的源数。

## 5. 覆盖完成等级

**L1：已有真实归档且实际回放通过。** 必须有 batch、index、records、expect；
方法从 noCapture 移除，sample-status 检查通过，expect 回放入口已接入并通过。
只“有归档”不够。

**L2：所有必需 case 都有真实 record/expect，且由内容覆盖断言实际检查。**
至少包括 syncing 的 false 与对象态、logs 的空与非空结果及 safe/finalized 上下文、
feeHistory 的 safe/finalized 上下文；不能只检查 request-id 名称。

未覆盖项如实登记，不合成 provider record、不跳过测试制造完成。L1 可作为部分
成果提交，L2 仍保持未完成。批次级完成还需回放测试与全量
`go test ./... -count=1` 的实际结果，不降低门槛、不删分母、不改品牌字符串。

## 6. 历史观察的适用边界

以下保留原 resolver 盘点中的有用限制，**不是当前覆盖索引，也不是新的 provider
兼容性结论**。状态更新只进入 live 方法矩阵，原始证据继续由捕获资产和 Git 保留。

| 已记录观察 | 能说明什么 / 不能说明什么 |
| --- | --- |
| 2026-09-23 `block-receipt-tx`、`eip1898-object` | block/receipt/transaction 与对象形状；部分 nonce/storage/code 对象只有单源成功，不能据此声称多源兼容 |
| 2026-09-23 `revert-data` | 四态与 4e 回放、execution/data 维度；generic Strict 的单 value 维度不是多维裁决 |
| 2026-09-05 soak logs | 曾观察到 70 条日志；不替代 logs 的必需 case 覆盖 |
| 2026-10-31 latest object 探测 | balance/nonce/storage/code/createAccessList 4/4；一次成功不等于兼容性结论 |
| 2026-10-31 block-by-number hash 位置探测 | 3 个拒收、1 个限流；不得把此 wire 形态当普遍可接受 |
| 2026-10-31 feeHistory | moving 区间 3/4 oldestBlock 一致、1 源区间不同；盘点记录固定历史区间 `[1,33,67,99]` 为 4/4 一致，不把窗口变化直接判成语义冲突 |
| 2026-10-21 blobBaseFee 样本与 2026-10-31 探测 | 后者 2/4 成功、1 个 free-plan 限制、1 个策略拒绝；能力/权限限制不可伪装成业务支持 |
| 2026-10-31 syncing | 4/4 都是 false，progress object 未观察到；不能用合成对象关闭真实捕获缺口 |
| 2026-10-31 WSS revert | 曾观察 missing、`0x` 与具体值；不能据此补出未观察的真实 null 或生成普通 WSS golden |

空 body 的 HTTP 429/403 是 transport fault，不进入比较键。捕获中缺失、null、
空结果与 transport fault 的区分不能在归档或报告时丢失。
