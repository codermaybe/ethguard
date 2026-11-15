# 方法契约

状态：现行方法语义索引。本文保留跨方法不变量和有意义的语义差异，不再复制历史决策、阶段任务或固定的方法数量。

## 1. 权威来源与职责

- 注册与装配入口为 [`rpc/methods/spec_inventory.go`](../../rpc/methods/spec_inventory.go)。全部 27 个注册读方法由各自方法文件自治定义名称、Spec、准入、normalizer、encoder 和只读映射，inventory／catalog 只收集。生成索引为 [method-rules.md](method-rules.md)，不得手改。
- 官方结果形态：固定 execution-apis bundle，见 [spec 数据说明](../../internal/spec/README.md)。保护域与完整官方对象的差异见 [返回值合同](return-value-design.md)，各方法的已接受边界以本文为准。
- 每个方法的 tag／EIP-1898／pending 请求行为：[moving-tag-matrix.md](moving-tag-matrix.md)。
- Root／EthClient／HTTP／WSS 实现和证据状态：[method-transport-coverage.md](method-transport-coverage.md)。
- 接受或取代某项产品选择的依据：[decisions.md](../decisions.md)。历史 Status／旧 API 不是当前实现台账。

新增或变更方法须回答：结果是什么事实、共同参考是什么、比较什么、输出与证据是什么、失败如何表达、有哪些成本和未证明项。答案应在拥有该知识的代码与合同中，而不是新增一份运行时字段 DSL。

已确认的兼容组织采用方法内外层 key／内层完整值状态的双 switch：整 key 是修正单位，上下文来自原始同源只读字段，未命中仍须校验和比较。无额外规则的方法不强制增加 switch。该收敛与全量归属迁移均已完成（两 transaction 方法为首个落地样板）；知识归属见 [拓扑](codebase-topology.md#方法自治组织)，具体语义仍归本文及对象专项合同。

### 前置生态兼容与官方语义检查

固定官方参考数据先经过独立的显式标准勘误层（`internal/spec/errata`）；生成器、mask 和字段投影共用其有效 schema，原始 bundle 与官方向量单独保留为来源证据。勘误只改变已审核的标准表达，不接收运行时报文，也不解除以下直接 key 兼容边界。每条勘误的来源、摘要、理由、依据与退出规则见 [生成合同](wire-codegen.md#显式标准勘误与外部审计)。

前置兼容是本工具对 RPC 生态的有限知识积累：方法文件集中维护有实际样例、明确等价依据和正反例的官方直接 key 修正规则，将获准的输入转换为符合官方语义的候选值。项目不承诺兼容所有 RPC、provider 方言或市场上的全部响应字段，也不为增加通过率持续向 key 内部细分修正。

共同处理主线为：保真接收原始响应并执行 JSON 安全检查 → 按官方定义识别字段 → 方法内执行已审核的有限兼容 → 对候选执行适用的官方结构和值约束及方法绑定检查 → 按方法合同规范编码、聚合比较与裁决 → 分通道交付结果和来源材料。此顺序描述职责，不要求每个方法有相同物理调用链，也不改变现有 Strict、Selected、写入或订阅的执行合同。

没有适用兼容规则的官方字段，保留其完整原值进入后续检查；原本缺失仍是缺失。“无法兼容而原样下传”不表示绕过验证或直接获得投票资格。符合官方和方法合同的值仍可参与比较，不符合的来源回答不得贡献支持，即使多个来源同样非法也不能形成成功结果。

兼容之后以固定官方来源及显式勘误表达的有效标准语义为检查基准，包括适用的 required、nullable、类型、分支、取值和对象关系；既有请求绑定、payload／声明一致性及承诺检查继续按方法合同执行。此后的 codec、生成类型和对象校验不是另一层无限方言适配。现有项目额外接受限制、保护域收窄或官方形态缺口须明确列出，不能混称为官方要求；生成规则表达不足也应在生成链修正，不以扩张 provider 兼容掩盖。

非官方内容沿既有附属比较、诊断和原始证据通道交付，不扩大主结果的语义责任。域外差异本身不改变官方结果分簇或处罚来源；JSON 安全、资源限制、来源归属仍有效。官方结果、typed 投影与附属材料各自承载的内容及保留条件由 [返回值合同](return-value-design.md#数据交付与责任范围) 维护。不同来源不必返回相同字段形态，用户获得的是有明确责任范围的多源结果及可用来源材料，不是对所有响应形式的统一结构承诺。

响应的 pattern／const／数量与 hash 约束不整体降级为 Warning。可能无害的文案差异通过官方修复、具体勘误或方法规则明确处理，不能把非法受保护值作为有效支持源。未知字段不自动并入 Value；Extensions／Diagnostics 与有预算的原始证据继续按既有合同交付，不承诺默认无限量无损保存所有原文字节。

## 2. 共同裁决边界

Strict 业务轮沿用固定成员多数 `Q = max(H, floor(N/2)+1)`，默认 H=2，Corroborated 至少两个支持源。reference 建立另有明确算法；公式、legacy 分支及失败语义由 [acceptance-thresholds.md](acceptance-thresholds.md) 维护，不在方法条目重新定义。

成员在构造期固定；每次调用捕获 Basis，参考和业务轮不因超时、调度跳过、准入或编码失败缩小分母。健康与 freshness 影响调度，不自动移除成员、提高票权或切换为单源。失败原因与支持／反对／弃权证据不能混为一类。引用及新鲜度见 [reference-freshness-policy.md](reference-freshness-policy.md)。

已注册方法按声明的 encoder 交付规范 Value；同簇规范字节不一致按返回值合同失败。代表源只是捕获顺序第一个支持源，不能借它返回未经保护的 metadata。`Corroborated` 是 provider-backed evidence，不是链共识或 finality；Selected／Forwarded 不能静默替代需要佐证的 typed 读取。

合法对象查询的 JSON null 按方法保持，与空对象、空数组和 malformed 区分；接受后的 typed NotFound 不是不存在证明。null／对象异见按实际多数或显式内部模式裁决，不再复述旧“默认全部有效回答一致”的规则。

## 3. 方法族与额外保证

| 方法族 | 保护与比较依据 | 明确未证明／例外 |
| --- | --- | --- |
| `eth_chainId`、`net_version` | 无块参考的身份事实，分别使用 hex quantity 与 decimal；net ID 不与 chain ID 互换 | 身份预检与单次裁决独立；`net_version` 的正值边界缺口已登记 |
| balance／nonce／code／storage | 调用内共同参考上的数量或 DATA；storage word 为 32 字节 | 一致响应不等于验证 state trie proof；tag 与 pending 按专属矩阵 |
| 完整 block（`fullTx=true`） | Header 自哈希、请求身份、完整有序交易 trie root、适用的 withdrawals root、完整 uncle 头承诺；比较键维持方法声明的身份键 | 不重执行 EVM，不证明 canonical chain／finality；头中的根值受承诺不等于加载并验证其全部对应内容 |
| header-only／`fullTx=false` block | 服务 reference／head 读取，允许更窄结果域 | 不得外推完整 block admission 的保证；不能称完整官方 block 对象，见问题记录 |
| 独立 transaction | 方法级完整 key 修正后执行生成 query required／nullable／分支约束及 payload hash、签名／声明一致性；by-block/index 另绑定上下文并拒绝 pending 对象；官方字段和 presence 完整比较 | 不恢复 sender，不证明 inclusion 或 blockHash／height 关系；pending 时间戳的有限同源派生与显式标准勘误见 [transaction 设计](transaction-semantic-matching.md) |
| transaction receipt／block receipts | 官方 receipt 形态的 required／nullable／byte 校验，来源提供的字段经规范编码比较；请求 tx／block 绑定，日志非零 hash 的父子一致性，logsBloom 与日志内容一致 | 不证明收据集合完整、receipt trie inclusion、from 的 sender recovery 或链上执行正确；完整日志要求与零 hash 容忍仍是项目接受政策，不能误写为完整官方输入域；见决策 0068 |
| `eth_getLogs` | 非 null 日志数组，要求完整日志字段，绑定请求 blockHash 或高度范围 | 不是 logs inclusion proof；当前并未核对全部 address/topics filter；只支持 logs 分支而非 pinned schema 的 hash-array 分支 |
| `eth_feeHistory` | 规范数量与 ratio，数组之间的长度关系，响应长度等于请求 count，reward 行宽等于请求 percentile 数（请求感知准入绑定，2026-11-15 起） | 官方允许返回 supported 子区间；高精度 ratio gate 也是项目限制。两项问题均待调整，不在本文认可为更强安全保证 |
| `eth_createAccessList` | accessList 元素的地址／storageKeys 与规范对象比较 | 强制 accessList 比官方顶层 required 更窄；不执行或证明建议集合正确 |
| `eth_simulateV1` | 生成 payload／selector 在 reference 前检查并固定；开放输入保留，封闭对象／字典拒绝非法成员；规范模拟结果按同一参考比较。比较与受保护值都经官方生成模型往返：pinned bundle 未声明的内层 `error.data`（如 revert reason 字节）不参与比较，也不进受保护值——内层失败的佐证只覆盖官方声明的 code/message | 模拟块与日志是合成结果，不证明链上执行或真实 inclusion；合法请求仍可能返回顶层错误，类型生成不能替代完整模拟语义及 provider 证据；未声明的内层错误扩展成员只经 Extensions／raw 通道交付（`TestSimulateV1InnerCallFailureRemainsAResult` 钉住），两个内层 revert 字节不同的源仍可聚成一簇 |
| `eth_call` | 共同执行参考，execution／data 两维裁决，未佐证 data 只删不补 | provider code/message 只是 `Decision.Revert` 上下文，不进受保护 Value；详见返回值合同 |
| gas price／priority fee／estimate | 既有 lower-median 选择及 spread 证据 | 上限 Selected；同值不升级 Corroborated |
| peer count／syncing／listening | 来源本地观测；适用顶层形状准入与 typed projection；SyncProgress 使用官方生成的三个 optional 字段，geth 额外计数由附属及原始来源证据交付 | 上限 Selected；syncing 规范结果执行生成 schema 检查，保留 optional presence；缺失计数不成为同步已完成的独立证明 |
| 写入 | 签名原字节广播与 relative-trust 响应分类 | 不签名、替换、自动重试或管理交易生命周期；不是读取 quorum |
| 未注册／显式 unsupported | 按 catalog 和公开入口规则处理 | “未知方法曾可 Forward”不代表当前已注册只读 `Call` 会绕过 allow-list；不能以未知转发补齐兼容缺口 |

实现入口为 `rpc/internal/objects/{block,transaction,receipt,log,feehistory,accesslist}` 与 `rpc/methods`。表中的“未证明”不是待实现承诺；需要增加保证须独立评审。

## 4. 请求参考、pending 与失败

- latest、safe、finalized 保留请求标签含义，在同一 invocation 内建立并固定对应 reference；不为成功静默换成另一安全级别，也不跨调用缓存 H。
- 显式 hash／高度不暗中换块。EIP-1898 对象按方法保留 `requireCanonical`；provider 接受裸 hash 不使该写法自动成为标准。
- 支持的 pending 路径由请求语义进入本地观测，不能在 quorum 失败后降级进入。typed pending block、pending logs 和 pending block receipts 的拒绝边界以标签矩阵为准，不能概括成“所有 pending 都支持／都拒绝”。
- “单源本地观测”描述证据类别（Support 恰一个来源、Selected 为上限），不是指只发出一个上游请求：pending 形状的采集仍对整个投票基扇出，全部成员都计入 Participants；只是裁决取第一个可规范化答案。
- pending 的 `Decision.Value` 是该来源的原始回答，不做 mined 路线的保护域裁剪：`eth_call` pending 的 revert 保留 provider 信封（code/message），mined 路线则按 rule 4e 删除信封只留 data——typed 投影把两者归一为同一 `*RevertError` 形状，差异只在根 `Call`／raw 通道可见。
- 请求不合法在 fan-out 前失败；单源回答 malformed、绑定失败或无法无损编码形成 Fault，来源仍占分母。有效异见、合法 null、可弃权 data 和传输故障保持不同含义。
- 失败的公开 `CallResult` 不包含成功 Value；内部 resolver 的诊断与公开载体不能混用旧阶段示例。

## 5. 附加证据与验证

当前证据随单次 `CallResult` 交付；`WithResult` 负责交付句柄，`WithSourceEvidence` 配置原始材料预算。旧 `WithUncertain`／capture API 示例已退出现行文档。原始材料不保证可靠或完整，不绕过主结果校验；预算、取消、发布后稳定性和复制隔离以决策 0066 为准。

方法行为沿生产 Policy、Aggregator 和 typed seam 验证；注册表、字段生命周期与场景覆盖双向互锁。合成 regression、捕获回放和外部 provider 观察分别证明不同事情，完整要求见 [method-verification-pipeline.md](method-verification-pipeline.md)。已完成 T 阶段的执行纪律与进度不再作为方法规范保留。

`eth_call` 的 execution／data 边界由 [`TestNormalizeCallOutcomeSeparatesRevertDataStates`](../../rpc/methods/eth_call_revert_test.go) 等回归验证：revert data 的缺失、null、空 DATA 与具体字节不得在没有规则依据时折合，provider 错误 message 不进入受保护 data。

完整 block 的合法空 uncle 集合与外块绑定拒绝由 [`TestEmptyBlockUnclesAreLegalAndForeignBlockIsRejected`](../../rpc/methods/content_compat_test.go) 验证。reference 的失败不触发业务 fan-out、每次调用重新建立及失败证据交付，分别由 [`TestReferenceAnchorFailureKeepsPrepareErrorAndSkipsBusinessFanout`](../../internal/verification/integration/reference_freshness_test.go)、[`TestReferenceEstablishedFreshPerCall`](../../internal/verification/integration/reference_freshness_test.go) 和 [`TestReferenceFailureEvidenceIsUnknownWithObservedAt`](../../internal/verification/integration/reference_freshness_test.go) 验证；这些测试引用说明合同落点，不表示本次文档更新重跑了对应运行场景。
