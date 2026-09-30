# 方法契约

状态：增量收敛中的方法语义契约；以现有实现为事实基准，“待明确边界”是待裁决问题，不是已接受决定。

节奏：按 [rpc-governance-discussion.md](rpc-governance-discussion.md) 第 5 节商定（2026-09-07）：每个方法回答其第 2 节的六问完成事实整理，关键语义决定并通过验证后闭环，不要求整表齐动；第 6 节场景随契约推进推演。

定位：本文件管方法语义（共同参考、归一化、结果分类、失败语义）。请求形态可移植性见 [wire-forms.md](wire-forms.md)；裁决机制与既定决策见 [rpc-aggregation.md](rpc-aggregation.md) 与 [../decisions.md](../decisions.md)。

目录职责和聚合/清洗边界见 [architecture-record.md](architecture-record.md)。本文件只记录方法契约，不再承担开发任务清单。0.x 节标题里的“实现待完成”是 2026-09-07 的快照措辞，不再逐节维护——实现状态以 [../decisions.md](../decisions.md) 的 Status 行为准。

## 0. Strict 共同裁决语义

本文件全部 Strict 方法共享以下第 4 问答案，各方法条目只写偏差：

- 默认（未启用 quorum）：全部有效回答一致且至少两源 → Corroborated；单个有效回答 → Selected；两个及以上不同值 → ErrConflict（携带 Support/Reject 证据）；零有效回答 → ErrNoValidResponse（[rpc/strict.go](../../rpc/strict.go)）。
- 显式 quorum（WithQuorum(n)）：获胜簇需 size ≥ H 且 2·size > N，N 为本次观察集合——含入选后失败与未获本地并发槽的成员；size == 1 永不 Corroborated（决策 0006）。
- 分母时间边界：请求前已过滤的来源不进入本次分母；入选后失败的来源保留；下一请求重新筛选（治理文档第 3 节）。
- 已迁移方法（方法规则索引的 result 为 canonical）的 Value 是规范编码，不随代表源拼写变化。数量和成功 DATA 的比较键就是规范 JSON。`eth_feeHistory` 的比较键仍是方法现有的 feeHistory 键，Value 是受保护字段的规范对象（0042）。`eth_getLogs` 的比较键仍是现有的 `normalizeLogs` / `PruneMethod` 结果，Value 是掩码留下的日志字段的规范数组（0043）。`eth_createAccessList` 的比较键仍是现有的 `normalizeAccessList` / `PruneMethod` 结果，Value 是掩码留下的 access list 字段的规范对象（0044）。`eth_getTransactionReceipt` 的比较键仍是现有的 `normalizeReceiptObject` / `PruneMethod` 结果，Value 是受保护收据字段的规范对象，`from` 不进 Value（0045）。`eth_getBlockReceipts` 的比较键仍是现有 `normalizeBlockReceiptList` / `PruneMethod` 数组键，每项排除 `from`；Value 复用收据字段模型编码为规范数组，顶层 null 与空数组分别保留（0046）。其余 Strict 方法的 Value 仍是获胜簇里捕获顺序第一个成员的原始回答：确定性，但不是“多数负载”，也不承诺由多数派提供。Representative 仍是这一人。
- 类型化读取（ethadapter）以 refcontext.RequireCorroborated 强制佐证，Selected 不静默替代（[ethadapter/client.go](../../ethadapter/client.go)、[aggregator/aggregator.go](../../aggregator/aggregator.go)）。
- Corroborated 表示方法策略得到满足的 provider-backed cross-source evidence，不是 Ethereum consensus，也不是密码学结论（治理文档第 2 节）。

## 0.1 已接受边界：完整区块内容保护

状态：2026-09-07 已接受设计边界，见 [决策 0019](../decisions.md#0019-complete-block-content-is-bound-to-the-corroborated-block-identity)；实现与真实 RPC 兼容性验证待完成。下方六问表仍描述当前实现，不表示已满足本条。

返回完整 Block 的接口必须保护其必要业务内容。多源佐证区块身份，返回内容按协议编码计算承诺并与该区块头对照：header hash、交易 trie root、适用时的 withdrawals root，以及使用完整历史叔块头计算的 uncle hash。不是对原始 JSON 做 keccak256，也不能只计算 header hash 就认定体已验证。

无法取得或验证的必要内容，不得静默省略后成功返回。供应商附加字段不自动受头部承诺保护，其处理仍待逐字段明确。裁决前准入及无效回答处理见第 0.2 节，具体实现待完成。

验证要求：固定同一块，对真实多源 byNumber/byHash 回答检查编码与字段兼容性，覆盖不同交易类型、空块、withdrawals 和历史叔块；本地篡改测试覆盖替换、删除、重排内容而保留原头部，纯表示差异应在解码后兼容。记录性能，不预设固定开销。现有 soak 的请求形态验证不等于本条已验证。

本条不要求完整 EVM 重执行或轻客户端；内容与头部一致仍不证明区块属于规范链，也不消除多源共同伪造身份的风险。

T04 已用离线样本锁住现有检查，没有把本条收成完备性结论：`internal/rpcsample/testdata/block-receipt-tx/2026-09-23` 含空块（空交易、空 uncle）、带 withdrawals 的头、单笔 transaction / receipt、空 receipt 数组，以及 block / receipt / transaction 的 null。白名单外字段不制造分歧，缺必填字段进不了准入，另一块的内容在绑定处失败。空 uncle 列表对空 uncle root 合法，空 receipt 数组对空块合法；这是单份响应的完整性，不是完备性结论。历史叔块样本未采集。测试：`TestRealShapesAgreeAfterPruneAndCaseFold`、`TestMissingRequiredFieldsStayOutOfAdmission`、`TestEmptyBlockUnclesAreLegalAndForeignBlockIsRejected`、`TestReceiptFromAnotherTransactionIsRejected`、`TestTypedRealHeaderRejectsAnotherBlocksContent`、`TestTypedReceiptFromAnotherHashIsMalformed`。

## 0.2 已接受边界：先准入，再裁决

状态：2026-09-07 已接受，见 [决策 0020](../decisions.md#0020-method-admission-rules-precede-adjudication)；实现待完成。

- 请求在本地校验参数类型、范围、互斥条件及允许标签。
- 每份回答先检查必要字段、类型、长度、范围、请求绑定和适用的内容承诺，合格后才贡献支持票。缺失、null、空值分别定义。
- 仅归一化契约明确列举的等价表示，不猜测补字段或自动转换类型。真实供应商差异用于评审兼容规则，不自动放宽规范。
- 无效回答不贡献支持票，其来源仍保留在本次 quorum 分母中。有效回答不足以满足裁决条件时拒绝；故障是否改变后续健康／资格另行裁决。
- 未纳入保护契约的字段不得静默进入受保护业务对象，可作为第 0.3 节的附加证据保留。

## 0.3 已接受边界：单次调用的 Uncertain

状态：2026-09-07 已接受语义边界，见 [决策 0021](../decisions.md#0021-uncertain-preserves-untrusted-data-from-an-individual-invocation)；获取方式已按第 0.17 节确定，具体数据结构与实现待完成。

**Uncertain 提供上游数据用于兼容，不负责业务处理，不保证可靠性。** 它是本次调用结果的附加产物，成功或失败均可携带；不是额外 RPC 查询，也不是 client 的“最近一次回答”。读取它不改变主结果的接受规则、状态或错误。

按字段路径归集，每个路径下用数组独立封装各来源的值。例如（仅示意，非最终 API）：

```json
{
  "vendorField": [
    {"source": "rpc-a", "value": "value-a"},
    {"source": "rpc-b", "value": null}
  ]
}
```

- 不设置 `presence`、`reason`。字段缺失则没有该来源的条目，显式 null 保留条目。来源是否回答、为何拒绝，由本次调用的来源记录、错误与诊断证据表达。
- 保留原始值，不作数值转换、归一化或同值合并；相同值也保留独立来源条目。数组顺序稳定，不表示可信排名。
- 请求与区块参考归属于本次调用的公共上下文，不要求逐值重复。
- 整份无法解析或结构不合格、不能按字段归集的响应，也按来源独立保留在原始响应集合中。不修复、不解释，原始数据格式不合格不应阻止获取这份附加产物。
- 不保证完整收集。没有某个字段条目不能推导为所有来源均无此字段；仅提供本次可保留的数据。
- 未纳入契约的厂商扩展、未接受或未获佐证的内容，可由此提供；其中的一致不表示可靠，异见也不表示已证伪。

必要字段不合格时，该来源回答仍不能投票，且仍保留在本次 quorum 分母中。不能把坏字段移入 Uncertain 后把剩余对象作为完整成功结果；独立接受字段子集必须有方法契约依据。决策 0039 规则 4e 的删减式投影是已冻结的这类契约：只删除未佐证字段，不把残缺对象说成该字段已获佐证。`eth_call` 的 revert `data` 已按此投影；未声明的方法仍返回来源原值。见 `method-verification-pipeline.md` 第 1.1 节。主结果的校验责任不因保留原始数据而改变。

获取方式已按第 0.17 节确定为 context 收集器；具体 Go 类型、字段路径语法、原始字节表示及保留容量待设计。需核对传输／解码路径能否暴露原始响应，当前尚未实现该能力。`Uncertain(paths ...string)` 仅为候选表达，不是已接受签名。

## 0.4 已接受边界：null 沿用官方语义

状态：2026-09-07 已接受，见 [决策 0022](../decisions.md#0022-preserve-standard-null-semantics-and-adjudicate-mixed-answers-normally)。区块、receipt、transaction 的 JSON null 已是可比较回答，类型化边界映射为 `ethereum.NotFound`；`eth_getLogs` 的 null 仍是非法回答。测试：`TestNullAnswersStayDistinct`、`TestTypedNullAndEmptyReceiptsStayDistinct`、`TestTypedLogsNullIsMalformed`。

对方法契约允许 null 的对象查询，null 是合法回答。区块查询在 RPC 层保留 null，接受该结果后类型化接口按 Geth 惯例映射为 `ethereum.NotFound`。不新增缺失状态，不猜测同步／裁剪等原因，不补成空对象，也不因合法 null 本身认定来源异常；NotFound 不构成不存在证明。

多源选择与 null 的含义分开：默认 Strict 对 null／有效对象混合返回 ErrConflict；显式 quorum 可接受达到既有门槛的任一簇。单个有效 null 最多为 Selected，不能绕过类型化读取的佐证要求。不因出现一个 null 就提前结束，也不因其他来源有对象就忽略 null。未接受回答按 Uncertain 边界保留。

缺字段、坏格式和方法不允许的 null 仍属准入失败，不与合法 NotFound 混同。区块、receipt、transaction 的 null 与空数组不是同一个键；日志 null 不进入比较。这不是 provider 失败，也不是 Unknown 业务裁决；类型化失败返回时 Status 为 Unknown。

## 0.5 已接受边界：revert 数据保护与兼容验证

状态：已实现，含决策 0039 规则 4e。`eth_call` 用方法专属 resolver，不走 generic Strict。执行维度比较成功结果与 revert，成功返回数据留在执行键里。revert 的 `data` 可弃权：缺失与 JSON null 是弃权，不是空字节；空字节（`0x`）、非空 hex 与非 hex JSON 仍是不同的 data 断言，比较键不把它们并成同一个值。弃权源留在投票分母，不进入 data 的 Reject，也不用其他来源的值补全。具体 data 达到门槛才进入 `Decision.Value`；短簇或一致性模式下的 data 分裂保持已佐证的 revert，并从一份已准入信封删掉 `data`。`DeletedFields` 只记录这次删掉的字段。被删字节留在 `Decision.Evidence`，不作为已佐证字段。厂商 code（3 与 -32000）和 message 不进入比较键，只保留在这份信封上，不表示文案已获佐证。传输失败、-32002、-32003、-32603 和限流仍是 fault，不进入 revert 键。`RevertError.Data` 只承接已返回的 hex；删减后的 revert 没有 data，`ErrorData` 为 nil，不伪装成空字节。结构化 data 在被返回时留在 `DataRaw`。

见 [决策 0023](../decisions.md#0023-revert-data-protection-precedes-provider-error-normalization) 与 [决策 0039](../decisions.md#0039-treat-partial-information-absence-as-abstention-rather-than-conflict)。测试：`TestNormalizeCallOutcomeClustersResultsAndReverts`、`TestNormalizeCallOutcomeSeparatesRevertDataStates`、`TestNormalizeCallOutcomeSameWordingDifferentDataDisagrees`、`TestCallRevertDataConflictIsNotSwallowed`、`TestCallProviderErrorDoesNotEnterRevertKey`、`TestCallRPCErrorRawPreservesDataAbsence`、`TestDecodeCallResultKeepsRevertDataStates`、`TestRevertSamplesUseDistinctDataKeys`、`TestRevertDataSamplesReplayWithoutEndpoints`、`TestCallRevertRule4eMatrix`。样本：`internal/rpcsample/testdata/revert-data/2026-09-23`。该样本只证明 code 3 下缺失、`0x`、`0xdeadbeef` 与 `Error(string)` 同时出现，不是普遍性结论；code -32000 与结构化 data 由单元测试锁定，不在这次样本里。

样本中同一请求（state override 注入 `revert(0, 0)`）s1 四次均缺失 data，s2 三次返回 `0x`、一次缺失；同一端点在相同请求上都不稳定，样本已脱敏，不能归因到具名 provider 或客户端。两源、floor 2 时，缺失对 data 弃权，单独的 `0x` 达不到 data 门槛，结果是已佐证的 revert 且没有 data，不是 `ErrConflict`。缺失与 `0x` 仍是不同比较键。双方都给出同一具体 data 时，该 data 随 revert 一起返回。

code 与 message 来自一份已准入信封，不跨来源拼装。未佐证的 `data` 只删除，不改写成别的值。`eth_call` 仍固定同一区块参考与执行参数后比较。同意是 provider-backed evidence of the returned dimensions，不是对 message 的佐证，也不是 Ethereum consensus。

## 0.6 已接受边界：标准字段与厂商扩展

状态：2026-09-07 已接受，见 [决策 0024](../decisions.md#0024-standard-method-fields-define-the-protected-result)；逐方法字段清单与实现待完成。

核心字段以适用的以太坊 Execution API 与协议规则为依据，明确方法、参数及分叉／版本条件，不以供应商返回频率决定是否必要。

- 必要字段缺失，该来源回答不合格。
- 协议允许的可选字段按其具体条件处理，不能一律忽略。
- 未纳入保护契约的厂商附加字段进入 Uncertain，不因字段集合或附加值不同而制造主结果冲突。
- 不从其他来源补齐缺失字段后，拼成一份有效回答。方法契约明确的后续取数及承诺校验（例如取得叔块头）不属于无依据拼接，需独立定义验证步骤。

当前整对象比较仍可能因附加字段不同而冲突；本条是目标边界，不是现有实现事实。

## 0.7 收尾检查：对照以太坊轻客户端规则

用户要求：所有语义与治理边界确定后，对完整方案进行一次以太坊轻客户端规则对照，检查是否存在违反或不兼容。此检查待执行，不在本轮提前宣称通过。

对照须记录规范版本及相关分叉，区分适用要求、不适用项、冲突与未决项，附依据。涉及可信根／checkpoint、头部与内容承诺、finalized／optimistic、新鲜度及缺失数据处理时，逐项说明。RPC 格式兼容或多源一致不等于轻客户端规则已经满足；本检查不代表批准引入轻客户端。

## 0.8 已接受边界：一次逻辑调用固定投票基准

状态：2026-09-07 已接受，见 [决策 0025](../decisions.md#0025-freeze-the-voting-basis-within-one-logical-invocation)；跨阶段实现及缓存衔接待完成。

对需要佐证的读取，在调用开始、建立参考之前确定本次投票集合及门槛。参考建立、按需确认与业务读取沿用该基准，不因超时、不可用、无效回答或调用期间的健康／新鲜度变化缩小分母、降低所需支持；无法满足则拒绝。

投票资格与实际请求分开：阶段内可复用证据、按需只请求部分成员，但不能据此重算为更小的投票基准。重复请求不增加票数，故障换源不能静默替换本次投票成员。参考支持与业务值支持分别记录，不相互充当支持票。

调用开始的成员及跨调用变化已按第 0.10 节确定为使用方显式管理；硬下界取值继续待决，跨调用参考缓存已按第 0.9 节退出本轮目标范围。它不把估计、本地观测改成 quorum，也不改变 Strict／显式 quorum 的方法语义。

当前 Collect 保持单次收集集合，finalized 确认保持初轮参考分母；贯穿参考与业务读取的调用级固定基准尚未实现。

## 0.9 已接受边界：每次调用重新建立移动参考

状态：2026-09-07 已接受，见 [决策 0026](../decisions.md#0026-establish-moving-references-afresh-for-each-invocation)。T05-1 已把调用内新鲜度与失败语义写成契约。

本轮不跨逻辑调用缓存区块参考或业务结果。方法需要移动参考时，每次向上游重新建立 H，再在该调用内部固定 H 完成读取；显式历史参考仍按其参数语义处理。连接可以复用。

调用内固定 H 解决比较期间参考移动，跨调用缓存 H 只是减少请求的优化，不是保证一致性的必要条件。真实请求也不保证上游已同步到全网最新状态；latest／safe／finalized 与新鲜度规则另行明确。

非零 `anchor_cache_ttl` / `WithAnchorCaching` 在构造期拒绝（决策 0026、0037）。`CachedAnchor` 仍保留，但不从 `Client` 接入，也不在本轮删除。每次调用重新建立参考，无跨调用复用、无 TTL；`ObservedAt` 是本次建立时刻。失败是 Prepare 期错误，包裹 anchor 原因，业务请求零 fan-out；证据 `Status=Unknown`，并保留 `Faults`。以后仅在请求成本与限流实测有充分依据时，重新评估显式可选缓存。测试：`TestReferenceAnchorFailureKeepsPrepareErrorAndSkipsBusinessFanout`、`TestReferenceEstablishedFreshPerCall`、`TestReferenceFailureEvidenceIsUnknownWithObservedAt`。

## 0.10 已接受边界：使用方显式管理投票成员

状态：2026-09-07 已接受，见 [决策 0027](../decisions.md#0027-caller-managed-membership-is-independent-of-health-scheduling)；实现对齐待完成。

配置修改及运行时主动 add 等显式成员操作均可改变投票集合。变化对后续调用生效，进行中的调用保留开始时的集合与门槛。健康、限流、失联及新鲜度过滤只影响请求调度，不自动删除投票成员或缩小多数分母；恢复后可再次请求。

来源选择、准入及控制成员操作的职责属于使用方。库不证明 URL 控制者独立，也不负责抵御使用方通过配置或成员接口注入大量恶意身份；新增成员不代表它更可信。仍需执行常规身份去重和方法准入规则，不允许上游回答自动产生投票成员。

例如 5 成员、显式 quorum 硬下界 3：2 源冷却，其他 3 源有效一致可以接受；3 源冷却，仅剩 2 源一致则拒绝。长期失效需使用方维护成员，不能靠健康机制自动删成员恢复成功。

本条不确定默认策略或硬下界。当前默认 Strict 仅要求有效回答一致，固定成员本身不会自动使它具备多数门槛；默认方向已按第 0.11 节接受，硬下界默认值待定。当前 eligible 收集路径仍待改造。

## 0.11 已接受方向：固定多数与可切换裁决模块

状态：2026-09-07 已接受，见 [决策 0028](../decisions.md#0028-default-corroboration-uses-fixed-membership-majority-with-unit-votes)；默认硬下界、具体 API 及实现待定。

需要跨源佐证的方法，默认采用固定成员集合严格多数：`Q = max(H, floor(N / 2) + 1)`；Corroborated 另需至少两个支持源。每个成员一票，健康、响应速度不增加权重。H 默认值已按决策 0029 接受为 2；N=1 不能获得 Corroborated，用户主动单源使用的转发模式及切换条件见第 0.13 节。

有效回答全体一致可保留为显式模式，同时要求最低支持数，不能仅因少数幸存者一致就成功；该模式已按决策 0029 确定为同样的 Q 门槛再加全部有效回答一致。模式切换不改变方法准入、归一化和内容保护规则；估计、本地观测及转发不因此统一变成投票。

裁决逻辑独立为可切换模式的模块：方法契约负责回答含义与准入，pool 负责调度与传输，裁决模块基于固定投票基准和合格支持证据输出可复现决定。具体包名与接口待设计，现有 `rpc.Policy`／`Strict` 是可利用的接口基础。

所有票权为 1 不代表任意共识协议均适用；新增模式仍须说明故障与交互前提，不能假定普通 RPC 来源参与验证者通信或签名协议。本轮未接受其他具体算法。当前代码默认 Strict 及 eligible 分母尚未改变。

## 0.12 已接受边界：默认支持下界为 2

状态：2026-09-07 已接受，见 [决策 0029](../decisions.md#0029-default-support-floor-is-two-for-corroborated-reads)；实现待完成。

佐证读取默认 H=2，仍必须满足固定集合严格多数。N=1 无法 Corroborated；N=2/3/4/5 分别至少需 2/2/3/3 个有效同值支持。H=2 不是容忍两个恶意源的承诺，使用方可提高 H，并明确实际 Q 与恶意预算的关系。

显式全体有效回答一致模式使用相同 Q 下界，另要求所有有效回答一致，不因少数幸存者一致而降低支持要求。

用户主动单源使用的转发边界已按第 0.13 节接受，不自动把多源调用的一个幸存源当作单源模式。

## 0.13 已接受边界：主动单源模式与配置责任

状态：2026-09-07 已接受，见 [决策 0030](../decisions.md#0030-intentional-single-source-mode-and-trusted-configuration-control)；API 与实现待完成。

初始化仅配置一个投票源且未指定模式时，默认单源转发；显式指定 quorum 而配置不足则报配置错误，不擅自转发。多源运行期间，健康源减少或主动删成员至一个都不自动切换模式，支持不足则拒绝。用户可主动切换模式，从后续调用生效，当前调用保持原模式及集合。

单源模式继续执行已支持方法适用的格式、请求绑定与内容校验，结果为 Forwarded，不能 Corroborated；仍可提供 Uncertain。ethadapter 在明确单源模式下可返回通过本地校验的结果，当前强制佐证入口尚待调整。未知方法不因单源模式获得原本不存在的专用校验保证。

合法配置和主动管理操作属于可信控制输入，上游回答属于不可信数据输入。使用方负责配置权限、管理接口鉴权与进程／运行环境保护；库负责配置一致性检查、准确执行模式和如实表达保证。攻击者篡改配置并以单源重启超出 RPC 裁决的防御范围，库无法据配置本身区分攻击与用户意图。

启动记录模式、投票成员数及有效支持要求；转发模式明确没有佐证门槛。记录用于审计，不替代配置保护。责任边界同步见 [威胁模型](../threat-model.md)。

## 0.14 已接受方向：自适应拥塞控制保留容量余量

状态：2026-09-07 已接受方向与余量语义，见 [决策 0031](../decisions.md#0031-health-scheduling-includes-adaptive-per-source-congestion-control)；算法、参数及实现待验证。

按来源观测限流时长／频率并调整调度，目标是在估计的可承受容量之下留出余量，不是拥塞时仍强行维持最低业务请求频率。保守起步，有实际需求且持续正常时缓慢提高，明确限流时较快降低或暂停，恢复后不立即回到旧峰值；低负载下未限流不能证明高容量。

比例余量、慢增快减、请求节流与并发上限共同作为候选评估，具体算法与数值不在此锁定。参考建立、确认、业务读取和健康探测都计入来源预算，不得以内部请求为由绕过控制。

排队受调用截止时间约束，未取得发送资格记录为本地未发出，不处罚来源，不缩小投票集合／分母，不降低门槛或切换模式。合法 null、已识别 revert 和有效异见不触发健康处罚。调度历史可以保留，不等于跨调用缓存 H 或业务结果。

共享账号／跨端点配额、队列容量及具体恢复参数待设计；通过变化容量、混合方法成本与并发流量验证，不能承诺学习后永久稳定。

## 0.15 已接受方向：保持标签语义，优先稳定共同参考

2026-09-07 补充（决策 0036）：库统一完成标签感知的共同参考建立，内部保留请求标签、高度、hash 与对应证据。本轮完整迁移旧自定义 resolver 接口及注入点，不保留兼容适配；自定义能力在大部分计划任务完成后另议。可切换裁决模块保留。公开 ethclient 签名不变，不在此另定 tag 便捷 API。

项目特性定位为“保持标签语义的多源共同参考与调用内固定”。latest／safe／finalized 本身已是标准 RPC 标签（[EIP-1898](https://eips.ethereum.org/EIPS/eip-1898)）；本库增加的是多源选取、佐证和调用内固定行为。开发验证后写入 README，当前不宣称已实现或已独立验证链上最终性。

状态：2026-09-07 已接受，见 [决策 0032](../decisions.md#0032-preserve-block-tag-meaning-while-choosing-a-stable-common-reference)；落后容忍及实现对齐待完成。

显式 hash／高度不自动换块；latest 不暗中改为 finalized；safe／finalized 保留请求的安全级别，无法建立相应参考则拒绝。pending 保持节点待处理状态含义，不改写为已出块状态，也不因多源相同就宣称 Corroborated；逐方法支持方式另行列明。

2026-09-07 补充（决策 0035）：支持的 pending 方法可通过兼容类型化入口返回节点本地观测；未支持的方法明确报错，不以一律拒绝为目标。该路由由请求语义决定，禁止 quorum 失败后降级进入。适用本地准入仍须执行；方法矩阵、来源选择和可见分类待明确。

允许在同一标签语义内牺牲少量新鲜度以获得稳定共同参考，尤其 finalized 读取不追逐单源最高报告。最大允许落后多少、如何计量仍待定；不授权不断回退候选直到成功，也不恢复跨调用 H 缓存。

用户场景：finalized 报告 A=10000、B=9999、C=10001、D=9999。在固定 4 成员、H=2、Q=3 下，9999 可作为共同候选；10000 仅两个来源报告了 finalized 覆盖。A/C 报告较高高度并不自动算作对 9999 的 hash 支持，需取得候选高度的确认；最终要求足够来源同时满足 finalized 覆盖与同高 hash 一致。不同高度的回答不能直接合票。

已接受候选规则：选择达到所选模式所需 finalized 覆盖数的最高候选高度。固定多数模式下，将合格报告高度降序排列，取第 Q 个；不足 Q 份报告则拒绝，不能缩小固定分母或降低门槛。报告低于候选高度的来源不能为该候选贡献 finalized 覆盖。新鲜度判据按第 0.16 节另行讨论，不预设固定块数上限。

只确认该候选一次：已有同高合格证据可复用，较高报告源按需查询候选高度。支持者必须同时满足 finalized 覆盖与同高 hash 一致；按选定模式判断，quorum 允许门槛内的少数异见，显式一致模式遵守其额外一致要求。确认未通过或支持不足就拒绝本次调用，不逐次回退到更旧高度。不是任意一个异见都令 quorum 失败。

目标是稳定提供来源共同确认的区块，便于资产监听等应用；该证据仍不等于客户端独立验证了链上最终性，不扩展为跨链桥完整安全保证。当前按配置 resolver 改写移动标签的路径需逐项核对，不能据本条宣称实现已保持所有标签语义。

## 0.16 已接受边界：一致性与新鲜度分别判断

状态：2026-09-07 已接受原则，见 [决策 0033](../decisions.md#0033-separate-freshness-from-agreement-without-prescribing-a-fixed-lag)；新鲜度策略独立缓议。

单源最高高度不足以认定其他来源过旧，多数一致也不足以证明结果足够新。新鲜度规则不得静默改标签、改投票成员或降低门槛；适用的新鲜度要求一旦明确，未满足就拒绝。

用户明确：允许落后多少及如何确定可另行讨论，不要求现在设置，也不限定为固定值。静态块数、动态判据及默认策略均未接受，不将这些参数作为通用裁决边界收敛的前置条件。

原则仅用于具有新鲜度要求的方法／参考；显式历史读取不能因为目标区块旧就被拒绝。第 0.15 节 finalized 候选规则保持，当前 tip／finalized 过滤参数仍是待核对实现事实，不代表本轮已接受其为默认新鲜度契约。未修改代码。

## 0.17 已接受入口：Context 收集器获取 Uncertain

状态：2026-09-07 已接受 API 方向，见 [决策 0034](../decisions.md#0034-opt-in-uncertain-capture-uses-the-invocation-context)；收集器类型、访问方法与实现待完成。

普通调用保持原 context、方法签名和 Geth 返回类型。需要扩展数据的用户显式为一次逻辑调用创建收集器：

```go
callCtx, capture := ethguard.WithUncertain(ctx)
block, err := client.BlockByNumber(callCtx, number)
// 返回后，通过 capture 获取本次保留的字段及原始响应。
```

携带收集器即按需启用，不再要求另设初始化 bool；初始化回调、自定义 Result、客户端最近一次状态均非本次选定的业务获取方式。上例仅确定入口形式，访问方法不照搬外部伪代码锁定。

一个收集器对应一次顶层逻辑调用，内部参考、确认、业务读取记录按阶段／请求／来源区分；同源多次原始响应不能因按来源单键存储而覆盖。各独立调用创建新收集器。context 可跨 goroutine 共享，不天然隔离调用；实现须同步内部并发写入，防止重复使用收集器静默串数据，误用时的具体返回规则待设计。

无论成功或失败，公开方法返回前发布本次已保留数据的稳定视图，返回后不被迟到任务修改。取消时只提供已取得的数据，不承诺完整收集。公开可变 map 无锁赋值的示例不能直接用作实现。未启用收集器时不额外保留完整 Uncertain 负载，正常校验和诊断不受影响。

已接受只读边界：Uncertain 是返回数据，只提供读取能力，不提供公开修改操作。可变构造状态留在内部，各来源／阶段独立收集，调用完成时统一归集发布，避免逐字段竞争共享 map；预算协调、取消与发布仍须正确同步，不承诺完全无锁。

发布后允许并发读取同一稳定结果。访问方法不能暴露内部 map、嵌套 slice、原始字节缓冲或可变指针的别名；使用不可变表示或必要的防御性复制。即使用户修改取出的副本，也不能影响存储结果和其他读者。传输缓冲复用和迟到任务不得修改已发布数据。

保留容量必须在收集期间受限，不能收齐大响应后才截断。通过只读结果元信息让用户知道发生截断／保留省略，不新增逐字段 presence/reason 分类。具体限额及保留策略通过测量确定。附加数据预算耗尽不改变主结果校验、裁决或错误语义，不使原本有效的主结果仅因 Uncertain 容量不足而失败。

测试需覆盖并发读取、返回缓冲的修改隔离、迟到任务／取消、大区块／日志／错误负载；分别测量启用与未启用时的分配和锁竞争。具体只读访问类型仍待设计。

交付要求：开发与验证完成后，在 README 介绍该特性，给出普通调用、context 启用、成功／失败后读取字段和原始响应的真实可编译用法，并说明单次归属与不保证可靠性。完成前不作为已实现特性宣传。验证覆盖并发、阶段隔离、失败／取消、返回后稳定性及原接口兼容。

## 1. 首批：链常量与区块读取

### eth_chainId

| 六问 | 契约 |
| --- | --- |
| 1 结果类别 | 确定性事实：链常量，不随区块参考变化 |
| 2 共同参考 | 无区块参考，无参数改写，比较前提仅“来源同链”。pool 侧另有拨号后一次性身份预检（配置链 ID 不符即永久除名，[pool/source.go](../../pool/source.go)），与逐次跨源裁决是两层互补的身份检查，证据与生命周期各自独立 |
| 3 归一化 | hex quantity → 数值字符串，等效拼写（0x1/0x01）一致；非字符串、缺 0x 前缀、坏十六进制 = 无效回答（Fault）；JSON null 同样无效，不是可比较的“不存在”（[rpc/methods/normalize.go](../../rpc/internal/wire/normalize.go)） |
| 4 结果分类 | 无方法偏差，见第 0 节 |
| 5 失败语义 | 跨源不同 chainId = ErrConflict（混入异链来源或配置错误）；来源失联只缩小分母；身份预检失败在 pool 层除名，不经由本方法 |
| 6 成本 | 单次对称 fan-out；无锚定、无缓存、无参数 |

待明确边界：无。

### net_version

| 六问 | 契约 |
| --- | --- |
| 1 结果类别 | 确定性事实：链常量的 legacy 十进制拼写。net ID 与 chain ID 是不同字段（分叉链可同 net ID 异 chain ID），语义不可互换 |
| 2 共同参考 | 无区块参考。pool 身份预检走 eth_chainId，与 net_version 裁决相互独立 |
| 3 归一化 | 十进制字符串 → 数值字符串，独立编码器、不与 hex quantity 共享归一化（[rpc/methods/net_version.go](../../rpc/methods/net_version.go)）；null 或非数字 = 无效 |
| 4 结果分类 | 无方法偏差，见第 0 节 |
| 5 失败语义 | 同 eth_chainId：跨源不一致 = ErrConflict |
| 6 成本 | 单次 fan-out；无参数；请求形态已由 census 验证（[wire-forms.md](wire-forms.md)） |

待明确边界：无重大项。net ID 与 chain ID 的语义差异由调用方自行承担——类型化 NetVersion 只返回数值，不解释两者关系。

### eth_getBlockByHash

| 六问 | 契约 |
| --- | --- |
| 1 结果类别 | 确定性事实：hash 寻址的不可变内容 |
| 2 共同参考 | 请求参数即参考：hash 定死内容，无锚定、无参数改写；fullTx 布尔透传（[rpc/methods/eth_getblockbyhash.go](../../rpc/methods/eth_getblockbyhash.go)） |
| 3 归一化 | 整对象 canonical：递归小写 0x 串、键序擦除，其余逐字比较——良性编码差异全部抹除后仍不同的，就是异见（normalizeCanonicalObject）。JSON null 归一为 "null"，是可比较值 |
| 4 结果分类 | 第 0 节之外：null 与对象是两个不同值。null 簇满足第 0 节佐证条件后，类型化解码映射 ethereum.NotFound（[call.go](../../call.go)）；默认 Strict 在 null 与对象并存时返回 ErrConflict，显式 quorum 按既有门槛接受获胜簇；语义见第 0.4 节 |
| 5 失败语义 | 重组不影响本答案（按 hash 寻址的内容不可变）；来源答 null 而其他源有对象时，默认 Strict 冲突，显式 quorum 按门槛裁决；不从 null 推断是否裁剪。类型化路径另有逐响应完整性：wire hash 必须等于计算的 header hash、uncles 一致性、请求-应答绑定（block.Hash() == 请求 hash），违反 = ErrMalformedResponse（[call.go](../../call.go)） |
| 6 成本 | 单次 fan-out；fullTx=true 时大负载且全对象参与比较；fullTx=false 时类型化读取被 DecodeBlock 拒绝（hash-only 交易列表）——根层 Call 传 false 可得 Corroborated 决策但类型化解码失败，当前由调用方避免（ethadapter 恒传 true） |

待明确边界：

- 可选字段差异（如个别供应商附加 totalDifficulty）按当前实现计为异见；是否定义豁免字段集待裁决。
- null 的外部语义与混合裁决已按第 0.4 节接受，保留 Geth 的 NotFound 映射，不新增不存在证明语义。

### eth_getBlockByNumber

| 六问 | 契约 |
| --- | --- |
| 1 结果类别 | 确定性事实：区块头（number + hash）。体（transactions、uncles、其余字段）不参与跨源比较，见待明确边界 |
| 2 共同参考 | 显式高度 = 参数即参考。移动标签（latest/safe/finalized）在本策略层不改写（Prepare 恒等）：标签的锚定发生在类型化路径——先建立参考，再按 hash 走 eth_getBlockByHash（[ethadapter/client.go](../../ethadapter/client.go) blockAt、决策 0018）；根层 Call 直传 latest 时按各源自然头回答，头一致即 Corroborated（共识于当时自然头，未钉参考），头分歧即 ErrConflict。策略层 pending 标签透传不改写，作为节点本地观测（Selected 上限），永不因同值成为 Corroborated，也不改写成锚定读取。类型化 BlockByNumber(-1) 在 adapter 本地拒绝：不支持 pending block typed projection（ErrInvalidRequest）。-1 不锚定为 latest/safe/finalized，不发送业务请求，也不编码成 -0x1。这次拒绝不是 provider 不支持，也不是 Unknown 业务裁决；错误返回携带的 Unknown 只是失败决策不得带成功置信，不是冲突或无有效响应裁决。本策略兼任锚定策略：strictHeadAnchor 以 ("latest", false) 建立参考，锚定裁决强制 Corroborated（[anchor.go](../../anchor.go)）。Corroborated 仍只表示 provider-backed cross-source evidence。 |
| 3 归一化 | 仅头："十进制高度 \| 小写 hash"（等效十六进制拼写一致；高度解析走 hexqty，强制 0x 前缀）；number 或 hash 缺失 = 无效；JSON null 解码为零值头 = 无效。**体不参与跨源比较**；获胜簇 Value 取簇内捕获顺序第一个成员的原始对象，不是规范编码（[rpc/methods/eth_getblockbynumber.go](../../rpc/methods/eth_getblockbynumber.go)） |
| 4 结果分类 | 第 0 节之外：null = 无效（区别于 byHash 的可比较 null）。高度超前于全部源 tip：全 null → ErrNoValidResponse；部分 null + 部分头：null 记 Fault，持有者成簇可 Corroborated——滞后源的“未见”不构成否决 |
| 5 失败语义 | 同高分叉 → 头 hash 不同 → ErrConflict（头比较即捕获）。同头异体（诚实执行不可能、恶意负载可能）→ 同簇 → Corroborated；类型化解码仅逐响应完整性（wire hash == header hash、uncle 一致、高度绑定），不校验体对 transactionsRoot |
| 6 成本 | 显式高度读取单次 fan-out；fullTx=true 时下载全部源的完整体但比较只用头（带宽按体计、比较按头计）。移动标签的类型化读取共两次 fan-out（锚定 + 按 hash 整对象读取；合并为单次是 M4 缓议项） |

待明确边界：

- **同头异体不对称**：byNumber 头一致即 Corroborated（体不比较），byHash 整对象比较会 fail closed。恶意来源返回真头 + 伪造体时，byNumber 的两层检查（头比较 + DecodeBlock）均不拦截，且簇内 Value 由池序决定而非多数。收紧方向已接受为第 0.1 节的内容承诺校验；具体实现、校验位置与兼容性仍待完成。同头异体应纳入该契约的验证场景。
- **null 实现对齐待完成**：当前 byNumber 将 null 视为无效，byHash 将其视为可比较值。第 0.4 节已决定对协议允许的 null 统一保留合法回答身份，并按既有 Strict／quorum 门槛裁决；不再作为未决语义问题。
- 体的可选字段差异在 byNumber 不构成异见（体不比较），与 byHash 的可选字段边界互为镜像。

## 2. 待收敛方法

T03-A 已固定、但尚未写成完整六问条目的边界：

- `eth_getLogs` 的 pending（fromBlock/toBlock，含大小写与非字符串 pending 编码，以及 blockHash 上的 pending 标签）在 Prepare 本地拒绝，错误说明该范围没有稳定 corroboration 语义（`ErrInvalidRequest`）。不调用 anchor，不访问 provider，不产生 Corroborated、Selected 或普通冲突决定。field-level pending observation 仍未实现，不能写成已支持 pending。
- 这两类本地拒绝都不是 provider 不支持，也不是 Unknown 业务裁决。`Corroborated` 仍只表示 provider-backed cross-source evidence。

- `eth_getBlockReceipts` 的 pending 同样在 Prepare 本地拒绝（`ErrInvalidRequest`：pending is not supported）。覆盖大小写字符串、首尾空白、JSON 字符串，以及 `{blockNumber: pending}`（可带 `requireCanonical`）。不调用 anchor，不访问 provider。非 pending 对象不按 pending 拒绝。这不是 provider 不支持，也不是 Unknown 业务裁决。
- `eth_estimateGas` 与 `eth_createAccessList` 的位置 1 接受与 T02 状态读取相同的 EIP-1898 对象。字符串移动标签仍改写成裸 anchor hash；`{blockNumber: latest|safe|finalized}` 改写成 `{blockHash: anchor}`，并保留 `requireCanonical`。显式数量、hash、earliest、pending（字符串或 `{blockNumber: pending}`）不改写。非法对象在调用 anchor 之前返回 `ErrInvalidRequest`。`eth_estimateGas` 的成功和分歧都停在 Selected，不因为同值成为 Corroborated。`eth_createAccessList` 的 pending 仍是节点本地观测。provider error 与 payload 冲突的失败决策保持 Unknown，不升格成业务成功。
- 2026-09-23 的 chainlist 对象样本只证明部分公开端点的响应形状：有的接受 `{blockNumber: pending}` 和 `{blockHash}`，有的对历史状态返回 pruned，有的拒绝 `requireCanonical` 或未知字段，也有端点接受未知字段。样本不能写成“provider 普遍支持”或“provider 普遍拒绝”。


按注册表（[client.go](../../client.go)）推进，每批以本文档新增条目闭环：

1. 锚定状态读取：eth_getBalance、eth_getTransactionCount、eth_getStorageAt、eth_getCode
2. 不可变对象读取：eth_getTransactionReceipt、eth_getTransactionByHash、eth_getBlockReceipts
3. 执行与范围：eth_call、eth_estimateGas、eth_getLogs、eth_feeHistory
4. 可变估计：eth_gasPrice、eth_maxPriorityFeePerGas
5. 节点本地观测：net_peerCount、eth_syncing、net_listening
6. 未注册方法：转发语义（Forwarded 的完整契约）

每完成一批，治理文档第 5 节第 1 项的清单相应缩短；第 6 节场景（首轮四个）随契约推进推演。
