# 返回值：受保护结果与规范编码

状态：2026-11-21 响应模型与同步操作组合已验收。本文统一受保护 Value、规范表示、typed 与来源材料的交付合同；历史实现理由由 Git／决策档案追溯，不把已完成迁移当待执行任务。

## 1. 独立合同与语义等价

| 合同 | 回答的问题 | 事实源 |
| --- | --- | --- |
| 官方 wire 合同 | 方法、结果字段、类型、required、nullable 和值约束是什么？ | 固定 execution-apis bundle 与对应版本的协议／方法说明；见 [spec 数据说明](../../internal/spec/README.md) |
| EthGuard 保护合同 | 哪些内容经过请求绑定、内容承诺或跨源比较，可以进入受保护 Value？ | [方法合同](method-contracts.md)、适用对象检查与现行实现；历史理由见决策档案 |
| 表示合同 | 同一个被接受的值如何编码成确定字节？ | `internal/ethcodec` 与各对象 encoder；官方生成层见 [wire-codegen.md](wire-codegen.md) |

项目接受政策单独说明哪些非标准拼写可兼容读入，以及哪些官方合法输入因方法语义被拒。它既不改写官方 schema，也不自动形成额外保护。

比较等价关系由方法和字段语义定义，不由 shape 相同推出：大小写、转义、键序可归一；QUANTITY 按数值，DATA 按完整字节。`0x0000` 与 `0xDeadBef` 不因此等价；若另一方法只保护二者共有的业务结论，须明确该结论及输出合同，不能仍将其中一份原始 payload 当作获佐证结果。

**规范编码表示确定性，不自动表示符合完整官方结果 schema。** 在合法对象上增加请求绑定是更深锁定；拒绝部分官方合法输入是接受域收窄；删掉官方 required 字段、输出不允许的 null，则不是原对象 schema 的合法子集。当前实现包含这几种不同情况，不得混称为“比官方更严格”。

公共 RPC 的实际响应有兼容性参考价值，但不是字段规范的来源。保留的 [shapeprobe 归档](../samples/2026-09-26-shapeprobe/README.md) 记录有限日期、端点和请求，不能推出普遍兼容。标准冲突与接受边界由各方法合同维护；receipt 裁决见决策 0068，独立 transaction 的首个实施检查点见 [方法合同](method-contracts.md#5-独立交易的有限兼容与对象合同)，完整 Block 与公开业务签名已接入生成模型，内部表示与接受域差异仍须逐对象审查。

## 2. Value 的含义

已注册方法的 Value 是方法声明的受保护结果，不是代表源原文。它必须来自一份已准入载荷的确定性编码：

- 不把多个来源的字段拼成新对象，不从比较键反序列化来重建回答。
- 比较键覆盖的字段、被请求绑定或已验证承诺约束的字段、按公开规则派生的字段，才有进入保护域的依据。方法必须说明采用了哪一种依据。
- 本地派生值需要与来源声明核对时，先核对；不把错误来源修成正确值后计票。
- 未获保护的内容只进入声明的 Extensions／Diagnostics 或 opt-in 原始证据通道，不借 Value 暗示已佐证。
- 多源 Corroborated 仍是 provider-backed evidence，不是 Ethereum consensus、canonicality、执行有效性或密码学 finality。

完整区块现在按官方生成Block交付：`size`、内嵌交易的`from`、hash与inclusion上下文保留在逐源规范对象中，并与其余官方字段共同参与比较及提交。缺失required字段拒绝来源，不再通过geth重编码丢弃它们。`size`和`from`属于来源声明经多源比较保护，不宣称由RLP独立验真或sender recovery；交易payload hash、上下文和body承诺仍单独核对。reference 分支只承担 number/hash 的最小身份合同，公开 generic `Call(..., false)` 也可触达；该结果不具有完整官方 Block schema 保证。full／header／reference 的公开调用与 typed 接受边界由 [TestStandardBlockConsumptionBranches](../../internal/verification/facade/standard_block_contract_test.go) 分别验证。

receipt 已采用官方形态与保护闭环：`from`／`effectiveGasPrice` 必须由来源提供并比较；nullable `to`／`contractAddress` 保留缺失、null、地址三个状态；可选非 nullable 字段存在时拒绝 null；`type` 受 byte 约束。`status`／`root` 至少一个存在，同时存在则独立校验、比较并保留。完整日志要求、bloom-log 一致性、请求绑定和零日志 hash 容忍仍按方法规则执行；后者不构成完整父子绑定证明。公开 typed 使用生成 `ethapi.Receipt`，保留受保护字段与 presence；私有 geth Receipt 只供算法检查，不能用它再编码主结果或丢掉 from／to。

## 3. 准入、编码和裁决

```text
冻结成员收集 → JSON 安全 → raw Admit + Encode 或 CanonicalProcessor
  → 方法分簇／选择与固定分母裁决 → 提交同源已验证规范 Value
  → 原始观察的附属投影与材料 → CallResult／typed
```

- 方法准入、EncodeCheck 或 processor 失败的来源记相应阶段 Fault，不贡献支持，仍留在本轮分母。不得先裁决、再删掉失败来源重算门槛。
- 完整区块采用canonical字节作为比较键，独立交易的比较与编码使用同一完整候选对象；reference的HeadCodec仍使用number/hash身份，不因此扩大为完整block校验。数量、成功 DATA、feeHistory、logs、accessList 和 receipts 使用各自规范字节。实际装配见生成的 [method-rules.md](method-rules.md)，不在本文复制注册表。
- 获胜簇内支持源的规范字节必须相同。同键却编码不同是保护域／比较键／实现不一致，返回 `ErrMalformedResponse`，不改选某份原文或逐字段投票。
- 非获胜簇的同类不一致只形成 Faults，不否决已经达门槛且自身一致的获胜簇，不触发第二次投票。诊断保留 source ID、长度和摘要，不复制完整 Value。
- Median 和 Local 保持 Selected；显式 Forwarded 没有跨源保护声明。低层 Registry 的默认 forwarding 不使公开 Call 接受未知方法。不能因编码一致把它们升级为 Corroborated。
- 主聚合失败时清空公开 `Decision.Value`，但最终业务错误不抹去已形成的聚合事实。合法 null、revert，以及读取成功后发生的 typed／派生绑定失败可以保留所属 Decision；失败的业务返回值不因此成为成功结果，`CallResult.Err` 始终等于尾部 error。结果载体、隔离与生命周期以决策 0066 和 `call_result.go` 为准。

字节相等只是必要条件：删掉应保护的字段也能制造相等。验证还必须覆盖字段处置、变异、请求绑定、派生和受保护语义往返，见 [验证管线](method-verification-pipeline.md)。

## 4. 表示与空值

| 类别 | 当前规则／边界 |
| --- | --- |
| QUANTITY | `0x`、小写、无多余前导零、零为 `0x0`；严格数量不把前导零当作另一种合法拼法 |
| DATA | 保留字节长度与前导零；不得把 `0x00`、`0x`、`0x0` 合并。storage word 另要求 32 字节 |
| 地址及对象 | 地址规范为小写；对象按 encoder 的字段顺序紧凑编码，关闭 HTML escaping；键序不表示语义优先级 |
| 官方 Number | 精确保留 JSON 十进制数，界限来自 schema，不经 float64；由生成层处理 |
| 受保护 feeHistory ratio | 仍执行项目 `CanonicalRatio` 的 float64 最短十进制保值 gate；`0.50` 可折合，`0.30000000000000001` 被拒。这是项目接受域限制，不是官方 number 定义 |
| 缺失与 null | 不全局补空值。对象查询合法 null、空数组、字段缺失和显式 null 按具体方法区分；现有 optional 字段的 null→省略例外及其冲突见问题记录。Header／区块共享的null→省略例外：`baseFeePerGas`／`withdrawalsRoot`／`blobGasUsed`／`excessBlobGas`／`parentBeaconBlockRoot`／`requestsHash`／`blockAccessListHash`／`slotNumber` 在官方 schema 为 optional 且 non-nullable，而 geth 头部 marshal 对未设指针字段输出显式 null——这是 pre-fork 区块事实上的 wire 形状，按显式八项政策折合为省略；baseFeePerGas与blockAccessListHash在完整模型接入时补入，依据同一锁定geth生成编码事实，required null与其余畸形值仍拒绝，使受保护值满足对应官方schema（与 log `blockTimestamp:null`→省略同类） |

标量与 JSON 原语由 internal/ethcodec 维护；对象规则与请求绑定统一位于 internal/ethobject，比较 mask 与剪枝位于其 wiremask 包，公开模型来自 ethapi。算法输入视图不能冒充第二份官方模型。不要在文档再维护一份可独立漂移的字段清单。

## 5. 代表源和证据

代表源是获胜簇中本次捕获成员顺序的第一个来源，不按到达先后或 source ID 排序。对已规范编码的方法，它决定审计锚点，不决定 Value 拼写。

Provenance 的准入证据、withheld 字节和代表源必须同源；不得从簇内其他成员补材料。Provenance 不保存代表源 Raw，也没有为 Raw 设定的字节上限。原始证据的保留预算属于 `CallResult.Uncertain`／source-evidence 通道，不能与此混为一谈。域外字段只有达到动态门槛的单一值才能进入 Extensions，其余为 Diagnostics；它们不成为受保护 Core。

完整 `eth_getBlockByHash`／`eth_getBlockByNumber(..., true)` 的非空 `Provenance` 是按 `Value.uncles` 顺序排列、经过同源准入的叔块响应 JSON 数组，元素可包含 block 形态额外字段；没有叔块时可以没有该载体。`ethadapter` 重建完整块时必须重新核验各 uncle header hash、对应 hash 与 aggregate uncle root，并复核交易／withdrawals 内容承诺。该默认载体与可选原始来源材料预算无关，不能跨方法把 opaque Provenance 当作免检对象。

`ethadapter` 的纯转换直接返回 `ConversionReport`；Client 调用通过可选审计交付省略与错误，不改变此处的核心结果语义。它不在 context 上重套 `WithResult`。核心调用成功后若 geth 转换失败，外层 capture 仍保留核心成功，适配器方法返回独立转换错误；`AuditEvent` 分别携带核心结果、`CoreErr` 与 `AdapterErr`。详见[兼容政策](go-ethereum-compatibility-policy.md)。

订阅审计使用独立的 `AuditEvent.Subscription`：setup／event／terminal 阶段携带固定 Basis 和对应原生 Head／Logs／Receipts 事件，包含空匹配块、撤回与证明；不伪造 unary `CallResult` 或 `Decision`。其保留、终态及关闭边界归[订阅合同](subscription-contract.md)。


### 数据交付与责任范围

项目不以一个万能 struct 兼容所有 provider 响应。官方定义是受保护结果的语义基准，前置兼容只积累经审核的生态规则；责任边界见 [方法契约](method-contracts.md#前置生态兼容与官方语义检查)。字段未被官方类型或公开 Go 类型表达，不等于它没有交付价值，但也不能因此把它并入受保护 Value。

| 交付通道 | 承载内容 | 责任与条件 |
| --- | --- | --- |
| Value 与 typed projection | 当前方法承诺保护的官方结果及其公开 Go 表示 | Value 沿方法合同准入、比较和编码；typed 可能无法表达其中全部官方信息，不能以 typed 零值或丢字段冒充完整保真 |
| Extensions／Diagnostics | 域外字段的附属比较结果及来源差异 | 按既有附属规则交付，不承诺适配每种厂商字段的业务语义，不把它们升级为官方主结果 |
| CallResult.Uncertain／source-evidence | 按来源保留的原始响应、字段与错误材料 | 按需开启，受现有安全限制、保留预算及截断规则约束；原文不因前置兼容被覆写，不作为主结果合法性的替代依据 |

“用户仍可取得来源提供的值”指通过上述不同形态取得受保护结果、附属内容及已保留的来源材料，不承诺默认、无条件、无限量保存所有原文字节。需要查看域外或无法规范交付的来源内容时，调用者应启用 `WithSourceEvidence` 并配置适当预算；实际丢弃和截断由 `Uncertain` 的记录状态说明。材料保留不足不能改变请求、比较范围、投票资格或门槛。 当前实现除调用方总 byte 预算外，还限制每份 raw response 为64KiB、raw与field合计最多256条、可变 metadata text最多4KiB；预算非正本地拒绝。capture句柄复用永久失效，panic使句柄aborted；这些交付错误不进入业务错误。

处于方法承诺保护域内的官方信息不能仅因 Go struct 无法承载，就降为可关闭、可截断的原始证据。域外材料也不能为保留在一个返回结构中而污染官方 Value。多源 RPC 的响应形态差异通过语义规范结果与分通道材料表达，不通过无限扩充一个统一结构消除。

`eth_call` 成功只输出规范 DATA。revert 分开裁决 execution 与 data：缺失/null 对 data 弃权，空字节、具体 hex 与非 hex JSON 是不同断言；未达门槛的 data 不进入 Value。provider code/message 留在 `Decision.Revert` 作为上下文，不拼装固定错误信封，也不声称文案获佐证。当前 `RevertError` 只承接受保护 data，不暴露旧 provider `ErrorCode()`。依据为决策 0039、0052、0054、0061。

传输保真必须按 adapter 区分：历史 geth WSS seam 的 missing/null 折叠不能外推到现行 raw WSS，也不能用 HTTP 证据替代 WSS。具体范围由 [传输契约](transport-contract.md) 和 [WSS 契约](wss-wire-contract.md) 维护。

### 同步写入的结果与诊断

同步写入通过同一个operation owner执行一次广播，再按明确的等待期限查询收据。`CallResult.WriteDecision`始终属于原广播；`CallResult.ReceiptWait`组合后续收据查询。顶层`Decision`仍属于普通read，同步操作的read证据有自己的嵌套位置。该组合及全部受影响消费者已通过全仓验收，范围见路线图。

| 位置 | 所属事实 |
| --- | --- |
| WriteDecision | 原始广播的提交hash、传播裁定及逐源两层摘要 |
| ReceiptWait.LastAttempt | 最后一次实际收据查询的Sequence、Decision和Err |
| ReceiptWait.LastObservation | 最近一次形成收据方法含义或裁定的查询，保留该轮自己的Sequence、Decision和Err |
| CallResult.Err | 整个公开操作的最终结论，与尾部error及capture一致 |

Sequence从1开始，每次实际调用前递增，表示收据查询轮次，不是单节点请求数。未进入等待时ReceiptWait为nil；等待已开始但未查询时可以没有LastAttempt。最近观察可以是合法null、实际分歧、单组支持不足、保证未达成或方法拒绝。仅通信中断、未形成方法判断的协议失败和内部中止不构成新的收据含义，不能覆盖此前观察。

例如第1轮null、第2轮合法异值、第3轮等待中断，最后尝试属于第3轮，最近观察属于第2轮；两者不共享票数。最终timeout只表达等待终止，错误链不包装历史NotFound或Conflict；历史错误在所属观察内消费。正常成功时两个位置可以指向同一轮，但各自拥有隔离的可变Decision数据。默认只保留这两个位置，内存不随轮询次数增长，不承诺完整历史、持续冲突、连续缺失或reorg证明。

NotFound、Conflict、InsufficientSupport和GuaranteeNotMet允许后续查询，其他receipt执行错误按现有合同终止。只有广播已Accepted且绑定本地交易hash才进入等待；广播只执行一次，receipt计时从广播接受后开始。父context和owner生命周期仍由现有执行入口管理，不自动重播交易。

来源原文是独立的可选材料：root配置WithSourceEvidence后可直接消费，view需要ResultCapture。所有广播与查询共用单次调用预算；预算关闭、耗尽或截断都不删除上述默认事实，也不改变请求、门槛或终止结论。原文记录本身没有完整轮次索引，不能把有界材料冒充完整投票历史。CallResult.Clone和capture复制隔离广播、两个receipt位置、候选成员及来源切片；不可变error按既有合同共享。

### 两层响应事实与裁定合同（2026-11-21，已验收）

结果模型先区分逐源通信事实与回答含义，再由Decision表达多源候选、固定Basis与保证结论。HTTP有响应不保证取得有效RPC回答；合法null、协议错误回答、方法准入失败、实际无回答、合法异值和支持不足须保留各自语义。下层前提缺失时，上层保持未评估；本地取消或资源上限不自动归责为网络故障。异值是否存在与支持是否达标分别表达，故障来源不从固定分母删除。

用户于2026-11-21明确当前项目没有兼容负担或实际用户负担；本轮按一个目标模型整体替换，必要的公开签名、结果类型、错误分类与内部接口可以直接调整。不安排旧结果投影、弃用窗口或V2并存；所有受影响调用方、capture、工具和示例须在同一专题迁移，旧表示和临时转换删除后才算完成。已验证的方法业务规则继续作为回归依据，旧结构本身不构成保留要求。

transport的Exchange/Answer两层Response贯通Emission/Observation/HeadObservation；强制Outcome、公开Sources/Adjudication、同步操作组合及canary/replay消费者均已验收。全仓普通测试与全量race均通过（51个通过包、9个无测试包），vet/build、gofmt/diff、旧出口审查和生成资产一致性通过。历史真实采集未改，本次验证不构成新的外部provider证据。

`Decision.Sources`与`ReferenceEvidence.Sources`默认交付每个冻结来源的Attempted、Exchange、Answer摘要及Relation。Answer摘要保留协议种类、原始原因与方法判断，不复制所有result原文。Outcome中的SourceVerdict只拥有解释时新形成的判断和来源关系；来源身份、通信与原回答仍来自输入Observation。完整原文不再是区分“未回答”和“不合法回答”的前提。

| 场景 | 裁定及来源事实 |
| --- | --- |
| 一组合法候选，其他节点失败，未达门槛 | InsufficientSupport；失败节点仍占Basis，不能声称观察到异值 |
| 多组合法候选，无法接受其中一组 | ConflictingCandidates；保留每组成员 |
| 多数已达门槛，另有合法少数异值 | Accepted与多个Candidates并存，少数异值不自动成为来源故障 |
| 已形成Selected值，但调用要求Corroborated | 保留已形成Status，Reason为GuaranteeNotMet |
| 收到完整回答后方法拒绝 | 保留Completed与Answer.Kind，Interpretation记录Rejected |
| 所有源尚未形成方法判断即中断 | Reason保持Unassessed，终止原因留在相应交换或操作 |
| resolver违反覆盖、身份或候选不变量 | InternalFailure；不能据此惩罚provider |

读Decision保留Support/Reject与故障便捷视图供现有事件和报告消费；这些视图由同一来源判断投影，不成为第二次分类入口。来源故障与collection故障分别归属来源摘要及coordinator，内部合同错误独立留在裁定或整轮诊断。写入统一在Sources保存来源回答，Faults只保留collection级错误，删除旧Accepted/Dissent/Rejected/Silent并列切片。

WriteStatus只陈述Unknown、Accepted或Unconfirmed，原因由Adjudication表达。SubmittedHash是本地签名交易的hash，Hash是领先的实际报告值；绑定失败时保留两者并返回HashMismatch。协议拒绝保留code/message与error.data的missing/null/value/unknown语义。空HTTP正文仍为Missing，非法hash为实际result的Rejected，不折叠成同一个“静默”字段。

## 6. 维护边界

当前行为由本合同、方法合同及实际入口共同核对；历史理由见决策档案。保护域、官方兼容或接受策略变化须明确目标、影响与独立预期，不能靠改写文档宣布实现完成。未决事项保存在对应正式合同，文档全系列整理并保存 Git 后再开始代码阶段。
