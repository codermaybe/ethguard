# 方法合同

状态：2026-11-20 已核对当前方法事实；接受域政策与外部证据缺口独立维护。本文统一维护跨方法保证、裁定门槛、逐方法保护范围与独立交易规则。公开类型迁移已完成；已登记的接受域差异和证据缺口仍未关闭。

## 1. 权威来源与职责

方法声明位于 `rpc/methods` 各自文件；`Specs()`／Catalog 只收集，`CompilePlan` 产生唯一生产 ReadPlan。[method-rules.md](method-rules.md)是自动生成的装配索引，不手改。模块与执行流程由 [拓扑](codebase-topology.md)拥有；字段来源与生成由 [生成合同](wire-codegen.md)拥有；Value 和材料交付由 [返回值合同](return-value-design.md)拥有。

每个方法说明它返回什么事实、参考什么状态、比较和保护什么、失败如何表达，以及成本和未证明范围。schema requiredness、方法绑定、算法承诺和多源信任是不同职责；不能用其中一项通过推导其他项完成。

### 前置生态兼容与官方语义检查

原始 execution-apis 固定数据经审核的标准勘误形成有效 schema，供生成、mask 和字段投影共同消费。勘误修正固定规范表达，不处理运行时报文。方法仅对有明确依据、完整等价条件及正反例的官方直接 key 做有限兼容；未经兼容的合法值也须完整检查与比较。

共同职责顺序为：保真接收与有界 JSON 安全检查 → 官方字段识别 → 已审核的有限兼容 → 适用生成约束／方法绑定／算法检查 → 逐源规范表示 → 方法裁决 → 分通道交付。具体调用链可以不同，不能为统一外观改变决策类别。

不以放宽 pattern、忽略 required、补零或混源补字段提高通过率。未声明内容沿 Extensions／Diagnostics／预算内原始证据交付，不进入主结果保护域。项目额外限制、官方合法输入被拒或官方输出不完整，应明确登记为差异。

## 2. 固定成员与裁定门槛

业务 Strict 在 corroboration snapshot 下使用 `Q = max(H, floor(N/2)+1)`；默认 H=2。N 是完整冻结 Basis，每个失败、gated、不可准入或未尝试成员仍占一个槽位。N<=0 的历史阈值为 1，实际以无有效响应失败；单一支持者最高 Selected，Corroborated 至少需要两个支持源。Unanimity 在同一 Q 上另要求所有 admissible 答案一致。

未携带 snapshot 的底层 legacy Strict 仍按自身全体一致规则执行；默认公开路径使用冻结 snapshot。Forwarded 是显式调用模式，H=0 且不进行跨源裁决，仍执行适用方法检查。Median／Local 不套 Strict 成功公式，保持 Selected。

reference 建立有自己的候选与确认算法，见 [reference 合同](reference-freshness-policy.md)；公式相似不授权合并。健康、freshness 和来源超时不能换成员、改票权、降门槛或自动退回 Forwarded。代表源是捕获成员顺序的第一个支持者，不能靠代表源原文补回未保护内容。

`Corroborated` 只表示 provider-backed cross-source evidence，不是 Ethereum consensus、canonical chain 或 cryptographic finality。合法 dissent、null、弃权、畸形回答与传输失败分别记录。

响应模型迁移采用结构化`Adjudication`记录固定Basis、Required、候选组及Reason。没有候选、仅一组候选但支持不足、确有多个候选且未被接受，分别为NoCandidate、InsufficientSupport和ConflictingCandidates；接受的多数仍可伴随少数异值。typed保证未达成另记GuaranteeNotMet，不把全部未成功路径统称Conflict。协议合法的null与错误回答仍是实际回答，其方法含义由方法owner判断；缺失或非法信封不能成为方法候选。

写入先保留各来源报告的hash或协议拒绝，再按冻结成员与传播门槛裁定，并校验报告hash是否绑定本地提交交易。SubmittedHash记录本地事实，Hash记录领先报告值，HashMismatch不冒充候选之间的冲突。Forwarded写入明确Required=1；未访问成员留在Sources中，不伪造连接失败。本段为已接受的迁移合同，最终实现验收状态见路线图。

## 3. 方法族与保证范围

| 方法族 | 保护与比较依据 | 明确未证明／例外 |
| --- | --- | --- |
| `eth_chainId`、`net_version` | 无块参考的身份事实，分别使用 hex quantity 与 decimal；net ID 不与 chain ID 互换 | 身份预检与单次裁决独立；`net_version` 的正值边界缺口已登记 |
| balance／nonce／code／storage | 调用内共同参考上的数量或 DATA；storage word 为 32 字节 | 一致响应不等于验证 state trie proof；tag 与 pending 按专属矩阵 |
| 完整 block（`fullTx=true`） | Header 自哈希、请求身份、完整有序交易 trie root、适用的 withdrawals root、完整 uncle 头承诺；完整官方规范对象字节作为比较键，保留并比较size与内嵌交易上下文／from声明 | 不重执行 EVM，不证明 canonical chain／finality；头中的根值受承诺不等于加载并验证其全部对应内容 |
| header-only／`fullTx=false` block | reference可使用较窄number/hash身份；完整Header逐源在重编码前核对原声明hash与自算hash，生成Header投影检查required及fork字段，typed查询另绑定请求hash／高度 | 不外推完整body trie／withdrawals／uncle载荷承诺；纯Header不强制EmptyUncleHash，历史PoW非空uncle root合法 |
| 独立 transaction | 方法级完整 key 修正后执行生成 query required／nullable／分支约束及 payload hash、签名／声明一致性；by-block/index 另绑定上下文并拒绝 pending 对象；官方字段和 presence 完整比较 | 不恢复 sender，不证明 inclusion 或 blockHash／height 关系；pending 时间戳的有限同源派生与显式标准勘误见 下文“独立交易的有限兼容与对象合同” |
| transaction receipt／block receipts | 官方 receipt 形态的 required／nullable／byte 校验，来源提供的字段经规范编码比较；请求 tx／block 绑定，日志非零 hash 的父子一致性，logsBloom 与日志内容一致 | 不证明收据集合完整、receipt trie inclusion、from 的 sender recovery 或链上执行正确；完整日志要求与零 hash 容忍仍是项目接受政策，不能误写为完整官方输入域；见决策 0068 |
| `eth_getLogs` | 非 null 日志数组，要求完整日志字段，绑定请求 blockHash 或高度范围 | 不是 logs inclusion proof；当前并未核对全部 address/topics filter；只支持 logs 分支而非 pinned schema 的 hash-array 分支 |
| `eth_feeHistory` | 规范数量与 ratio，数组之间的长度关系，响应长度等于请求 count，reward 行宽等于请求 percentile 数（请求感知准入绑定，2026-11-15 起） | 官方允许返回 supported 子区间；高精度 ratio gate 也是项目限制。两项问题均待调整，不在本文认可为更强安全保证 |
| `eth_createAccessList` | accessList 元素的地址／storageKeys 与规范对象比较 | 强制 accessList 比官方顶层 required 更窄；不执行或证明建议集合正确 |
| `eth_simulateV1` | 生成 payload／selector 在 reference 前检查并固定；开放输入保留，封闭对象／字典拒绝非法成员；规范模拟结果按同一参考比较。比较与受保护值都经官方生成模型往返：pinned bundle 未声明的内层 `error.data`（如 revert reason 字节）不参与比较，也不进受保护值——内层失败的佐证只覆盖官方声明的 code/message | 模拟块与日志是合成结果，不证明链上执行或真实 inclusion；合法请求仍可能返回顶层错误，类型生成不能替代完整模拟语义及 provider 证据；未声明的内层错误扩展成员只经 Extensions／raw 通道交付（`TestSimulateV1InnerCallFailureRemainsAResult` 钉住），两个内层 revert 字节不同的源仍可聚成一簇 |
| `eth_call` | 共同执行参考，execution／data 两维裁决，未佐证 data 只删不补 | provider code/message 只是 `Decision.Revert` 上下文，不进受保护 Value；详见返回值合同 |
| gas price／priority fee／estimate | 既有 lower-median 选择及 spread 证据 | 上限 Selected；同值不升级 Corroborated |
| peer count／syncing／listening | 来源本地观测；适用顶层形状准入与 typed projection；SyncProgress 使用官方生成的三个 optional 字段，geth 额外计数由附属及原始来源证据交付 | 上限 Selected；syncing 规范结果执行生成 schema 检查，保留 optional presence；缺失计数不成为同步已完成的独立证明 |
| 写入 | 冻结原始字节，先本地解码与签名恢复检查，再广播并按relative-trust响应分类；非法签名在业务网络前拒绝 | 不签名、替换、自动重试或管理交易生命周期；不是读取 quorum |
| 未注册／显式 unsupported | 按 catalog 和公开入口规则处理 | “未知方法曾可 Forward”不代表当前已注册只读 `Call` 会绕过 allow-list；不能以未知转发补齐兼容缺口 |

实现入口为 `rpc/internal/objects/{block,transaction,receipt,log,feehistory,accesslist}` 与 `rpc/methods`。表中的“未证明”不是待实现承诺；需要增加保证须独立评审。

### 广播后的同步收据观察

2026-11-21方法级验收完成：四入口的输入、传播接受、请求绑定、收据观察、终止、capture及预算逐项核对；复用已有实现并通过26个定向顶层测试。没有新增生产行为，框架验收与此次方法完成判断分别记录。

`SendRawTransactionSync`／`SendTransactionSync` 的 root/view 共用一次广播和后续 receipt read。只有 `WriteAccepted`、非空获胜 hash 且与本地签名交易 hash 一致时才查询 receipt。未接受的广播由 `WriteUnconfirmed` 和 `Adjudication.Reason` 表达；协议拒绝、支持不足、实际分歧、foreign hash 或广播错误均不进入轮询。typed 交易编码一次，receipt 查询绑定已接受的 hash，等待过程不重放广播。

NotFound、Conflict、InsufficientSupport 和 GuaranteeNotMet 均允许继续观察，其他 receipt 执行错误立即返回。显式正 timeout 只约束广播后的等待；nil／zero 使用当前 20 秒默认，父 context 可更早结束。负 timeout 在广播前拒绝；typed 入口先快照再校验 timeout，保持已有本地错误顺序。成功交付请求绑定的 provider-backed receipt，不证明 inclusion、finality 或 reorg safety。

`CallResult.WriteDecision` 保留原广播，`ReceiptWait.LastAttempt` 和 `LastObservation` 分别保留最后尝试与最近形成的方法观察，各有自己的序号、Decision 和错误。最后尝试被中断时，不能覆盖此前的有效观察或沿用其票数；最终 timeout 只表达等待终止，不包装历史 NotFound 或 Conflict。默认保留量相对轮询次数有界，不能据两个位置推断连续缺失、持续冲突或完整历史。字段、观察形成条件及 capture 所有权见 [返回值合同](return-value-design.md#同步写入的结果与诊断)。

## 4. 请求参考、pending 与错误

- latest／safe／finalized 保留请求 level，在调用内建立相应 reference；不跨调用缓存，不静默替换标签。显式 hash／高度不换块，EIP-1898 的 presence 与逐方法规则见 [引用矩阵](moving-tag-matrix.md)。
- 请求错误在 reference 和业务 fan-out 前拒绝。reference 失败保留已形成的诊断，不发业务请求。
- pending-local 不建立 moving reference。它可以收集多个成员，但选取第一个可用本地观察，Selected 封顶；Support 恰为所选来源，Participants 仍表达实际冻结成员。
- typed pending block/header、pending logs 和 block receipts 的拒绝与普通 pending 状态读取分别维护，不概括成全支持或全拒绝。
- pending `eth_call` 的 raw Value 可保留 provider revert 信封；mined 路线只交付受保护 data。typed 边界按同一错误语义投影，差异不能被写成所有 raw 路线完全相同。
- 单源响应的 malformed／请求绑定失败／无法规范编码不贡献支持，保留故障及分母；标准方法能力错误、合法异见和调用者取消不能一律归为 provider 不规范。
- 公开 Call 本地拒绝未知方法；低层 Registry forwarding 不扩张公开 allow-list。失败 CallResult 不交付成功 Value；corroborated null／revert 的业务映射按返回值合同区分。

## 5. 独立交易的有限兼容与对象合同

适用 `eth_getTransactionByHash` 与 `eth_getTransactionByBlockHashAndIndex`。方法拥有完整官方直接 key 的兼容规则；外层按 key、内层按完整值状态分支，原始同源上下文只读、候选独立写入，每个 key（含允许缺失派生的 key）只处理一次。无额外规则的方法不增加空 switch。

三种出口分别是：命中规则并产生完整候选；未命中、保留完整原值交后续检查；已知非法／歧义返回错误。未命中不等于合法，不丢掉 required、nullable、分支或 signed payload 检查。

当前唯一缺失派生是 by-hash 的 blockTimestamp：该 key 缺失且同源 blockHash、blockNumber、transactionIndex 三项都明确存在为 null 时补完整 null。其他缺失或混合上下文不修正。in-block 保持 mined-only，不能继承 by-hash 的 pending 派生。

对象准备使用有效生成 query 模型，保留 payload hash、签名形态、from 语法、包含上下文及请求绑定检查；protected legacy 的 chainId 声明与签名派生值核对。当前不做 sender recovery 或 inclusion proof。未知 typed payload 无法验证时保留可分类拒绝与原始材料，不能因 raw 一致获得已验证交易的保证。

`Candidate` 共享对象准备，两个 processor 消费同一结果；raw Normalize／Encode／Admit 适配保留。canonical 结果只在一次 invocation 内复用，不按 hash 跨调用缓存。规范 typed 值优先，仅补回官方 mask 保留而当前分支未表达的成员：开放分支中官方容忍的跨分支附加项保持；闭子对象的域外成员由 mask 隔离，差异不处罚健康。mask 成员身份不等于值已验证，不以合并掩盖覆盖缺口。

独立 query 与 BlockTransaction 的有效上下文合同不同。完整块输出已经接入生成 Block；当前 block admission 仍有 transaction Candidate／私有 geth 处理，不能沿用旧“内嵌交易完全不走独立内核”的描述，也不能只凭代码复用宣称两者接受域完全一致。该关系属于后续审查，本文不修改生产行为。

兼容规则应确定、无来源偏好、与遍历或到达顺序无关；等价关系须自反、对称、传递。不能只因调用效果相似合并不同 DATA、签名或 payload，也不建立 key×type×path×context 注册体系或对子路径／数组元素持续扩展方言修正。provider 方言需真实可追溯样例，状态派生需状态验证，纯官方 required 修复可依据规范和独立反例进行。

### 现有证据与未覆盖范围

官方 type 0–4 向量、required／nullable、合并、绑定、原始材料和健康最终记账已有测试。固定 Anvil 1.7.1 的无 fork transaction 生命周期曾通过 HTTP／raw WSS、Root／view 的 88 条消费断言；一个实例的多连接不算多个独立 provider。相关规则与 fixture 已保存于源码；旧运行日志和分批提交详情由 Git 保留，不复制为当前检查结果。

新公网方言采集、更多交易类型的真实状态生命周期和完整 provider 兼容仍未完成；文档重组没有运行这些观察。验收必须覆盖合法表示聚合、真实内容不误合并、非法一致回答仍被拒绝、vendor 差异不干扰主裁决、固定分母、最终 typed 及来源材料。

## 6. 待裁决与待实现事项

| 事项 | 已知事实与处理边界 |
| --- | --- |
| FeeHistory | 准入复用完整解析模型绑定 count，停止生成后丢弃 key；精确小写 reward 绑定与 struct alias 分开。官方精确 Number 与现行 ratio gate、supported 子区间与请求 count 等长限制、optional 数组 presence 仍须分别裁决 |
| AccessList | 已消费生成的 AccessListItem／AccessListResult，方法适配仍强制 accessList 存在，并将 error／gasUsed 的 null 折合省略；保留基线别名、条目形态检查及错误顺序。适配成本单独实测，不因采用生成类型自动认定性能通过 |
| 共享 Log | 内核保留手写 CanonicalLog 和完整字段校验；单遍尝试已恢复为基线的 Validate 与 EncodeOne 两段处理，以保留精确键准入、编码别名及错误优先级。生成 Log 的 optional／nullable 不同，后续替换仍需 query、receipt、订阅及 Pull 的等价证据 |
| 本地观测 | local Provenance、无可尝试来源与 identity 错误优先级仍需逐模块复核，不将旧探针当现行缺陷证明 |
| net_version | 当前方法 Normalize／Encode／typed quantity 路径没有正值 gate；public plan 实测接受零、负值、加号、前导零，拒绝 hex 字符串。是否收紧至非负或正值须单独裁决；NetworkID 与 ChainID 不强制相等，不额外查询。生成十进制模型的接受域不能直接冒充此方法现行规则 |

文档基线之后的实现与审计已推进上述事项，剩余差异继续由本合同和路线图维护。静态候选不等于已经复现的漏洞或已授权的新合同；实现切片通过也不代替跨模块与成本验收。

## 7. 验证入口

`eth_call` 的 execution／data 边界由 [`TestNormalizeCallOutcomeSeparatesRevertDataStates`](../../rpc/methods/eth_call_revert_test.go) 等回归验证：revert data 的缺失、null、空 DATA 与具体字节不得在没有规则依据时折合，provider 错误 message 不进入受保护 data。

完整 block 的合法空 uncle 集合与外块绑定拒绝由 [`TestEmptyBlockUnclesAreLegalAndForeignBlockIsRejected`](../../rpc/methods/content_compat_test.go) 验证。reference 的失败不触发业务 fan-out、每次调用重新建立及失败证据交付，分别由 [`TestReferenceAnchorFailureKeepsPrepareErrorAndSkipsBusinessFanout`](../../internal/verification/integration/reference_freshness_test.go)、[`TestReferenceEstablishedFreshPerCall`](../../internal/verification/integration/reference_freshness_test.go) 和 [`TestReferenceFailureEvidenceIsUnknownWithObservedAt`](../../internal/verification/integration/reference_freshness_test.go) 验证；这些测试引用说明合同落点，不表示本次文档更新重跑了对应运行场景。
