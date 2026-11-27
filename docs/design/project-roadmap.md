# 项目路线图与新会话入口

本文可直接用于重启对话：说明当前基线、下一步、执行顺序和未完成事项，不依赖旧聊天、`docs/tmp/`或`/tmp`材料。具体业务合同由末尾链接的正式文档维护。

**当前状态（2026-11-27）：ethapi 标准生成与本轮 P0–P3 性能切片均已通过本地独立验收，并按业务语义保存提交。** 性能轮父基线为 `c8c7f82`（`audit/rpc-quality`）；用户授权先保存，再借助独立 agent 对整条分支做整体评估并修复确认问题，本轮不做 beta 发布。整体评估结论尚未返回，不等同此前两位执行者的交叉审查。主控规划与验收，最多两个执行者，总并发为 3；未推送。

此前公共数据管线 A-G、27 个注册 read 方法、响应模型、广播/同步写入、Sender/Header/Anchor、NewHead/Logs/Receipts 及跨入口生命周期均已验收。旧订阅 channel 和事件聚合桥已删除。质量治理七切片和最小安全升级也已验收并保存为本地提交，未推送；Go1.26.11 基线普通/race各52包7096测试事件、17规则默认/anvil和漏洞复扫属于该历史范围，完整边界见发布合同，不替代本次 ethapi 验收。

## 当前接续：保存性能改造，整体评估后修复确认问题

本轮代码提交按职责分开：字段聚合 `026b967`、扩展投影 `7b7493b`、根标量 JSON 验证 `a17ea9c`、固定 hex 生成 `8aa7e9d`、BlockReceipts canonical 复用 `b1ba5b0`、Forwarded 路线复用 `ebd33ae`。提交不改变公开签名、固定成员与门槛、完整 Evaluation、原始来源证据、错误阶段或指纹合同。

- 聚合直接收集已验证树；仅全部有效结果原字节完全相同时复用解析，混合结果保留通用路径。标准观察投影只收集扩展；空扩展仍解码 core，省去重编码和哈希，保留浮点溢出等原错误。
- JSON 仅以标准库接受合法根标量，容器和任何失败仍走原 Token walker。固定 hex 只覆盖三个精确 pattern，规则归属见 [生成合同](wire-codegen.md)。不扩大大小、深度或键数预算。
- BlockReceipts 复用每条已验证的 canonical 字节；Forwarded 按 resolver 能力复用 processor，pending-local 和自定义 raw-only／SingleSource 保持原路线。整列表安全门、首错顺序及内部故障归因见 [代码库拓扑](codebase-topology.md)。

最终组合普通/race 各 54 包、7994 测试及子测试通过，8 包无测试文件，无失败或测试用例跳过；JSON、固定 hex、字段聚合三组新旧差分 fuzz 各通过 100 万次（输入预算 64KiB，大边界另有确定性测试）。vet/build、tidy、固定 lint 2.15.0 默认/anvil、anvil 编译和生成源码/audit 一致性均通过，未运行真实链。首次构建曾有旧沙箱模块缓存只读警告，退出 0。

本地串行基准以已验收 P0 为 P1–P3 基线：BlockReceipts 真实调用链 B/op 下降约 27%–34%，Forwarded 四方法约 22%–41%；端到端延迟仍约 19ms，无稳定改善。根标量典型 Validate 从 8 次分配降至 0；1MiB 字符串末尾非法转义的拒绝耗时约增加至 1.78 倍，保留此取舍。整容器预筛因非法前缀加大尾部导致拒绝成本放大已撤回。性能测试与冻结参考保存在对应源码包；本地表格/原始日志仍按临时材料规则保存，不成为第二份合同。

后续先以本分支与 `master` 的共同祖先为范围做一次整体评估，确认问题按业务语义修复和提交。Logs 的 timestamp 错误阶段、Block fullTx/uncle 处理、reference/capture 简化、环境预算、IPC/geth 适配、真实 provider/Anvil 和发布均未因本轮自动完成。

## 前序基线：标准生成与外部适配待办

局部入口为 [ethapi README](../../ethapi/README.md)，来源、勘误、生成和表示的唯一详细合同为 [wire-codegen](wire-codegen.md)。本轮保留官方原件和原三份勘误，新增 22 份 DATA 勘误覆盖 64 个节点，显式修正完整字节约束。构造器先检查有效 schema，再执行合法值表示政策；精确字段名、封闭对象、数值 const 和组合字符串身份均有独立反例。开放结果投影可能改变组合分支时，生成期必须证明稳定，否则拒绝生成。该证明不进入库运行时。

消费者既有大小写、HexByte 及字段投影兼容已移至明确前置 owner，原始来源证据保持；CallMsg／Simulate 原始请求和直接标准构造仍按有效 schema。公开模型声明及函数签名保持，Go 和 audit 均可从固定来源重新生成。本轮不补整项缺失 schema 的通用注册机制，不升级来源或依赖。

成本分别记账：用户确认生成期成本不作为库调用性能问题，运行时必要规则可以付出少量性能成本；不由此接受任意回退。去除重复 transaction 解码和无变化重编码后，固定本机样本三次中位值如下，基线 `981683e` → 本轮实现：

| 运行路径 | ns/op | B/op | allocs/op |
| --- | --- | --- | --- |
| TypedProjectionRawBlock | 387892 → 425723 | 86715 → 102941 | 1376 → 1709 |
| TransactionByHashNormalize | 202185 → 245127 | 62499 → 84345 | 1220 → 1616 |
| TransactionByHashAdmit | 242602 → 282763 | 64243 → 86107 | 1284 → 1680 |

复现入口为 `internal/typed/typed_projection_cost_bench_test.go` 和 `internal/methods/transaction_bench_test.go`，参数 `-run '^$' -bench '^(BenchmarkTypedProjectionRawBlock|BenchmarkTransactionByHash(Normalize|Admit))$' -benchmem -benchtime=500ms -count=3 -cpu=2`。耗时约增加 10%／21%／17%，分配也增加；构造器局部样本的分配下降不代表消费者整体下降。这些是本地固定 fixture，不代表网络或生产延迟。

首次全仓普通验收失败后，已修复依赖旧宽容构造器的 Block／TransactionSender／Simulate 消费入口及测试 helper。另一个 Receipts reorg 测试在旧 HEAD 和当前均复现，原因是异步两源通知与有限历史 fixture 的调度；仅在该测试中用 synctest 和查询 gate 确定事件顺序，保留全部原断言，不改生产恢复逻辑。该修正的独立 overlay 在 race 下覆盖 CPU 1/2/8 共 300 次，落入仓库后普通 300 次和 race 60 次通过。

最终独立验收通过：`go test -p 2 -json ./... -count=1` 与 `go test -race -p 2 -json ./... -count=1` 各 54 包、7263 测试事件通过，8 包无测试，无失败或测试跳过。`go vet ./...`、`go build ./...`、固定 lint2.15.0 的默认/anvil 全仓检查均通过，lint 两路各 0 issues；`go test -p 2 -tags anvil -run '^$' ./...` 完成 64 包编译检查，未运行本地链。独立生成 Go/audit 逐字节一致，9 份官方原件／原勘误／依赖身份保持，65 份本轮 Go/JSON 文件在验收前后内容不变，格式与 diff 检查通过。当前生成 41 objects、435 fields、12 unions、25 shims，audit 覆盖 32 个选择根及 772 条标量／对象政策路径，不能称为所有 schema 节点的完整索引。

首次汇报已完成，用户要求结束本次工作，将IPC提供及以下议题列入下一轮待执行规划；具体计划保存在现有module-simplification TMP README，本次不启动新能力实施。事实入口为 [兼容政策](go-ethereum-compatibility-policy.md) 与 [方法覆盖](method-transport-coverage.md)：

| 讨论方向 | 现行行为及需决定的事项 |
| --- | --- |
| IPC 与连接构造 | 当前 HTTP(S)/WS(S)，普通 Dial 要求至少两个不同端点，单源显式选择；下一轮计划提供 IPC，平台范围及其来源、订阅和生命周期语义需单独设计 |
| geth 类型适配 | common.Address/Hash 及业务对象在适配层转换；审查缺失/null/零值、数值范围、失败和调用方生态接口，不改标准生成模型来承接 |
| 对外行为 | policy+Next/Basis/Close 订阅、pending 支持范围、moving tag/reference、同步发送和错误 helper 均有独立合同；逐项决定保留、解释或提供适配 |

完整 Simulate 语义、F1、共享 Hub/持久化、真实 provider/Anvil 执行和发布证据继续独立。本次验收不将历史待办自动标为完成。

## 1. 当前基线

响应模型于2026-11-21完成验收：HTTP/WSS unary与rpcwire使用`rpc.Response{Exchange, Answer}`；方法拒绝保留原回答，Resolver强制返回逐源Outcome，Sources/Adjudication及canary/replay消费者已完整迁移。框架保存提交为`6fb6a2b`；同步写入方法级审查也已完成，ReceiptWait保留最后receipt尝试与最近观察，广播保持独立。

NewHead于2026-11-23完成实现与独立验收：单订阅Tip+DAG、连续原子路径、完整重组、冻结证人与审计，以及WSS有界通知和长期回收已接入。普通/race全仓测试、vet、build、tidy、生成资产一致性与格式检查通过。2026-11-24按用户授权在`audit/rpc-quality`逐文件分组保存：`efe95f6`为Sender/Header/Anchor补证与注释，`ee589ce`为NewHead、Logs、共享订阅基础及receipts局部修复；此前基线为`e7ac360`。正式文档随后的独立提交记录合同与接续点；没有推送。恢复工作先核对实际Git状态，不把已保存等同已发布。以下A-G/read成本与验证记录属于此前基线。

- 2026-11-20 已完成 A-G 与本轮 27 个 read 方法的代码验收，本轮父基线为 `943133e`。新会话先用 `git log`、`git status` 和未提交差异核对实际实现提交与工作树，不覆盖已有改动。此前 `8e0e77a` 的 AccessList 字段/生成类型复用和 codec 字符串优化保持。
- 项目当前固定Go **1.26.11**，由用户授权的安全补丁从1.26.10提升。Go1.27.3只做过隔离评估，生产JSON import、生成器及生成资产保持。**Go1.27暂不引入**，后续整体评估，收益不计入现有实现优化。
- 现行设计文档系列、公开`ethapi`类型迁移和首轮逐模块整理已完成。目标仍为固定go-ethereum v1.17.10的47项目标方法名称集合，已有27个注册读RPC以及独立写入、订阅和派生能力；这些数量不代表完整兼容。
- A-G 公共管线已验收：准备事实冻结、显式 reference evidence、来源槽位与传输所有权、Outcome 裁定、双 facade 交付/观测均已核对；frozen params transport 仍等待端到端瓶颈证据，exported ReferenceStage 的兼容风险留待发布说明。
- 两个交易查询、单收据与 BlockReceipts 的 strict canonical 路径已接入；有 canonical 消费能力的 Forwarded 也可复用。receipt 复用已校验 canonical 字节；AccessList 仍使用现有方法规则和生成模型，不以新增 processor 数量衡量完成度。
- FeeHistory 准入已复用完整解析模型的 count 事实，删除生成后丢弃的比较键和独立 range 重解析；精确小写 reward 绑定仍独立。各阶段没有通用跨调用模型。共享 Log 继续保留 Validate 与 EncodeOne 两段处理，仅去掉方法包装器重复的顶层数组解析。receipt raw 比较键去掉无业务变换的 JSON 往返；默认 canonical processor 不变。

### 已知成本与验证边界

固定Go1.26.10，本轮AccessList的64项对象编码约由1.160ms降到0.829ms，分配次数约3638降到2480；真实Aggregate的1/3/5来源大列表耗时约下降9%/12%/13%。这些是本地固定fixture的对照，不是生产延迟或CPU使用率。相对最初手写编码仍有成本差距，尚未接受所有性能回退。

变更相关全仓测试、race、vet、build、tidy、架构／方法互锁、生成新鲜度及字符串／AccessList各10万次定向race fuzz通过。65项差分矩阵的接受、输出字节和本矩阵错误文本与原基线一致；有限矩阵不等于完整输入域证明。

额外JSONsafe race fuzz完成100万次。额外wire normalizer race fuzz在最后报告654606次、运行超过12分钟后人工中止；未观察到反例，**不算通过，仍待独立诊断和完成**，不直接归因于环境或新代码。未进行本轮真实provider采集、Anvil重跑或正式发布。历史证据不代替未来目标提交的验收。

可复现入口在已跟踪源码：

- 对象基准：`internal/ethobject/accesslist/bench_test.go`；归因基准：同目录`cost_decomposition_bench_test.go`。
- 真实聚合基准：`internal/aggregator/accesslist_route_bench_test.go`，覆盖空／典型／64项和1/3/5来源；同一fixture保留冻结旧encoder控制组。
- 独立字节对照及fuzz：`internal/ethcodec/helpers_test.go`；条目接受域与顺序反例：`internal/ethobject/accesslist/acceptance_domain_test.go`。
- Log真实消费者反例：`internal/typed/log_alias_projection_test.go`、`internal/ethobject/receipt/log_alias_test.go`。

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

**新会话不再从 P1 重做。** 本表保留已验收的责任合同。需要修改时，先核对当前工作树和对应正式合同，再为具体方法或独立专题制定计划；响应模型及NewHead替换已经完成，其余独立专题仍按各自计划执行。

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

2026-11-20 用户确定继续考察上述27个读RPC之外的方法。这些入口的反馈、可交付信息、证据与锚点关系更复杂；截至2026-11-23，广播、同步写入与NewHead已分别验收，后续按下表的未完成入口独立规划。

1. 先结合 catalog、公开 facade 和 [覆盖清单](method-transport-coverage.md) 做源码事实盘点，区分独立写入及同步等待、订阅、派生与生命周期入口，以及显式 unsupported 能力。47 个上游公开方法与 27 个注册读 RPC 的口径不同，不能用相减确定剩余任务，也不能将 facade 名称不同自动当成新 RPC。
2. 为候选方法说明：反馈表示已接受、已观察、已确认还是结果未知；成功值、错误、诊断和来源材料分别包含哪些事实；请求／区块／交易／会话锚点从哪里建立、何时失效，以及等待、重组和恢复时是否仍成立。继续核对成员与门槛、原文与规范值、顺序与所有权、取消／关闭、重试或重放边界；由现行合同和反例支持结论，不把读方法的裁定与 reference 规则机械套用。
3. 选定一个方法后才制定独立计划，列出调用者、Interface、接受域、错误阶段、变更文件、反例、必要成本证据、完成门槛及未决政策。主控规划、编排和独立验收；当前用户授权最多2个执行者按互斥文件并行，主控维护文档。NewHead当时由单一执行者实施是历史事实，不限制后续授权。阶段计划仍写现有module-simplification README TMP，验收后迁移结论并清空；不新增阶段目录或总结文件。

写入的accepted／unknown／rejected与链上观察关系归 [方法合同](method-contracts.md) 和 [返回值合同](return-value-design.md)；NewHead连续路径、证人、审计、恢复与终止，以及Logs/Receipts完整证明和交付归 [订阅合同](subscription-contract.md)；锚点保证归 [reference合同](reference-freshness-policy.md)。现行保证与待裁决事项分别记录，unsupported不自动变为新增实现任务。F1／剩余S1、工具链、provider证据和发布缺口继续独立维护。

### 非 read 入口推进状态（2026-11-24）

主控负责计划、合同裁决与验收，执行 agent 使用用户授权的 `gpt-6.1-sol/high` 或 `gpt-6-astra/high`。同一时刻只有一个代码执行任务；后续模块可只读盘点。每项先在现有 README TMP 批准计划，执行者仅改代码/测试，主控核对后将长期结论迁回既有合同。

| 顺序 | 入口与验收责任 | 当前状态 |
| --- | --- | --- |
| 1 | SendRawTransaction、SendTransaction：广播反馈、签名/hash 绑定、来源拒绝材料、固定门槛 | 已验收；本轮统一Sources/Adjudication，区分支持不足、实际分歧、协议拒绝和hash不匹配，保留error.data presence |
| 2 | SendRawTransactionSync、SendTransactionSync：广播与收据观察的组合、超时/取消、证据与重组保证范围 | 已完成方法级验收；12项输入/反馈/证据/锚点/生命周期/预算矩阵核对通过，四入口及capture的26个定向顶层测试通过；保留现有实现。仅保留最后尝试与最近观察，不增加完整历史或链上/重组保证 |
| 3 | SubscribeNewHead：连续路径、冻结评估、hash恢复、完整重组、审计与回收 | 已验收；root/view使用policy与Next/Basis/Close，Q路径证人和对象供应分离，迟到观察进入后续评估；删除旧channel/gap/逐高度轮次出口。全仓普通/race、vet/build及生成资产检查通过，长期内存和解码成本证据见订阅合同 |
| 4 | TransactionSender：输入交易与block/index绑定、官方from声明和事实交付 | 已验收；生产实现保留，官方from异值与共同声明的HTTP/raw WSS及root/view补证完成；11项方法定向普通/race通过 |
| 5 | Header 视图、AnchorBlockHash/ForTag/Height：派生与锚点绑定、事实交付 | 均已验收；Header 5项定向普通/race及6项内核普通检查，Anchor 26项普通/25项独立race通过；运行实现保留，finalized preset的default/latest例外已明确 |
| 6 | SubscribeFilterLogs | 已验收（2026-11-24）；Head出生后的完整过滤连续流、policy+Next/Basis/Close、实时range/hash无历史补播；完整块证明、重组/零命中/字段/审计及资源均已核对；全仓普通/race各51包通过 |
| 7 | SubscribeTransactionReceipts | 已验收（2026-11-25）；完整块证明和固定Q官方字段互证，root/view统一policy+Next；共享内容owner及旧桥接删除完成，整仓普通/race各51包通过，静态和生成资产检查通过 |
| 8 | Close/Unsubscribe/取消及显式 unsupported：资源完成、错误与能力边界 | 已完成（2026-11-25）；来源准入/退休、唯一退订、生产耗尽及完成屏障收口；root/view三流关闭/能力/策略拒绝补证，冻结代码整仓普通/race各51包通过 |
| 9 | 全仓质量治理：静态检查扩围及整体执行流程审查 | 七切片及最小安全升级已验收；17规则默认/anvil全仓门禁接入本地/CI/release。最终普通/race各52包7096测试事件、vet/build/生成/平台覆盖及0可达漏洞通过；边界及上游fixture失败归发布合同 |

响应结果模型、同步写入方法级审查和NewHead分别完成，不能相互替代验收。NewHead的500高度完整Header测试交付598个delta，消费后稳定状态预算峰值39,669 B，Close后归零；3,000步合成tracker保留峰值7,259 B，300块恢复后图容量缩回16。这些是预算账本测量，不是Go heap/RSS硬上限。按hash请求与push的来源会话绑定不证明相同TCP连接；Gate只记录等待到期并重评估，RPC期限与重试另行配置；Client.Close触发异步Head清理，Head.Close提供完成屏障。完整使用约定、审计导出和成本限制由订阅合同拥有。

2026-11-24最新恢复点：Logs已完成本地方法验收，正式范围/交付见订阅合同第7节，验证历史见第9节；总规划和下一项保留在现有module-simplification README的TMP上方，Logs TMP已清理。最终全仓普通/race使用-p 2，各51包通过、9包无测试；vet/build和格式检查通过。此前NewHead fixture重复关闭及新增Logs WSS fixture缺字段/错时间戳均已修正，历史失败记录保留；首次并行整仓普通曾出现既有同步写入250ms观察断言失败，最终降低争用复验通过，该方法未改。没有新增provider/Anvil或发布证据。

接续SubscribeTransactionReceipts时，先读module-simplification/ethguard.md保存断点及订阅合同第8节。nil/未设置/空集合全量实时、首源候选而非最终锚、固定Q出生块及出生后完整性均已确认，不重问。2026-11-25用户确认根证明完整性、其余官方字段固定Q互证，不新增链分叉配置。保留既有过滤/生命周期修复和block_commitment内核，复用Head/Logs及规范值裁定，完成供应与独立提交/交付/资源；Logs通过不等于Receipt派生字段已证明。生命周期整体收敛设计保存在现有internal-subscription模块第15节，作为后续阶段评估输入。

2026-11-25接续：Receipts完整流及跨入口生命周期已通过本地验收，Go内容保存为f40a9e8；质量治理随后按数值边界、确定冗余、类型事实、错误/工具、资源/结构和全仓门禁推进。可读性与行为一起验收，保留有真实职责的直接实现、快照值复制及错误原因优先级；结构规则只用于定位，不以零告警或机械拆函数替代整体效果。新共享lint入口固定2.15.0，撤销整测试/工具路径豁免，默认/anvil与生成代码均纳入；G115和类型断言按范围/构造证明保留审查报告。实现基线全仓普通/race各52包7096测试事件通过，8包无测试，无失败/测试跳过。最终govulncheck发现3项可达告警后，用户明确授权Go1.26.11、OTel1.47.0同族及Gorilla修复伪版本的最小安全升级，新基线已重新独立普通/race、静态/生成及跨平台验收通过，漏洞扫描0可达/0导入包、7仅模块层报告；真实代理契约通过，上游两个旧/新版本均失败的夹具明确保留。TMP已清空。版本、报告及完成状态以[发布合同](release-contract.md)为准。Go1.27、F1、完整Simulate、共享Hub/single-flight、持久化、provider/Anvil实际执行及发布仍独立；用户随后授权按改动分组保存：数值/资源7908ab5、类型事实59fae6b、错误/测试d86b903、lint/CI bc1bf17、安全升级7493d0d，正式文档单独提交；未推送，TMP保持忽略。

以上是已有入口的合同与实现审查，不把`47−27`作为待办数。NewHead公开配置与事件接口已定；其余S1、F1默认值、真实provider、工具链和发布保持独立，无收益的实现允许有证据保留。

## 4. 阶段三：独立运行期专题与工具链评估

| 专题 | 当前事实与未决项 |
| --- | --- |
| F1 freshness | 已有请求scope和tip样本过期机制；默认lag、样本窗口／最低覆盖、豁免、结构化原因及finalized／单源组合未定，不能从零重新实施或自行选默认值 |
| S1订阅 | NewHead的路径/证据/策略/内存/终态已验收；Logs完整实时批次及终态/预算/方法生命周期已验收；receipts完整证明及固定Q字段连续流已验收。诊断及provider行为继续独立。共享Hub与持久化没有纳入本轮 |
| Go与JSON | Go1.27暂缓引入。已知保留旧JSON API也能受益于新内核，但B/op可能增加；直接使用json/v2默认语义会改变大小写匹配、重复键、UTF-8、空数组和omitempty，需独立验收 |
| 验证维护 | 手工feature／noCapture登记、cell到真实断言的追溯、canary阈值互锁、时间敏感测试及未完成wire大预算fuzz，分别诊断，不用自述表代替独立断言 |

这些专题保持现行行为，不成为阶段一等价整理的隐含改动；需要改变保证时由主控明确目标、影响和验收，再交执行代理实施。

## 5. 阶段四：外部证据与发布验收

补齐实际所需的交易类型生命周期、真实provider方言／状态／订阅观察、跨传输乱序／背压／重连／取消和材料证据。离线、Anvil与真实provider分别记账，既有捕获与日期化证据不改写成新观察。

对最终目标提交执行发布合同的完整门禁、兼容／API报告、构建与产物检查。内部优化、有限矩阵或一次全仓测试通过，均不能推出完整provider兼容或稳定发布已验收。没有明确授权不推送、发PR或发布。

## 6. 所有阶段共同保持的边界

- 官方字段／标量规则归已审计来源和生成层；方法关系及绑定归方法模块；coordinator不解释Ethereum schema。
- 保持冻结成员、分母、裁定门槛和代表源等业务保证；正常dissent不成为健康失败，每次真实尝试最终记账一次。结果模型专题可按明确的新合同替换错误分类及表示，其他专题不得借整理隐改错误语义。
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
