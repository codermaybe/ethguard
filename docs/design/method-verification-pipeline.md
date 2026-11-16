# 验证、捕获与证据合同

状态：2026-11-17 文档基线。本文统一方法验证、字段与场景、捕获／golden、Anvil、工具所有权和证据保留。生产语义见 [方法合同](method-contracts.md)，执行组织见 [拓扑](codebase-topology.md)，目标提交的正式门禁见 [发布合同](release-contract.md)。当前覆盖状态只在覆盖清单和路线图维护。

## 1. 证据分层

| 证据 | 能证明 | 不能代替 |
| --- | --- | --- |
| 官方原始向量／独立 schema oracle | 固定来源的结构和值约束、生成语义 | 方法绑定、实际链状态和 provider 行为 |
| 本地协议与故障测试 | 指定响应／故障下的公开值、裁定、错误与清理 | provider 曾实际返回该形态 |
| 本地 Anvil 真实进程 | 受控 pending／mined、回滚和固定历史对象关系 | 真实主网历史、provider 方言或独立多源 |
| 真实定点采集 | 某来源某时刻实际请求与回答及 provenance | 永久兼容、来源独立性或执行正确性 |
| 离线回放 | 冻结记录经当前生产路径的行为 | 新的外部观察 |
| 外部 canary／artifact recheck | 固定提交、成员和 plan 的观察及产物绑定 | 链共识、finality、长期可用性 |

默认测试不接触公网，允许 loopback 对端。真实采集与 Anvil 是显式入口；合成、派生故障和 Anvil JSON 不能冒充 provider record。digest 绑定内容，不单独证明来源真实性。live canary 的 state／manifest／report 和 workflow 例外由 [canary 合同](live-canary-contract.md)独立维护。

## 2. 方法登记与规则互锁

MethodSpec／CompilePlan／ReadPlan 是运行时声明；method-rules.md 是只读生成投影，moving-tag-matrix.md 是人工行为对照，二者不成为第二套运行时字段表或 DSL。

- Registered inventory、官方 schema 映射、引用矩阵、字段生命周期与场景声明双向互锁；缺行、重复和失效测试名失败。
- 无官方 result schema 的 net_peerCount／net_listening 显式登记 noSchema；上游收录后移除豁免，不能静默沿用。
- 引用矩阵保持 16 列和每方法唯一行，Tests 只接受可由 Go AST 找到的完整 Test 名称；测试存在不等于每个 cell 已有行为断言。
- `TestMethodRulesMatchIndex` 从代码渲染并逐字比较，默认不写文件；仅显式 ETHGUARD_UPDATE_METHOD_RULES=1 更新。TestAdmissionIndexRow 另核对 admission 声明。
- 规则索引报告方法、reference、pending、resolver、compare、key source 和 result，不解释 eth_call 全部分支，也不新增注册维度。pending 使用 local-observation／local-reject／none。

## 3. 字段生命周期与安全边界

原始响应经 JSON 安全检查、有限兼容、对象／方法检查和规范表示，进入既有裁定；typed 只消费最终 Value。raw 路线的 Admit／Encode 与 canonical processor 路线共享目标业务合同，不能把物理 hook 顺序强制统一。

每个受保护字段有清晰的 required／optional／derived／Value／Extensions／Diagnostics／删减或 fault 去向。字段的保护依据是比较、请求绑定、已验证承诺或公开派生规则；进入 mask 或 encoder 不单独证明有效性。

| 差异 | 必须验证 |
| --- | --- |
| 合法表示变化 | 键序、空白、允许的 hex 与字符串 escape 形成同一规范结果；DATA 的长度／前导零仍有意义，严格 QUANTITY 的非法前导零不能被一概视为合法 |
| 非法／缺失／错误绑定 | 即使全部来源一致也拒绝，不借省略、补零或混源字段制造成功；保留原错误阶段及分母 |
| 合法语义异见 | 保留 dissent，不一律处罚健康；合法 null 与空数组、malformed 区分 |
| 显式弃权 | 仅在方法声明的维度使用；eth_call data missing／null 弃权，空 DATA／具体 DATA／raw JSON 是不同断言 |
| 域外字段 | 不改变主结果分簇与支持集合；按附属比较和预算内原文交付 |
| 往返与内容守恒 | 原始合法输入、规范 Value、公开生成模型中的受保护语义一致；Block size、交易 from／上下文和 receipt presence 不因私有 geth 再编码消失 |

测试分别验证来源是否可计票、最终裁定、错误类别／链、来源归因、reference、健康最终记账及材料。失败、gated、弃权和未尝试仍保留固定成员；不得为通过重算 N、降 floor 或选另一 source 补字段。

## 4. 决策场景与公开调用链

`RegisteredNames()` 每个方法至少覆盖 `all-agree`、`minority-dissent`、`outlier`、
`single-fault`、`all-fault`、`conflict`、`notfound`、`pending/local` 八类，并以
`quorum-boundary` 独立检查门槛。现有测试把 pending/local 表达为 `pending` 行，
共九个显式行；每个方法逐行调用断言 helper，AST 互锁要求名称各一次、调用各一次，
不用遍历所有方法/场景的通用 runner。静态互锁不替代运行时语义断言。断言同时覆盖：

- `Unknown`、`Conflict`、`Selected` 及方法允许的 `Corroborated`；
- 固定分母下 Support、Reject、Faults、各维度 Abstain；
- 代表源与 Value 的同源/规范编码关系；
- Policy、真实 Aggregator 和 typed adapter 的公开结果。

场景矩阵是进程内合成证据，不进入 provider capture 目录、不产生 provider
兼容性结论。错误、admission failure 与 reference 结果必须沿生产边界构造，
不能用 mock 绕过实际门禁。EncodeCheck、Admit、typed decode 失败不得改走
单源值；`Selected`/`Forwarded` 只能由已有方法契约产生，不得为测试方便升格。

合法异值是 dissent，不是 fault；合法 null/空结果/notfound 不等于 malformed。
不适用的场景也保留显式行并说明契约结果，不能为所有方法强套 Corroborated。
已有 syncing 合成矩阵与公开 projection 用例分别保留；新增合法对象场景须有独立预期，不能把合成 progress object 标成真实 provider 观察。

固定 N、floor 与 Q 要独立核验门槛两侧，例如 N=5、floor=2 时 2 个支持者不足、
3 个支持者达到门槛；不只比错误文字。Unknown 行只有 conflict 且显式声明 ValueFrom
时可保留簇 Value；all-fault、notfound、本地拒绝和 ValueFrom<0 的行断言空 Value。
按实际方法契约区分 all-agree、合法少数异见、全故障与 pending-local 的状态，不通过
删除 fault 来源、放宽 mode 或下降动态门槛制造成功。

## 5. Raw／canonical 与跨模块验收

只测 resolver 会漏掉前置 admission、typed 和健康／材料归属。baseline 与候选应使用相同编译计划、请求、fixture、来源身份与固定顺序，经过真实 Aggregator 或公开 Client；canonical 对照 baseline 仅清空 ProcessCanonical，保留实际 Admit 和 resolver。不用手工 CanonicalObservations 代替 coordinator。

比较 Status、Value、具体 Support／Reject 成员、Representative、错误类别与必要文本、SourceFaults／Faults／CollectionFaults、Provenance、Diagnostics／Extensions、原始材料及健康；忽略无业务含义的时间与并发顺序时按 source identity 对齐。不能仅比较 fault 数量或两条共享 helper 的输出，需独立预期与不可合并反例。

processor 计数是 hook 次数，不是 JSON 解析次数。成本分别报告耗时 ns/op、B/op、allocs/op；ns/op 不是 CPU 使用量。相同负载、初始化计时外、可重跑原始结果是前提，不能以噪声或单点计数推导整体提速。

## 6. 捕获与 golden

G1：普通 expect/v1 的 transport 仍只接受 http；raw WSS 的合成保真、真实帧观察和普通 HTTP golden 是不同证据，不能自动互相关闭缺口。

G2：只为方法声明的 reference 建立上下文，logs 使用高度范围、feeHistory 仅改 newestBlock 为高度；chain/local/median 无 reference 不虚构 anchor。safe／finalized 分别建立，不退回 latest。

G3：golden 区分 wire／shape、Corroborated、Selected／local、Fault／Forwarded。median spread 是诊断；local、median 不套 Strict 成功公式。固定数值是历史回放预期，不是未来 provider 承诺。

G4：回放离线；网络只发生于显式采集。G5：N 是 Aggregate 入口冻结 Basis，失败和 gate 不缩分母；expect.n 与 sources 数量一致是记录完整性，不代表每个 resolver 成功条件相同。

### 捕获目录、不可变记录与 golden

```text
internal/rpcsample/testdata/<feature>/<batch-id>/
├── batch.json       # ethguard.batch/v1；批次 provenance、可选 reference_context
├── index.json       # 每条 record 的来源、请求、时间与 SHA-256
├── expect.json      # ethguard.expect/v1；冻结观察的回放预期
└── records/
    └── <request-id>--<source-id>.json
```

- record 的 `request` 永远是实际发出的 wire 请求。请求参数不在 `batch.json`
  重复；逻辑请求与 reference 的关系按下文 reference_context 单独记录。
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

### expect.json 是数据

每个 case 至少包含：

1. `schema_version`、请求标识与实际 wire 请求、`transport`、成员数 `n`、mode、floor；
2. 每源比较键、准入结果和 Faults；
3. 每个受保护维度的 Support、Reject、Abstain、Returned；
4. 是否发生删减投影、被扣除字段、最终 Decision.Status/error。

不得引入条件表达式、继承、`Prefix*` 或自动生成测试逻辑。CI 回放与 expect 比较；
改变分类/裁决的变更必须附可解释的 golden diff，不能盲目 `-update`。

### 新批次接入

- feature 和 batch-id 随批次登记，不能改全局 `sampleBatchID` 而覆盖历史批次。
- 新 feature 同时加入 `TestRegisteredMethodSampleStatus` 遍历列表、其他 feature
  遍历入口及该批次的 expect 回放入口；不要在目录尚不存在时提前放空占位。
- 对已归档方法更新 `internal/rpcsample/replay/batch_expect_test.go` 的 `noCapture`。
  这个 map 的清空不证明必需 case 已全覆盖。
- `TestRegisteredMethodSampleStatus` 只证明 record 中出现方法，既不证明有 expect，
  也不证明 expect 被执行；必须同时接通真实回放和 case 覆盖断言。

**G4：回放离线。** 捕获可在受控环境访问真实 provider；提交的是脱敏冻结记录。
测试只允许进程内 `httptest`，不允许外部网络 I/O。

### Reference 上下文与实际请求

**G2：只对声明了 block-reference 的方法建立相应 reference，不给无参数、
chain-local 或 variable 方法虚构 anchor。**

| 方法形态 | 捕获/回放要求 |
| --- | --- |
| `eth_getLogs` | 过滤对象 `fromBlock`/`toBlock` 按 reference **高度**改写 |
| `eth_feeHistory` | `[blockCount, newestBlock, rewardPercentiles]` 只有参数 1 是 block-reference，改写为**高度**，不是 hash |
| 无 block-reference 参数的 chain/local/median 方法 | 按自己的 wire 形态捕获，不新增一致性维度 |

`safe` 与 `finalized` 各自建立，不回落 `latest`。上游不支持时换源；所有可用源
都不支持则登记未覆盖，不伪造 record，不放宽要求，也不阻塞无关方法的捕获。

### batch.reference_context

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

### 覆盖等级

L1 要求真实 batch/index/records/expect 齐备，sample-status、实际 expect 执行和回放通过；目录有 record 不足以标完成。L2 要求必需 case 的真实内容覆盖：syncing false／对象、logs 空／非空及 safe/finalized 上下文、feeHistory safe/finalized 上下文等。未观察形态不能合成填补；noCapture 清空不证明覆盖完整。

原始 record/index 除脱敏修正外保持不可变，格式或 fork 变化新增批次。expect 改动须解释分类／裁定差异，不盲目更新自证。provider 错误、限流、空 body、null 和合法空结果分别保留。

## 7. 本地链与外部场景

现有 `internal/testkit/anvil` 与 `internal/verification/anvil` 通过 anvil build tag 隔离，已固定 Anvil 1.7.1／commit、shanghai、chain ID 31337、genesis 与仅限本地账户。缺工具或版本不符在显式选择专项后失败，不能静默 skip 称通过。

```sh
go test -tags=anvil ./internal/verification/anvil -run '^TestTransactionLifecycle$' -count=1 -v
```

启动仅监听 loopback，动态端口，有界就绪检查核对 chain ID／区块／账户；正常、失败、取消和启动异常都清理并等待进程。生命周期实际验证 pending → mined、by-hash／block-index／receipt 关系、推进后固定历史对象，以及 snapshot/revert 后可见性与重启隔离。不能只以 revert 返回 true 证明全部状态恢复。既有 88 条消费断言属于日期化本地证据，文档整理未重跑它们。

fork 是独立在线专项，需固定远端 chain、height 与 hash、版本、预算和截止。dump/load 或 cache 不证明离线可用；必须阻断公网后重跑所需请求证明。真实历史、moving tags、EIP-1898 和更多类型按实际依赖扩充，不把完整平台作为所有规则修复的前提。

本地故障复用 httptest、RoundTripper 和 internal/testkit/wstest；net.Pipe 可用于合适 seam 的阻塞测试，但不提供 TCP half-close。不引入第二套 typed decoder／执行链，也不为测试扩生产接口。真实定点采集复用 rpcsample，固定 canary 不承担任意采集计划。

## 8. 工具所有权与材料保留

| 工具／资产 | 所有者与职责 |
| --- | --- |
| rpcsample | cmd/rpcsample、internal/rpcsample；通用 plan 录制 index/records，transaction scenario 另输出完整批次与 provenance，均不判兼容 passed |
| replay | internal/rpcsample/replay；摘要、reference_context、policy 与端到端回放 |
| shapeprobe | 固定参考上的形态分类；观察不建立业务合同 |
| livecanary | 固定 registry／成员／plan、报告及重跑；真实产物在 artifact，不写入 golden |
| wsscapture／nativesmoke／soak | 显式手动取证与诊断，不进入普通 CI，不扩张方法保证 |
| specbundle／specdiff | 原始规范裁剪、变更和迁移审计，不是运行时 schema 解释器 |

工具与证据消费核心运行时，核心不得反向导入工具、verification 或 testkit。cmd 是 binary，internal 工具包的反向依赖由 architecture interlock 检查。

活动样本存放 internal/rpcsample/testdata、internal/livecanary/testdata；被测试引用就有归属。docs/samples 是有日期和采集说明的归档，独立原始证明价值不因无测试引用而消失。真实失败记录也不能被合成替代。原始输出入树须有活动测试或归档归属；仓库外产物可记录定位与摘要，不能冒充可下载 attestation。

默认 CI 不采 provider；依赖下载、漏洞查询和显式 canary 的网络需求分别说明。完整发布与工具版本由发布合同维护，本文不重复另定 fuzz 或产物清理政策。

## 9. 验证所有权与待优化事项

公开 root/view 的黑盒与跨模块用例归 internal/verification/facade、integration；需要私有状态／归因的白盒归实现包。共享 fixture 的真实消费者和接口决定归属，不按文件数拆包、不为迁移导出生产符号。

待后续代码阶段评估：feature/noCapture 的手工登记、cell→行为断言的可追溯性、协议类别与能力元数据重复、canary requiredSupport 与生产阈值的行为互锁、并发测试中的时间等待。它们是维护候选，不是已证明缺测或运行时故障；不能用新的自述表替代独立断言。

本轮仅文档一致性检查。后续实现报告实际修改、场景、命令、退出码和未观察范围；受影响包回归按改动选择，发布门禁按目标提交独立执行，不沿用历史通过日志。

## 10. 日期化观察线索

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
