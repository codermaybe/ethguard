# 项目路线图与新会话入口

本文可直接用于重启对话：说明当前基线、下一步、执行顺序和未完成事项，不依赖旧聊天、`docs/tmp/`或`/tmp`材料。具体业务合同由末尾链接的正式文档维护。

**当前顺序：公共数据管线 A-G 与 27 个注册 read 方法精调已全部验收；非 read 入口盘点完成，正在从独立广播写入开始串行规划、实施和验收。** 不重开已冻结的公共 Interface，不同时启动多个改造方向。

## 1. 当前基线

- 2026-11-20 已完成 A-G 与本轮 27 个 read 方法的代码验收，本轮父基线为 `943133e`。新会话先用 `git log`、`git status` 和未提交差异核对实际实现提交与工作树，不覆盖已有改动。此前 `8e0e77a` 的 AccessList 字段/生成类型复用和 codec 字符串优化保持。
- 项目仍固定Go **1.26.10**。Go1.27.3只做过隔离评估，未改`go.mod`、生产JSON import、生成器或生成资产。**Go1.27暂不引入**，后续整体评估，收益不计入现有实现优化。
- 现行设计文档系列、公开`ethapi`类型迁移和首轮逐模块整理已完成。目标仍为固定go-ethereum v1.17.10的47项目标方法名称集合，已有27个注册读RPC以及独立写入、订阅和派生能力；这些数量不代表完整兼容。
- A-G 公共管线已验收：准备事实冻结、显式 reference evidence、来源槽位与传输所有权、Outcome 裁定、双 facade 交付/观测均已核对；frozen params transport 仍等待端到端瓶颈证据，exported ReferenceStage 的兼容风险留待发布说明。
- 两个交易查询与单收据strict canonical路径已接入。receipt复用已校验canonical字节；AccessList仍使用现有方法规则和生成模型，不以新增processor数量衡量完成度。
- FeeHistory 准入已复用完整解析模型的 count 事实，删除生成后丢弃的比较键和独立 range 重解析；精确小写 reward 绑定仍独立。各阶段没有通用跨调用模型。共享 Log 继续保留 Validate 与 EncodeOne 两段处理，仅去掉方法包装器重复的顶层数组解析。receipt raw 比较键去掉无业务变换的 JSON 往返；默认 canonical processor 不变。

### 已知成本与验证边界

固定Go1.26.10，本轮AccessList的64项对象编码约由1.160ms降到0.829ms，分配次数约3638降到2480；真实Aggregate的1/3/5来源大列表耗时约下降9%/12%/13%。这些是本地固定fixture的对照，不是生产延迟或CPU使用率。相对最初手写编码仍有成本差距，尚未接受所有性能回退。

变更相关全仓测试、race、vet、build、tidy、架构／方法互锁、生成新鲜度及字符串／AccessList各10万次定向race fuzz通过。65项差分矩阵的接受、输出字节和本矩阵错误文本与原基线一致；有限矩阵不等于完整输入域证明。

额外JSONsafe race fuzz完成100万次。额外wire normalizer race fuzz在最后报告654606次、运行超过12分钟后人工中止；未观察到反例，**不算通过，仍待独立诊断和完成**，不直接归因于环境或新代码。未进行本轮真实provider采集、Anvil重跑或正式发布。历史证据不代替未来目标提交的验收。

可复现入口在已跟踪源码：

- 对象基准：`rpc/internal/objects/accesslist/bench_test.go`；归因基准：同目录`cost_decomposition_bench_test.go`。
- 真实聚合基准：`aggregator/accesslist_route_bench_test.go`，覆盖空／典型／64项和1/3/5来源；同一fixture保留冻结旧encoder控制组。
- 独立字节对照及fuzz：`internal/ethcodec/helpers_test.go`；条目接受域与顺序反例：`rpc/internal/objects/accesslist/acceptance_domain_test.go`。
- Log真实消费者反例：`internal/typed/log_alias_projection_test.go`、`rpc/internal/objects/receipt/log_alias_test.go`。

临时日志可以缺失；根据以上基准、测试和Git历史重新验证即可，不能因临时文件不存在阻塞接续。

## 2. 阶段一：公共数据管线已验收

管线概览：调用输入 → 请求准备／冻结 → 执行计划与reference → 来源调度／采集 → 逐源处理 → 聚类／裁定 → typed结果、证据与最终记账。

这是审查数据依赖的顺序，不是要求机械移动现有检查。先沿真实调用链说明每段输入、输出、谁拥有数据、何时需要复制；复用已完成的工作，保留必要的独立检查与错误阶段。方法专属schema／绑定／算法知识继续由方法和对象模块拥有。

| 顺序 | 工作 | 完成条件 |
| --- | --- | --- |
| P1 | 调用入口、参数检查、所有权与冻结 | 调用方值不被修改；用户marshaler不重复执行；语义请求、wire参数与记录保持一致；失败在正确阶段返回 |
| P2 | 计划、模式、reference和路线冻结 | 声明与实际能力一致；应建立reference时只有一条明确路径；失败不发业务请求；后续消费冻结事实，不反复推断 |
| P3 | 来源选择、调度、采集与原始响应交接 | 区分未尝试、gated、传输失败与返回值；固定成员分母；原文保真，重复信封处理／数据转换有明确理由 |
| P4 | 逐源结果到裁定、交付与最终记账 | 复用已验证表示；梳理canonical构造、getter、分簇、排名、commit间的复制；内部只读消费与对外独立副本分清；fault、健康、证据和清理各有唯一完成责任 |

**新会话不再从 P1 重做。** 本表保留已验收的责任合同。需要修改时，先核对当前工作树和对应正式合同，再为具体方法或独立专题制定计划；不重新执行公共 Interface 收敛、全系列文档整理或公开类型迁移。

阶段一不要求新建通用框架、大型处理结果对象或公开API；已有ReadPlan、CanonicalResult等表示优先沿用。发现相似代码先核实是否重复做同一工作，不以helper合并或源码行数减少代替收益。可以选择方法作为管线验证样本，但不提前展开该方法的全面精调。

阶段一已完成，公共流向、数据交接和生命周期已由反例、全局测试/race及相关 benchmark 核对。本结论不将冻参 transport 延期项或外部兼容风险自动关闭。

## 3. 阶段二：本轮 27 个 read 方法精调已验收

本轮范围是当前 27 个注册 read spec，逐个建立输入/输出域、请求关系、依赖查询、错误阶段、主值/附属证据和处理次数，再由执行模型书面交付、规划模型验收。保留现状的方法也分别记录理由。以下表格区分本轮已核对的实现与仍需独立裁决的政策；不把 read 精调等同完整模拟语义、写入/订阅专题或真实 provider 兼容。

| 方法方向 | 本轮结论与独立未决项 |
| --- | --- |
| 标量与本地观测 | 默认 strict canonical-bytes 每成功源一次 Encode（内部 normalize），比较/提交复用字节；by-hash count 保留 normalizer-key 与 null；Median/CallRevert 仍各有两次规范处理及不同错误阶段；Local 保留首合法来源、空 provenance/零 representative、选中源编码失败不改选。net_version 实测接受零、负值、加号和前导零；当前无正值 gate，是否收紧及与 ChainID 的关系另行裁决 |
| FeeHistory | 准入整对象解析 3→2，丢弃 key 构造 1→0，count 复用模型；精确 reward 与 struct alias 不合并。Number/ratio、supported 子区间、optional presence 政策仍独立 |
| AccessList | 现有生成类型/字段复用及错误顺序保留，未创建第二套 prepared 模型。顶层必需/null 折合是现行方法规则，未静默放宽 |
| Log／Receipt | logs 包装器顶层解析 2→1；receipt raw Normalize 额外 key decode/encode 各1→0。共享 log exact-key 准入与编码 alias 双遍保持，查询、receipt、typed订阅和同源 Pull 反例通过；默认 receipt processor 继续复用已校验字节 |
| Transaction／Block／Header | 两个交易 Candidate 单次准备及各自 pending/mined 域保留；block 生产按完整 canonical bytes 聚类，独立 Normalize/reference identity 不是默认窄键。header-only 的保证与完整体绑定/commitment 区分；geth 签名/hash/bloom/trie/uncle 转换保留，旧注释已纠正 |
| CallRevert／Median／Local／SimulateV1 | 分别核对裁定、presence、reference/pending、typed 与 Selected 上限。Simulate 用户 payload/base marshaler各一次，生成模型与 trust gate 各有消费者；完整模拟语义和 provider 观察仍独立 |
| 写入／订阅 | 不属于这 27 个 read spec 精调的新增实现范围。已有冻结/尝试/同源Pull消费者反例保持，未决政策进入独立专题 |

保留项也须明确说明理由。生成模型存在不代表可删方法规则；解析函数共用不代表同一响应只解析一次；typed边界的独立检查不自动属于浪费。

本轮局部成本证据：投影 wildcard 完整交付 176→136 allocs/op；FeeHistory 1/64-block 准入中位耗时约降 15.8%/15.1%；logs 1/64-row 准入约降 8.2%/9.7%；receipt raw narrow-key resolve 约降12.7%，默认 canonical-bytes 控制分配不变。纯标量投影耗时持平。这些是本地固定 fixture 的局部测量，不能等同网络 Aggregate、生产延迟或已接受全部性能差距。

当前生产代码的 `go test ./... -count=1`、`go test -race ./... -count=1`、`go vet ./...`、`go build ./...` 已通过；重新生成的 Go/审计资产与受跟踪文件逐字节一致。上述结果是本轮工作树验证，未新增真实 provider 采集，未解决前述独立 wire 大预算 fuzz 待办，不代表正式发布验收。

### 下一会话：27 个 read spec 之外的方法与能力

2026-11-20 用户确定下一轮推进方向：考察上述 27 个读 RPC 之外的方法。这些入口的反馈、可交付信息、证据与锚点关系更复杂；当前只确定方向，尚未选定具体方法或裁决新政策。

1. 先结合 catalog、公开 facade 和 [覆盖清单](method-transport-coverage.md) 做源码事实盘点，区分独立写入及同步等待、订阅、派生与生命周期入口，以及显式 unsupported 能力。47 个上游公开方法与 27 个注册读 RPC 的口径不同，不能用相减确定剩余任务，也不能将 facade 名称不同自动当成新 RPC。
2. 为候选方法说明：反馈表示已接受、已观察、已确认还是结果未知；成功值、错误、诊断和来源材料分别包含哪些事实；请求／区块／交易／会话锚点从哪里建立、何时失效，以及等待、重组和恢复时是否仍成立。继续核对成员与门槛、原文与规范值、顺序与所有权、取消／关闭、重试或重放边界；由现行合同和反例支持结论，不把读方法的裁定与 reference 规则机械套用。
3. 选定一个方法后才制定独立计划，列出调用者、Interface、接受域、错误阶段、变更文件、反例、必要成本证据、完成门槛及未决政策。继续由主控规划和验收，`gpt-6.1-sol/high` 执行子 agent 串行实施；执行者通过聊天书面交付，规划验收者维护文档。阶段计划仍写现有 module-simplification README TMP，验收后迁移结论并清空；不新增阶段目录或总结文件。

写入的 accepted／unknown／rejected 与链上观察关系归 [方法合同](method-contracts.md) 和 [返回值合同](return-value-design.md)；订阅建立、同源 Pull、恢复与终止归 [订阅合同](subscription-contract.md)；锚点保证归 [reference 合同](reference-freshness-policy.md)。现行保证与待裁决事项分别记录，unsupported 不自动变为新增实现任务。F1／S1、工具链、provider 证据和发布缺口继续独立维护。

### 非 read 入口推进状态（2026-11-21）

主控负责计划、合同裁决与验收，执行 agent 使用用户授权的 `gpt-6.1-sol/high` 或 `gpt-6-astra/high`。同一时刻只有一个代码执行任务；后续模块可只读盘点。每项先在现有 README TMP 批准计划，执行者仅改代码/测试，主控核对后将长期结论迁回既有合同。

| 顺序 | 入口与验收责任 | 当前状态 |
| --- | --- | --- |
| 1 | SendRawTransaction、SendTransaction：广播反馈、签名/hash 绑定、来源拒绝材料、固定门槛 | v1 已验收；拒绝证据保留 data presence 与原始 JSON |
| 2 | SendRawTransactionSync、SendTransactionSync：广播与收据观察的组合、超时/取消、证据与重组保证范围 | 规划入口；先核对可交付事实，保留现行读写 Decision 互斥 |
| 3 | SubscribeNewHead：事件头身份、同源 Pull/gap、session 恢复与取消 | 只读盘点中，尚未批准实施 |
| 4 | SubscribeFilterLogs：过滤/removed/前驱、同源补查与来源归属 | 未开始 |
| 5 | SubscribeTransactionReceipts：查询快照、批内上下文/过滤子集、同源补查 | 未开始 |
| 6 | TransactionSender、Header 视图、AnchorBlockHash/ForTag/Height：派生与锚点绑定、事实交付 | 未开始；复用已验收 read 内核 |
| 7 | Close/Unsubscribe/取消及显式 unsupported：资源完成、错误与能力边界 | 未开始；跨入口最终验收 |

以上是已有入口的合同与实现审查，不把 `47−27` 作为待办数。S1 未定公开配置/事件 API、F1 默认值、真实 provider、工具链和发布保持独立；无收益的实现允许有证据保留。

## 4. 阶段三：独立运行期专题与工具链评估

| 专题 | 当前事实与未决项 |
| --- | --- |
| F1 freshness | 已有请求scope和tip样本过期机制；默认lag、样本窗口／最低覆盖、豁免、结构化原因及finalized／单源组合未定，不能从零重新实施或自行选默认值 |
| S1订阅 | 已有快照、会话、固定门槛、同源Pull及恢复；Close／终止错误、单源模式、预算／背压、generation、恢复范围、诊断和root事件API仍需统一 |
| Go与JSON | Go1.27暂缓引入。已知保留旧JSON API也能受益于新内核，但B/op可能增加；直接使用json/v2默认语义会改变大小写匹配、重复键、UTF-8、空数组和omitempty，需独立验收 |
| 验证维护 | 手工feature／noCapture登记、cell到真实断言的追溯、canary阈值互锁、时间敏感测试及未完成wire大预算fuzz，分别诊断，不用自述表代替独立断言 |

这些专题保持现行行为，不成为阶段一等价整理的隐含改动；需要改变保证时由主控明确目标、影响和验收，再交执行代理实施。

## 5. 阶段四：外部证据与发布验收

补齐实际所需的交易类型生命周期、真实provider方言／状态／订阅观察、跨传输乱序／背压／重连／取消和材料证据。离线、Anvil与真实provider分别记账，既有捕获与日期化证据不改写成新观察。

对最终目标提交执行发布合同的完整门禁、兼容／API报告、构建与产物检查。内部优化、有限矩阵或一次全仓测试通过，均不能推出完整provider兼容或稳定发布已验收。没有明确授权不推送、发PR或发布。

## 6. 所有阶段共同保持的边界

- 官方字段／标量规则归已审计来源和生成层；方法关系及绑定归方法模块；coordinator不解释Ethereum schema。
- 保持冻结成员、分母、裁定门槛、代表源、错误类别与阶段；正常dissent不成为健康失败。每次真实尝试最终记账一次。
- 原始响应、canonical主值、Provenance、Diagnostics和Extensions职责不同，不能互相补字段或冒充来源材料。
- canonical处理只在现有合同允许的路线使用；pending-local／Forwarded保留其语义。Block、Median、Local、CallRevert、写入和订阅不强行套用同一处理器。
- 不为性能引入跨调用缓存、自动重试／写重放，或借新的统一表示改变接受域。必要算法桥、信任边界检查及对外交付隔离继续保留。
- 在已确定合同内连续完成任务；普通失败自行修复。未决政策默认保留当前行为，记录后继续可独立执行的事项，不设等待用户回复的常规中间审计点。
- 主控负责合同和设计判断；轻量模型执行明确的小范围实现、验证、测量与记录。整段确定性工作结束后统一审计，不把各代理的“完成”直接作为验收结论。
- 允许完整重写有效文档，避免多代说明拼接；阶段材料只存`docs/tmp/`并忽略，不新增`docs/design/tmp`，不force-add临时日志。正式合同、当前路线图、必要测试及基准进入Git。

## 7. 正式合同入口

| 需要了解 | 负责人文档 |
| --- | --- |
| 模块职责、ReadPlan与执行路线 | [代码库拓扑](codebase-topology.md) |
| 方法保证、分母、门槛、请求绑定及待裁决差异 | [方法合同](method-contracts.md) |
| Value、presence、typed、原文与附属材料 | [返回值合同](return-value-design.md) |
| 官方来源、勘误、生成及新鲜度 | [生成合同](wire-codegen.md) |
| tag／pending与传输能力 | [引用矩阵](moving-tag-matrix.md)、[覆盖清单](method-transport-coverage.md)、[兼容政策](go-ethereum-compatibility-policy.md) |
| F1／S1 | [reference合同](reference-freshness-policy.md)、[订阅合同](subscription-contract.md) |
| 行为证据与发布检查 | [验证合同](method-verification-pipeline.md)、[发布合同](release-contract.md) |
| 历史原因 | [决策档案](../decisions.md)，ID和日期保留，旧Status不自动支配当前实现 |

`Corroborated`表示provider-backed cross-source evidence（配置provider之间的佐证），不是Ethereum consensus、canonical chain或cryptographic finality证明。正式行为以各owner合同为准；本文确定接续顺序与工作状态，不成为第二份方法规范。
