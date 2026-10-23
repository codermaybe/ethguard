# Live canary 契约（T10.0）

状态：**已冻结（2026-10-23）**。本文件是 T10 live canary 的规则正文；
决策见 `decisions.md` 0059。T10.1 起按本契约生成 schema、manifest 和 report。
本文件不改变 reference policy、`expect/v1`、`batch.json` 或生产裁决语义。

关联：`project-roadmap.md` T10、`capture-golden-contract.md` G1–G5、
`reference-policy-proposal.md`、决策 0026/0037/0056/0058。

---

## 1. 证据分层与措辞

T10 同时维护三层证据，任何一层都不得冒充上一层：

| 层 | 证明什么 | 最低产物 | 不能证明什么 |
| --- | --- | --- | --- |
| Offline regression | 固定源码、依赖和输入下，生产路径可重复 | 外部 CI 的 commit 绑定日志、测试结果、构建/覆盖 artifact | 真实 provider 行为、长期稳定性 |
| Live observation | 指定时间、指定 Chainlist 快照和指定 RPC 集合上的实际行为 | 远端 runner 生成的 registry 原文或安全副本、registry hash、候选/选中成员、plan、commit、工具和运行报告 | 共识、canonicality、finality、provider 独立性 |
| Attested evidence | 上述 live 产物没有被本地手工替换，且可由第三方复核 | 不可变 artifact、文件 digest、CI run/身份和保留链接 | 对抗共谋、统一错误、未来时刻的可靠性 |

固定措辞：

- live canary 是 **reality observation**，不是 proof；
- `finalized`、`safe`、`latest` 是 **provider-reported tag**，不是
  Ethereum finality；
- `Corroborated` 是 provider-backed cross-source evidence，不是
  consensus、canonical proof 或 cryptographic finality；
- `passed` 只表示本次 canary 契约成立，不表示 provider 值正确、网络健康或
  其他时刻仍然成立；
- `inconclusive` 是 **没有结论**，不是成功；
- `failed` 表示代码或契约不变量被破坏，不是 provider 波动。

---

## 2. Run state

每次 live run 有且只有一个顶层 `state`：

| state | 含义 |
| --- | --- |
| `passed` | 固定 plan 全部执行；每个必需结果满足 plan 的允许状态；证据守恒；没有 `failed` 或 `inconclusive` 原因。少数派 `reject` 和语义/形状 fault 可以存在，但必须完整记录，且不得包含可用性 fault。 |
| `failed` | 发现代码错误、请求绑定失败、非法状态、证据缺失、分母变化或 schema/hash 校验失败。失败必须保留证据，不得改写成 `inconclusive`。 |
| `inconclusive` | 外部条件使观察不完整：registry 不可用、有效成员不足、provider 不可达/限流/超时、reference 或 identity 无法达到 corroborated、quorum 不足、整体超时；畸形响应在期望状态未达成时同样使观察不完整。`inconclusive` 不得作为 release 成功。 |

聚合优先级：

```text
failed > inconclusive > passed
```

- 任一 `failed` 原因存在时，顶层状态必须是 `failed`。
- 否则任一 `inconclusive` 原因存在时，顶层状态必须是 `inconclusive`。
- 只有 `state_reasons` 为空时，顶层状态才可以是 `passed`。
- 产生的 artifact 是否上传成功，与 `state` 无关。workflow 可以成功产出
  一个 `inconclusive` artifact，但不得把该 run 显示为 `passed` 或让 release
  gate 把它当成功。
- `passed` 不要求每次出现冲突，也不把没有冲突当作安全证明。冲突、dissent 和
  fault 都必须完整归类并保留；冲突不能达成期望 status 时 run 为
  `inconclusive`。少数派 `reject` 是合法观察结果；provider 可用性 fault 按
  §7 归入 `inconclusive`。

---

## 3. 成员固定与 Chainlist 边界

### 3.1 Chainlist 只负责 discovery

- Chainlist 是社区列表，不是信任锚、不是投票权重、不是 provider 质量排名。
- 每次 run 在启动时抓取一次 registry，之后不刷新。列表变化只产生新的 run，
  不与旧 run 合并或跨 run 比较。
- 一次 run 的成员集合在发出第一个业务 RPC 前固定；之后不得替换、添加、
  删除或缩小分母。成员失败仍占分母。

### 3.2 固定选择算法 `chainlist-sorted-v1`

T10.1/T10.2 必须按以下顺序实现，selection 算法版本写入 manifest：

1. 解析 registry 顶层 JSON；只取 `chainId == manifest.chain_id` 的条目；多个
   条目按 registry 原顺序合并。目标 chain 不存在时，run 为 `inconclusive`。
2. 只针对目标 chain 解析 RPC 条目，接受对象形态（`url` 字段）和裸字符串
   形态。无法解析的条目记录 raw entry 的 SHA-256 和 `invalid_url`；registry
   顶层 JSON 无法解析时 run 为 `inconclusive`。其他 chain 的条目问题不影响
   目标 chain。
3. 解析 URL；无 scheme/host 或含 fragment 的条目记为 `invalid_url`。
4. scheme 不是 `https` 的条目记为 `non_https`（含 `http`、`ws`、`wss`）。
5. URL 含 userinfo，或 query 参数名（大小写不敏感）为 `apikey`、`api_key`、
   `key`、`token` 的条目记为 `credentials`。
6. 计算 endpoint identity，规则与 `pool` 的 endpoint 去重键一致：
   scheme/host 小写、去默认端口、裸 `/` 路径归一为空；path/query 保留。
   endpoint identity 已出现时，保留 registry 顺序中的第一个，其余记为
   `duplicate`。
7. 按 endpoint identity 的字节序升序排序；取前 `max_members`（v1 为 12）个
   作为 selected，其余合法条目记为 `cap_exceeded`。
8. 按 selected 顺序编号 `node-01` … `node-N`；计算 `voting_basis_sha256`：
   对每个 selected 成员按顺序取 `source_id + "\t" + url + "\n"`，UTF-8 编码
   后求 SHA-256。

过滤原因闭集：`invalid_url`、`non_https`、`credentials`、`duplicate`、
`cap_exceeded`。

约束：

- `min_members` 固定为 **3**，`max_members` 固定为 **12**。
- selected 成员少于 3 时，run 为 `inconclusive`（`insufficient_members`），
  不发起业务 RPC，不补成员。
- selected URL 必须是去凭据后的 HTTPS URL；否则该成员在步骤 4 已被过滤。
  manifest 可以发布 selected URL。被过滤的凭据 URL 只记录 `url_sha256`，
  不发布原文。
- 固定成员数与生产门槛的关系：生产默认 floor=2，`Q = max(2, floor(N/2)+1)`。
  N=3 时 Q=2，canary 才可能在单个成员故障下仍有可审计的 corroborated
  reference；N=1 会进入 forwarded 语义，不能作为 live canary。
- selected URL 必须可公开且不含凭据；若无法在不泄露凭据的前提下发布 selected
  basis，run 为 `failed`（`evidence_missing`），不得脱敏 selected URL 后仍声称
  可重建。
- v1 只按上述明确规则过滤，不对 path 做高熵 token 启发式。若发现 selected
  URL 的 path 含凭据，必须升级 selection 算法版本并重新选择，不得在同一版本
  静默改变过滤规则。
- v1 只选择 HTTPS；WSS 的 error-data presence 缺口按决策 0056 处理，不把 WSS
  纳入本 canary 的传输证据。
- 本契约不引入 provider 权重、动态替换或信誉排序（决策 0058）。

### 3.3 Registry artifact

- manifest 记录抓取时间、原始字节 SHA-256、发布副本 SHA-256、发布副本文件名
  和脱敏模式。
- 若 registry 含明显凭据，发布副本必须脱敏；脱敏不得改变 selected 成员或
  过滤原因的可复核性。原始 hash 只证明抓取字节，不证明来源签名。
- registry 抓取或解析失败时，manifest 仍记录 attempted URL/时间/失败状态，
  report 为 `inconclusive`（`registry_unavailable`）。

---

## 4. 超时与资源边界

v1 固定值：

| 字段 | 值 | 生产用途 |
| --- | --- | --- |
| `run_ms` | `300000`（5 分钟） | 整个 plan 的总 deadline；超时为 `inconclusive`（`run_timeout`） |
| `registry_ms` | `60000` | 单次 Chainlist 抓取 deadline |
| `reference_ms` | `30000` | reference 建立调用的 deadline；与生产 `collect_timeout` 默认值一致 |
| `step_ms` | `30000` | 每个业务/identity/negative 步骤的 deadline |
| `registry_max_bytes` | `33554432`（32 MiB） | registry 响应体上限；超限为 `inconclusive`（`registry_unavailable`） |

- 每次调用只请求一次，不自动重试；重试会产生新的 observation，不在同一 run
  内发生。
- 不使用跨调用 anchor cache（决策 0026/0037）。每个带 moving tag 的调用各自
  建立 reference；报告记录该调用自己的 reference。
- `started_at`、`finished_at`、`duration_ms` 必须由运行器记录，用于超时和
  审计；不得由人工填写。

---

## 5. 固定请求 plan `mainnet-canary-v1`

### 5.1 Plan 身份

- `plan.id = "mainnet-canary-v1"`。
- `plan.sha256` 是 canonical JSON（对象键排序、无多余空白、数组保持顺序）下
  `{id, steps}` 的 SHA-256；不包含 `sha256` 字段本身。
- plan 是固定常量，不读取环境变量、不按 run 动态生成、不因 provider 结果
  增减步骤。plan 内联进 manifest，report 只引用 `plan_sha256`。
- `$reference.hash` 是唯一允许的占位符，由 runner 用 **run reference** 的
  hash 替换；report 必须记录替换后的逻辑请求和实际 prepared request。

### 5.1.1 Reference scope：run reference 与 step reference

本契约冻结两个名字，执行时不得互换：

- **run reference**：`reference.finalized-balance` 建立的 corroborated
  reference。`$reference.hash` 永远指向这个 hash；`pinned.block` 和
  `typed.block` 只使用它。report 顶层 `reference_step_id` 必须指向该步骤。
- **step reference**：每个 moving-tag 调用在自身 Prepare 内建立的 reference。
  `business.fee-history.finalized` 等后续步骤各自建立 step reference，只在
  该步骤内与 prepared request、`decision.reference` 绑定；它不替换 run
  reference，也不被后续步骤复用。
- `reference.finalized-balance` 的 step reference 同时就是 run reference。
- 任何步骤都不得跨步骤复用 reference（决策 0026/0037）；report 不得把
  feeHistory 等步骤的 step reference 写入顶层 run reference。
- step reference 与 run reference 在数值上相等是允许的，但不是要求；report
  和测试不得断言两者相等，也不得因两者不同而失败。
- v1 不增加 `$step_reference.*` 占位符。需要 step reference 的步骤用自身的
  moving tag（例如 `"finalized"`）建立它，不通过占位符注入。

### 5.2 步骤

| ID | phase | path | method | params | 期望 |
| --- | --- | --- | --- | --- | --- |
| `identity.chain-id` | identity | client_call | `eth_chainId` | `[]` | decision；`corroborated`；值等于 `chain_id` |
| `reference.finalized-balance` | reference | client_call | `eth_getBalance` | `[vitalik, "finalized"]` | decision；`corroborated`；step reference `corroborated`，并成为 run reference；prepared 区块参数等于该 reference hash |
| `pinned.block` | business | client_call | `eth_getBlockByHash` | `[$reference.hash, false]` | decision；`corroborated`；值 kind 为 object；参数使用 run reference hash |
| `business.fee-history.finalized` | business | client_call | `eth_feeHistory` | `[2, "finalized", [25, 50, 75]]` | decision；`corroborated`；step reference `corroborated`；prepared newestBlock 等于该 step reference height |
| `typed.block` | typed | typed_block | `eth_getBlockByHash` | `[$reference.hash]` | 无 decision；typed block hash/height 等于 run reference |
| `negative.notfound-receipt` | negative | typed_receipt | `eth_getTransactionReceipt` | `[zeroHash]` | decision `corroborated` 且 value 为 null；typed decode 返回 `not_found` |

固定 fixture：

- `vitalik = 0xd8dA6BF26964aF9D7eEd9e03E53415D37aA96045`
- `zeroHash = 0x0000000000000000000000000000000000000000000000000000000000000000`

说明：

- `reference.finalized-balance` 同时承担 reference establishment 和 moving-tag
  state 业务读取；它的 prepared request 已 pin 到 reference hash，因此同时覆盖
  pinned state。reference evidence 与 business decision 在 report 中是两个独立
  对象，必须分别审计。`finalized` 是 provider-reported tag，本次 reference 是
  provider-backed corroboration。
- `pinned.block`、`typed.block` 使用同一个 `$reference.hash`，即
  `reference.finalized-balance` 建立的 **run reference**，不是它们自己的
  step reference。`typed.block` 走 `ethadapter`，不产生新的 decision；它的
  typed hash/height 必须与 run reference 和被 pin 的区块一致。`pinned.block`
  的 header-only decision 已通过 block hash 承诺完整区块内容；typed step 只
  新增 typed decode 断言，不另行制造第二份 block 证据。
- `business.fee-history.finalized` 覆盖需要高度改写的 block-reference 方法：
  它自己建立 **step reference**，prepared request 的 newestBlock 必须是该
  step reference 的 hex height，不能仍是 `finalized`；该 step reference 不得
  被后续步骤复用，也不影响 run reference。
- 示例中 `reference.finalized-balance` 与 `business.fee-history.finalized`
  的 reference 恰好同高同 hash；这只是一次合成 run 的形状，不是相等要求。
  执行 agent 不得把 feeHistory 的 step reference 写入顶层 run reference，
  也不得断言两者必须一致。
- `negative.notfound-receipt` 使用零值交易哈希，期望 fleet 对 null 达成
  corroborated absence，再由 typed 边界映射为 `ethereum.NotFound`。它是
  “错误/不可用路径”中的确定性 notfound 分支，不保证每次 run 触发 provider
  错误；provider 的错误/限流/超时由 §7 分类。
- 不把实时值写成固定 expect。report 只记录本次响应、canonical value digest、
  value kind 和证据；跨 run 数值相等不是契约。

### 5.3 执行顺序

1. 校验 manifest 输入、plan hash、timeouts、成员上下限；失败为 `failed`
   （`schema_error`），不联网。
2. 抓取 registry，固定 selected 与 `voting_basis_sha256`；不足 3 个成员时
   结束为 `inconclusive`。
3. 构造客户端：selected 顺序、`WithFinalizedAnchoring(max_lag_blocks)`、
   `WithCollectTimeout(step_ms)`，不启用 anchor cache。
4. 依次执行 §5.2 步骤；每个步骤使用独立 context deadline，并捕获本次
   prepared request、decision、reference、typed 结果和 per-source evidence。
   identity 未达到期望状态时停止；reference 未建立 corroborated 时停止；
   其余单步失败不跳过后续步骤，以便保留完整观察。
5. 生成 report 和 digest；先写失败/inconclusive 证据，再写顶层 state。
   report 生成失败本身是 `failed`（`code_error`）。

---

## 6. 不变量与证据守恒

以下不变量对每个步骤求值，结果写入 report 的同名布尔字段；不适用时为
`null`，不得用 `false` 冒充“不适用”。

| 不变量 | 检查 |
| --- | --- |
| `request_binding` | 需要绑定时，prepared request 的区块参数必须等于**该步骤自己的 reference** hash（按字节比较）或 height（按数值比较）；`business.fee-history.finalized` 使用它的 step reference，`reference.finalized-balance` 的 step reference 同时是 run reference。moving tag 不得留在 prepared request 中。`binding=none` 的 `client_call` 步骤，prepared 必须与 logical 一致（占位符替换后）；typed-only 步骤不适用。 |
| `status_allowed` | `decision.status` 必须属于 plan 的 `expect.statuses`；error 路径按 `expect.error` 检查。 |
| `evidence_conservation` | `n == len(participants)`；`support`、`reject`、source-attributed `faults` 两两不相交，并集等于 `participants`；`collection_faults` 单独记录；`participants` 数量与顺序等于 manifest selected basis。 |
| `representative_in_support` | `representative` 必须等于 `support` 的第一个来源；`support` 顺序保持本次 basis 顺序。 |
| `reference_status` | 需要 reference 的步骤必须携带**该步骤自己的** `ReferenceEvidence`；`status == corroborated`；`tag` 等于请求的 moving tag；`hash`/`height` 非空（按方法需要）；reference 的 support/reject/source faults 与 participants 按同一规则检查，collection-level fault 单独记录。`reference.finalized-balance` 的这一对象同时是 run reference。 |
| `error_status_exclusive` | `rpc.Call` 返回 error 时 decision status 必须是 `unknown`；成功 status 不得携带 error；不得把 provider error 包成成功。该不变量不约束 typed decode 的预期 `not_found`，后者由 `typed_decode` 检查。 |
| `basis_unchanged` | 本次 run 的 members 始终等于 manifest selected；不得替换、缩小或重排分母；`voting_basis_sha256` 必须一致。 |
| `typed_decode` | typed adapter 必须成功解码，且 hash/height 与本次 run reference 一致（typed block 不要求步骤自带 reference 对象）；negative 路径必须得到预期 `not_found`。 |
| `shape_matches_plan` | actual `value_kind` 必须等于 plan 的 `expect.value_kind`；shape drift 必须记录，不得静默改写。 |

强制规则：

- prepared request 必须是 **观察到的**，不得由 logical request 和 reference
  反推后冒充证据。若当前 seam 无法取得 prepared request，T10.3 必须增加
  不改变裁决语义的诊断 seam；仍无法取得时，run 为 `failed`
  （`evidence_missing`），不得降低要求。
- run reference 只能来自 `reference.finalized-balance` 的 step reference；
  不得从 feeHistory 或其他步骤的 step reference 推导、替换或刷新 run
  reference。step reference 只在该步骤内有效。
- 不得断言 step reference 与 run reference 相等；两者相同是允许的观察，
  不是 canary 契约。
- per-source fault 归属必须来自结构化证据通道（例如 `WithUncertain` 的
  per-source response/error、pool source health，或 T10.3 增加的等价诊断
  seam），不得靠错误字符串猜来源。
- 每个 participant 的处置必须可追踪到 support、reject、source fault、
  collection fault 或 typed 边界；不得无记录消失。
- 少数派 `reject` 不使 run 变成 `inconclusive`；它是被保留的观察。
- **可用性 fault**（不可达、429、超时、未尝试的 gated member、无响应）使
  run 为 `inconclusive`，即使当时 decision 仍达到了期望状态。
- **语义/形状 fault**（畸形响应、admission/encode 失败、fan-out 后的 provider
  error）不自动使 run 变为 `inconclusive`：期望 status 仍达成时，step 记为
  `degraded`，fault 完整保留；期望 status 未达成时，按 §7 归入
  `provider_malformed`、`provider_error` 或 `provider_divergence`。
- `status_allowed`、`evidence_conservation`、`representative_in_support`
  只对到达 fan-out 的 decision 求值。Prepare 阶段失败（例如 reference 未
  corroborated）时 decision 可以为 `null`，由 §7 的
  `reference_not_corroborated`/`provider_*`/`code_error` 分类；不得把正常的
  Prepare 失败误记为 `evidence_missing`。
- 计数不跨维度相加；若方法有多个维度，每个维度各自使用自己的分母和
  support/reject/fault/abstain 证据。本 v1 plan 不包含多维度 `eth_call`。
- `Selected`/`Forwarded` 不得因 canary 场景被升格为 `Corroborated`；在 N>=3
  的 corroborating 方法上出现 `selected`/`forwarded` 是 `illegal_state`
  （failed），不是可接受的降级。

---

## 7. 失败分类

分类是闭集。report 的 `state_reasons[].category` 只能取以下值之一。

### 7.1 failed

| category | 触发 |
| --- | --- |
| `code_error` | 未分类的本地错误、panic、report 序列化/写出失败、fan-out 前不可解释的错误。 |
| `request_binding` | prepared request 未绑定到本次 reference；moving tag 残留；hash/height 不匹配。 |
| `evidence_missing` | 必需 evidence 缺失：reference、participants、support/reject/fault 归属、prepared request、value digest、source health、plan/manifest hash 等。 |
| `illegal_state` | error 携带成功 status；成功 status 为 `unknown`；status 不在允许集合；representative 不在 support；重复来源；support/reject/faults 不是 participant 的划分；N>=3 时出现 selected/forwarded 语义。 |
| `identity_mismatch` | identity decision 成功但值不等于 `chain_id`。 |
| `denominator_changed` | 成员集合、顺序或数量与 manifest selected 不一致；运行中换成员或缩分母。 |
| `schema_error` | manifest/report 不满足 schema，或 manifest/plan digest 不匹配。 |
| `typed_decode` | typed adapter 解码失败、hash/height 与 reference 不一致、negative 路径未得到预期 `not_found`。 |

### 7.2 inconclusive

| category | 触发 |
| --- | --- |
| `registry_unavailable` | Chainlist 抓取/解析失败、超时、超过 `registry_max_bytes`、目标 chain 缺失。 |
| `insufficient_members` | 过滤、去重、限长后 selected 少于 3。 |
| `identity_basis_invalid` | identity 调用因 provider 链 ID 不一致被拒，或 identity 无法达到 corroborated；不是代码成功返回错误值。 |
| `reference_not_corroborated` | reference status 未达到 `corroborated`（quorum、fault、divergence），且证据完整。 |
| `provider_unavailable` | 不可达、无可用源、member 被 gate/冷却/未尝试、无响应。 |
| `provider_rate_limited` | source health 记录 429/已知限流码，或等价结构化限流证据。 |
| `provider_timeout` | RPC/reference 的 context deadline 或单步超时。 |
| `provider_malformed` | 响应畸形、无法通过 JSON safety/admission，且期望 status 未达成。 |
| `provider_error` | 已 fan-out 的 provider error 不在 plan 期望内，也不是上述可用性类别，且期望 status 未达成。 |
| `provider_divergence` | 多个 admissible cluster 冲突，且期望 status 未达成。 |
| `quorum_unmet` | support 低于 Q，或没有有效响应，且期望 status 未达成。 |
| `run_timeout` | 整个 run 超过 `run_ms`。 |

规则：

- 期望 status 达成时，语义/形状 fault 只把 step 记为 `degraded`，不产生
  `state_reasons`；可用性 fault 仍然产生 `inconclusive` reason。
- `expect.error = "not_found"` 的步骤得到预期 `not_found` 时，不产生
  `state_reasons`；它是成功观察。
- 预期 notfound 步骤反而得到成功值：`illegal_state`（failed）。
- 预期 notfound 步骤得到其他 provider error：按 `provider_error` /
  `provider_divergence` 分类；不得写成 passed。
- 任何无法归类的错误不得默认为 `inconclusive`：fan-out 前归
  `code_error`，fan-out 后且证据完整归 `provider_error`，证据不足归
  `evidence_missing`。
- 多个类别同时满足时取更具体者：`provider_malformed`/`provider_error` 优先于
  `quorum_unmet`；存在可比较的冲突值时 `provider_divergence` 优先于
  `quorum_unmet`；完全没有有效响应时才用 `quorum_unmet`。
- 每条 reason 必须带 `step_id`；registry、成员不足等 pre-plan 失败为 `null`。
  provider 相关时带 `source_id`；`detail` 必须脱敏、限长，且不得作为裁决依据。

---

## 8. Manifest 契约

Schema 版本：`ethguard.live-manifest/v1`。示例见
`docs/design/live-canary/manifest.example.json`；示例是合成形状，不是真实
run，也不是证据。

### 8.1 顶层字段

| 字段 | 类型 | 生产用途 |
| --- | --- | --- |
| `schema_version` | string，固定 `ethguard.live-manifest/v1` | 解析/版本分派 |
| `run_id` | string | 关联 manifest、report、artifact 和 run |
| `generated_at` | RFC3339 UTC | run 起始时间与新鲜度审计 |
| `source_commit` | 40 位 git SHA | 第三方按 exact commit 重跑离线部分 |
| `tool_version` | string | 选择 plan/selection 语义 |
| `go_version` | string | 工具链复现 |
| `environment` | object | 区分本地诊断与外部 runner；决定是否可进入 attested evidence |
| `chain_id` | integer | registry 选取、identity 断言 |
| `registry` | object | Chainlist snapshot 与发布副本 |
| `selection` | object 或 `null` | 固定 voting basis 与可重建的筛选结果；registry 不可用时为 `null` |
| `reference` | object | 客户端 reference 配置（tag 仍是 provider report） |
| `timeouts` | object | runner 执行边界 |
| `limits` | object | registry 抓取资源边界 |
| `plan` | object | 固定请求计划与断言 |

`environment`：

| 字段 | 生产用途 |
| --- | --- |
| `kind` | `ci` 或 `local`；`local` 只能作为诊断，不能当作外部 live evidence；`local` 时 `ci` 为 `null` |
| `runner_class` | `github-hosted`、`self-hosted`、`local`；attestation 说明用 |
| `ci.provider` | 定位 run |
| `ci.repository` | 定位 run/artifact |
| `ci.workflow_ref` | 定位 workflow 与 ref |
| `ci.run_id` | 关联 run 页面和 artifact |
| `ci.run_attempt` | 区分重跑 |
| `ci.job` | 定位 artifact 生产者 |

`registry`：

| 字段 | 生产用途 |
| --- | --- |
| `status` | `ok` 或 `unavailable`；决定是否进入 selection |
| `url` | 抓取目标 |
| `fetched_at` | 抓取时间 |
| `raw_sha256` | 原始字节 digest；只证明抓取内容，不是签名 |
| `artifact` | 发布的安全副本路径 |
| `artifact_sha256` | 发布副本 digest；第三方据此验证副本 |
| `redaction` | `none` 或 `url_credentials`；解释 raw/artifact hash 是否可相互验证 |

`status == "unavailable"` 时，`raw_sha256`、`artifact`、`artifact_sha256` 为
`null`；`fetched_at` 记录尝试时间。

`selection`：

| 字段 | 生产用途 |
| --- | --- |
| `algorithm` | `chainlist-sorted-v1`；重建设择 |
| `min_members` | 固定 3；不足即 `insufficient_members` |
| `max_members` | 固定 12；限制运行成本与公开端点负载 |
| `candidate_count` | 目标 chain 的原始 RPC 条目数；校验 selected+filtered |
| `selected[]` | 固定 voting basis；`source_id`、`url`；按顺序注册为 node-01… |
| `filtered[]` | `url_sha256`（无 URL 时为 raw entry 的 SHA-256）+ `reason`；审计过滤且不泄露凭据 |
| `voting_basis_sha256` | 校验 run 内分母未变 |

`reference`：

| 字段 | 生产用途 |
| --- | --- |
| `policy` | v1 固定 `finalized`；配置 `WithFinalizedAnchoring` |
| `max_lag_blocks` | 配置 finalized lag bound；32 为现有默认 |

`plan`：

| 字段 | 生产用途 |
| --- | --- |
| `id` | 定位固定 plan |
| `sha256` | 校验 plan 未被手改 |
| `steps[].id` | report 关联和 reason 定位 |
| `steps[].phase` | 执行阶段与报告分组 |
| `steps[].path` | `client_call`、`typed_block`、`typed_receipt`；选择执行器 |
| `steps[].method` | 逻辑方法 |
| `steps[].params` | 逻辑参数；唯一占位符 `$reference.hash` |
| `steps[].expect.decision` | 是否要求 rpc.Decision |
| `steps[].expect.statuses` | 允许的 decision status |
| `steps[].expect.error` | `none`、`not_found`、`invalid_request` |
| `steps[].expect.reference` | `none` 或 `corroborated` |
| `steps[].expect.binding` | `none`、`reference_hash`、`reference_height` |
| `steps[].expect.value_kind` | 期望 value 形态；`none` 表示该步骤没有 decision value；shape drift 的对照 |

`registry.status == "unavailable"` 时，`selection` 为 `null`，
`voting_basis_sha256` 不存在；report 只记录 registry 失败证据和
`inconclusive` 状态，不执行 plan。`plan`、`timeouts`、`limits` 和 `reference`
始终存在。

Manifest 生成纪律：

- 所有字段由程序从输入、registry 和固定常量生成；人工和 AI 只能审阅，
  不得填写后再作为 run 产物。
- `plan.sha256`、`voting_basis_sha256` 和 `manifest_sha256` 必须可重算。
- 不写入历史 golden，不把 live 响应写进 `internal/rpcsample/testdata`。

---

## 9. Report 契约

Schema 版本：`ethguard.live-report/v1`。示例见
`docs/design/live-canary/report.example.json`；示例是合成形状，不是真实
run，也不是证据。

### 9.1 顶层字段

| 字段 | 类型 | 生产用途 |
| --- | --- | --- |
| `schema_version` | string，固定 `ethguard.live-report/v1` | 解析/版本分派 |
| `run_id` | string | 与 manifest 关联 |
| `manifest_sha256` | string | 绑定本次 manifest 文件字节 |
| `plan_sha256` | string | 与 manifest 的 plan 对齐 |
| `started_at` / `finished_at` | RFC3339 UTC | 运行窗口 |
| `duration_ms` | integer | 超时与审计 |
| `state` | `passed`/`failed`/`inconclusive` | release/观察门禁 |
| `state_reasons[]` | array | 解释非 passed 状态；`category`、`step_id`（pre-plan 失败为 `null`）、可选 `source_id`、脱敏 `detail` |
| `reference_step_id` | string 或 `null` | 固定指向 `reference.finalized-balance`（run reference 来源）；未建立 corroborated run reference 时为 `null`。其他步骤的 step reference 不改变此字段。 |
| `steps[]` | array | 已执行步骤的完整证据；提前结束时可以少于 plan |
| `source_health[]` | array | 限流/不可达/延迟分类与审计；client 未建立时为空 |

`source_health[]` 字段：`source_id`、`url`、`successes`、`failures`、
`rate_limited`、`protocol_server_errors`、`p95_ms`、`last_error_class`。
`last_error_class` 是分类，不是原始错误字符串。

### 9.2 step 字段

| 字段 | 生产用途 |
| --- | --- |
| `id` | 与 plan 对齐 |
| `phase` | 与 plan 对齐 |
| `path` | 执行器 |
| `method` | 逻辑方法 |
| `logical_request` | 调用方请求（替换占位符后的逻辑值） |
| `prepared_request` | 观察到的 fan-out 请求；typed-only 步骤可为 `null`，但 `client_call` 步骤必需 |
| `reference` | 该步骤自己的 step reference evidence；不需要时为 `null`。`reference.finalized-balance` 的这一对象同时是 run reference；feeHistory 的仅是该步骤的 step reference。 |
| `decision` | decision 证据；typed-only 为 `null` |
| `typed` | typed adapter 结果；无 typed 路径为 `null` |
| `invariants` | §6 的布尔/`null` 结果 |
| `outcome` | `ok`、`degraded`、`provider_inconclusive`、`invariant_failed` |

`outcome` 是投影，不是第二事实源：

- `invariant_failed`：任一 failed 不变量/分类；
- `provider_inconclusive`：任一 inconclusive 分类；
- `degraded`：期望结果达成，但存在少数派 `reject` 或语义/形状 fault；
  不包含可用性 fault；
- `ok`：期望结果达成、无 fault、无 reject。

`business.fee-history.finalized` 的 `reference` 是 step reference；它可以与
顶层 run reference 不同。示例中两者相同只是合成 run 的巧合，report 解析和
后续断言不得把它们合并或要求相等。

### 9.3 decision 字段

| 字段 | 生产用途 |
| --- | --- |
| `status` | 允许集合检查 |
| `error_class` | 分类；成功时为空 |
| `error_detail` | 脱敏、限长的诊断；不得作为裁决依据 |
| `n` | 固定分母快速核对；必须等于 `len(participants)` |
| `participants` | 本次 voting basis，按 manifest 顺序 |
| `support` / `reject` | 聚类结果 |
| `faults` | source-attributed faults：`source_id`、`category`、脱敏 `detail` |
| `collection_faults` | pool-level faults：`category`、脱敏 `detail` |
| `representative` | 代表源；必须是 support 第一个 |
| `value_sha256` | canonical Value 的 SHA-256；用于 pinned 稳定性与跨 run diff |
| `value_kind` | 实际 value 形态：`quantity`、`data`、`array`、`object`、`null`、`bool`、`string`；shape drift 依据 |

`reference` 对象字段：`status`、`tag`、`height`、`hash`、`origin`、
`observed_at`、`n`、`participants`、`support`、`reject`、`faults`、
`collection_faults`。`faults` 是 source-attributed；`collection_faults` 是
pool-level。

`typed` 字段：`kind`（`block`/`receipt_absence`）、`ok`、`height`、`hash`、
`error_class`。typed 结果必须与 reference 和对应 decision 一致。

### 9.4 生成纪律

- report 必须由本次 manifest、响应和 evidence 直接生成；不得由 AI 或人工
  填写 expect，不得事后修改后再上传为通过结果。
- report 必须携带 `manifest_sha256` 和 `plan_sha256`；缺失或不匹配即
  `failed`（`evidence_missing`/`schema_error`）。
- 失败和 `inconclusive` 也必须生成 report，保留已有证据；不得只输出
  stderr 或空 artifact。例外：manifest 本身无法生成或通过 schema 校验时，
  不要求 report；workflow 必须失败并保留诊断。
- 不把实时值写入仓库 golden；report 中的 value 只以 digest/kind 和脱敏
  证据出现，完整原始响应留在受控 artifact 中。
- 人工只能审阅 artifact；任何人工改写都必须使 attestation 失效。

---

## 10. 观察与证明的边界

一次 live run 可以说明：

- 在时间 T、registry snapshot R、固定成员 M、commit C、plan P 下，生产
  路径完成了哪些请求；
- 每个 decision 的 status、denominator、support/reject/fault 和 reference；
- prepared request 是否绑定到本次 reference；
- 哪些 provider 在本次观察中限流、不可达、超时或返回了不同形状；
- 本次 run 的失败分类和 artifact digest。

一次 live run **不能**说明：

- Ethereum consensus、finality、canonicality 或 light-client 验证；
- provider 的独立性、诚实性或未来可用性；
- 某个值在链上“正确”，或某个 tag 的真正 finality；
- 没有发生 uniform stagnation、colluding majority、provider tag downgrade
  或未来时刻的漂移；
- 本地 replay/capture 的 provenance 已被补全。

Attestation：

- 只有外部 runner 生成并发布带 digest 的 artifact，才可称为
  **external live observation**；本地运行只是诊断。
- GitHub-hosted runner 证明执行环境脱离作者本机，不证明独立审计；需要更强
  第三方属性时，允许独立 workflow/repository 按同一 manifest 重跑。
- digest 不是 provider 签名，也不是不可篡改证明；它只绑定发布内容。
- `inconclusive` 可以在 artifact/UI 中显示为“未得出结论”，不得显示为
  passed，也不得作为 release 成功条件。

---

## 11. 非目标与禁止范围

- 不修改生产 reference policy；`finalized` 仍是 provider-reported tag。
- 不修改 `expect/v1`、`batch.json`、capture record 或历史 golden。
- 不新增 RPC 方法，不新增 provider 权重，不做动态成员替换。
- 不修 WSS presence，不生成 WSS 四态 golden（决策 0056 继续有效）。
- 不用 live 测试替代离线 regression；不把 live 输出复制成新的 golden。
- 不引入第二套业务裁决、第二事实源或 `ConfirmationSnapshot`。
- 不把 pinned `ReferenceContext` 注入 resolver，不引入 `MockChainState`，
  不用 Chainlist 实时成员替代固定分母。
- 不承诺检测 uniform stagnation、colluding majority 或 provider tag
  downgrade。
- 不在普通 `go test` 中触网；live 只能在独立 job/命令中运行。
- 不把一次成功 run 推广成长期可靠性、provider 独立性或协议最终性证明。
- 不通过修改历史 capture、手填 expect、降低门槛或缩小分母来“变绿”。

---

## 12. T10.0 出口与后续

T10.0 的产物：

1. 本契约；
2. `manifest.example.json` 和 `report.example.json`；
3. 决策 0059；
4. roadmap 的 T10.0 状态记录。

后续按 T10 行动规划表逐行执行。T10.1 必须先实现 manifest schema、生成器和
schema/脱敏测试；在维护者审计 T10.0 之前不得开启 T10.1。
