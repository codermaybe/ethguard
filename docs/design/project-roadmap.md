# 当前路线图与接手入口

更新：2026-11-14。本文是当前架构收敛目标、真实进度和接手顺序的唯一入口，不要求后来者拥有聊天记忆或临时目录。职责合同见末尾链接；不再新增施工计划，也不按旧阶段编号重新开工。

## 已确认的目标

- 官方业务类型统一以固定版本 execution-apis 原始数据及显式审核的标准勘误为源，由项目生成并公开使用；原始数据不改，生成工具使用通用 Jennifer，不写方法 AST 特判。不是仅为 SimulateV1 创建类型例外，也不继续把 geth struct 作为官方响应字段的事实源。
- 47 是业务方法及语义覆盖目标：锁定 go-ethereum v1.17.10 的 48 个导出 receiver 方法，排除底层 `Client()` 访问器。迁移后的公开签名使用项目生成类型，不再要求 geth Go 名义类型身份一致；这是明确的公共 API 迁移，不能只改测试名称或通过减少目标隐藏缺口。
- 前置生态兼容仅做审核过的完整直接 key 修正，随后检查适用的官方字段和方法语义，再聚合、比较、投票。不做万能 struct，不无限适配厂商字段，不引入通用嵌套路径修正规则系统。
- 官方受保护信息进入规范 Value／生成业务类型；域外材料通过 Extensions／Diagnostics 和有预算的原始证据交付。原文与规范结果不是同一材料，不能为使用一个返回结构而混入官方主值。
- root `Client` 与 `EthClient` view 共用一个执行 owner、参数冻结、方法计划、传输和生命周期。Strict、pending-local、Forwarded、写入、订阅继续遵守各自合同，类型统一不改变裁决语义。
- SimulateV1 应进入正常原生方法闭环。公共 `gethcompat` 可在其职责替换后删除；旧单源 `SimulateClient` 不算原生接入，也不产生项目的 quorum／CallResult 证据。

生成类型负责业务结构，不替代签名、哈希、区块体／uncle／withdrawal 承诺和 receipt 一致性检查。迁移不得删除这些检查来消除 geth 依赖。业务类型依赖与算法依赖分别登记；过渡中的私有 geth 类型桥必须标为未迁完，不能包装后宣称完成。生命周期接口、执行事实和标准库类型不属于 execution-apis JSON 业务结构。

## 当前交接边界

用户已确认暂时保留当前 `ethapi` 公开形态，本轮只核验并归档未提交改动，不继续调整公开范围或开始新的整体收敛。后续整体收敛与判断由用户接管；下文迁移目标和未闭合任务仍保留，不以本次提交宣称整体迁移完成。

本次归档：独立入口验证已提交为 `3e73faa1f0fed51ef7a1e2fe1540d6400396e217`，并在仅叠加这三个测试文件的 HEAD 隔离快照中通过定向 race。签名字节测试已精确比较完整 payload 和参数个数；其余相互依赖的生成／公开类型／标准勘误及合同一起归档。最终 743 个 Go／module／嵌入 spec 数据文件按下文相同算法的 SHA-256 为 `e8ce0faf8d1be165a59c47c303b38bbf4aba0cedccd42d8bc83e2206aab4cca9`；最终全库 race、vet、lint（零问题）、tidy、新鲜度与格式检查通过。正式发布的 Simulate 缺口及其他未闭合项保持列缺。

## 当前代码与待接管工作

独立核查基线为分支 `audit/rpc-quality`、HEAD `10c135d`。接手先读现场 `git status --short`、`git log -5 --oneline` 和实际 diff；不得回退到此基线、覆盖用户修改或把历史通过当作当前工作树通过。

基线时未提交的用户文档为 `docs/design/README.md`、`go-ethereum-compatibility-policy.md`、`method-contracts.md`、`wire-codegen.md`。保留已有有限兼容和数据交付内容，在其基础上合入新方向。`return-value-design.md` 的此前用户内容已经随历史提交入库，不在 dirty 列表不代表丢失。

前一执行 agent 留下 `internal/verification/integration/write_test.go`、`write_sync_test.go` 的修改和未跟踪的 `internal/verification/facade/subscription_entry_test.go`。本轮保留。订阅设施已修复通知过早清空／硬编码 ID，并新增 root/view newHeads 的真实 WSS 注册、同源 Pull、交付和退订验收；logs／receipts 与更广资源矩阵仍待完成。旧临时施工单和 `/tmp` 产物只作线索，本文件不依赖它们才能继续工作。

## 已核实事实与剩余出口

| 范围 | 已有事实 | 尚未闭合 |
| --- | --- | --- |
| Jennifer 历史 | `01be3dd` 于 2026-11-11 00:53 引入 Jennifer v1.7.1，真实用于生成 `types.gen.go`；`7618d29` 于 01:49 改成字符串 emitter，并删除依赖和文档说明 | 不是从未落实；现有提交说明未解释撤去 Jennifer 的理由，尚不能判定授权情况或归责于本轮 agent。恢复目标时保留该提交的 schema／精确数字等修复，禁止整体回滚 |
| 当前生成层 | 已恢复 Jennifer v1.7.1 的结构化输出，保留现有 schema／精确数字／nullable／union 语义；新增现场生成→编译→执行的验收 | 定义已实际迁入公开 ethapi，三个调用族输入及 FeeHistory／CreateAccessList／SyncProgress 结果已接入；没有整体请求生成；pending query 已迁独立 JSON Patch，生成器 AST 特判退出；生成、mask、字段 projection 共用有效 schema，整体公开类型迁移尚未完成 |
| 官方 Simulate 来源 | 已从固定官方 commit 原样重建 bundle，并永久纳入 91 个原始向量及摘要清单；62 个 result 验证声明字段完整，27 个顶层错误单独记账 | 2 个 result 的 VM 消息与官方 pattern 冲突，明确列缺；部分失败 call 的 logs／error.data 属 schema 未声明材料，原生执行还须验证附属及原始证据交付 |
| Jennifer 恢复批次核证（2026-11-14） | Jennifer 恢复批次（后续公开类型迁移的检查另见下文）：全库 normal／race、build、vet、tidy、新鲜度及 wire／spec 范围 lint 通过；下界检查定向 mutation 被现场生成行为测试抓住；Jennifer 不进入 root 运行时依赖 | 该历史批次全库 lint 曾报订阅草稿的 6 个未引用辅助声明；后续已通过真实 newHeads 入口测试消除，最终全库 lint 零问题，没有屏蔽或空测试 |
| 业务类型 | 公开定义、codec 和统一 schema 读取出口已经拆分。root/view 的 CallMsg、FeeHistory、SyncProgress 及 root AccessListResult 已迁生成模型 | 交易／receipt／block／log／selector／写入／订阅仍使用 geth 业务类型，输入地址和 hash 也尚未迁移；须逐族贯通，不另造手写 DTO |
| 方法集 | 默认 view 46／47，缺原生 SimulateV1；现有注册读 RPC 为 26 | 接入 simulate 后注册集合应自然扩展，不能为了保留旧 26 断言漏注册；47 项的新类型及语义合同分别验收 |
| transaction | 两个 RPC 已接 canonical 单次处理；result/error 配对、consumer 缺失、Candidate alias 等已有修复 | `10c135d` 将 raw 完整 Prepare 从每来源 3 次降为 2 次，canonical 仍 1 次；记录源码身份，hook 次数不能代替内核次数 |
| 性能 | `84304de` 对 `10c135d` 的同一真实 HTTP harness 已测两 RPC、3／5 源，1s×5；原始结果及 harness 哈希已核对 | by-hash／3 源时间约 −47.6%，in-block／5 源约 −61.6%，仅限已测 fixture。其他类型／大响应／峰值仍列缺；临时产物不是永久可重现证据 |
| block | 五个官方 optional non-nullable header 成员已有 null→omit 修复、golden 和守恒回归 | `baseFeePerGas`、`blockAccessListHash` 尚未同政策收口；`slotNumber` 不在锁定官方 schema，须另审官方投影和域外保留，不能混入官方白名单。baseFee 历史边界为 pre-London |
| 写入／订阅测试 | 新增五个 root SendTransaction／Sync 用例在基线独立运行通过 | 本次归档前已将签名字节断言收紧为完整 payload 与唯一参数比对；现有 m4 transport 回归保持强断言。root 测试不能关闭 view 缺口；newHeads 已补 root/view 入口和退订证据；logs／receipts 公开入口及三族更广的乱序／背压／取消证据仍待补 |
| 其他质量缺口 | 有 feeHistory 数组检查和 fuzz；一次 duration exit-1 后计数预算百万次及后续三次 60s 通过 | 原始判定行丢失，只能记疑似 Go 截止竞态；reward 行宽与请求 percentile 数、jsonsafe 合法正向边界、release 生成新鲜度门已加入，完整发布仍受 Simulate 缺口阻断；EIP-1898 非 map 形状先复现再定性 |

此前列出的 11 个缺直接入口证据为 PendingBalanceAt／PendingNonceAt／PendingStorageAt／PendingCodeAt、CallContractAtHash／PendingCallContract、EstimateGasAtBlock／EstimateGasAtBlockHash，以及三类订阅。接手须核对实际调用对象、参数化 helper 和独立预期，不能只按方法名文本搜索判定覆盖。其余已存在测试的方法也未因此自动完成逐变体深审；逐方法状态由正式覆盖清单维护。

## 本轮公开类型迁移与来源裁决

已实际完成：公开 `ethapi` 定义、内部 codec 拆分、三个 schema bundle 的统一读取、43 个官方请求描述及摘要来源清单；六个 CallContract／EstimateGas 入口使用生成 CallMsg；FeeHistory／CreateAccessList／SyncProgress 返回生成结果。完整方法签名合同分别检查已迁移的明确签名和其余旧签名，不减少 47 项业务目标。FeeHistory 的 blob 字段／精确数值、调用字段 presence 和同步原始计数都有独立断言。

这是破坏 Go 类型身份的迁移：CallMsg 的字段使用官方 hex／optional 表达，FeeHistory 不再用 float64 比率，AccessListResult 的 optional presence 不再被手写 DTO 消除，SyncProgress 仅包含三个官方 optional 字段。geth 额外同步／索引计数在附属及原始来源证据中交付；生成结构没有 Done()，不得把计数缺失解释成完成。详见 [生成合同](wire-codegen.md#已迁移的公开调用族)。

2026-11-14 已核对官方主干固定 commit `981aa66d79364be8322a9c358aaf0a571cca0873`：五项 Simulate 相关修复已合并，但 BlockStateCalls 缺 items 和 pending inclusion 不可 null 仍存在。因此“纯官方原样 schema、完整强类型业务语义、零本地勘误”目前不能同时满足；此前未见强阻断项的判断需要据此修正。用户随后明确接受带依据和退出条件的显式修正结构，取代零本地勘误目标。本批已将既有 pending 修正迁入 `internal/spec/errata/transaction-query-pending-context.json`，删除生成期 AST 特判，生成代码除勘误审计注释外逐字节一致。原始 bundle／向量不改，`ReadOriginal` 与 `Read` 分别提供原始／有效视图；共享有效视图服务生成、mask 和字段 projection。生产 pin 尚未升级，Simulate items 和 getLogs 空数组尚未添加勘误，不把结构落地当作三项全部解决。

当前公开迁移批次已运行全库 `go test ./... -count=1`、`go test ./... -race -count=1`、vet、build、tidy -diff、全库 lint（零问题）、格式及生成新鲜度检查；新增 newHeads 入口另以 race 重复 10 次通过。最后一次 syncing 准入变更有方法／facade／integration 定向普通测试及全库 race 覆盖。完整发布方法门 `ETHGUARD_REQUIRE_COMPLETE=1` 已独立运行，按真实现状失败，唯一缺少 SimulateV1；没有运行完整 release 流程、公网观察或 Anvil，不宣称业务迁移完成。

上一公开类型迁移批次源码身份：HEAD 为 `10c135d243573a9547ceb4c4523ed67a5439c52c`，修改均留在工作树。对排序后的 740 个现存 Go 文件、go.mod／go.sum 及 internal/spec 下非 Markdown 嵌入数据，逐项以 `path + NUL + SHA256(file)` 累计 SHA-256，当时为 `7329de23ae40774100d0d2f8946e6d7de59e110e8f20625c547f4389f832033c`；公开生成文件为 `12c86cf6ef0e3cba8a0112bc16250f4e4895bd33ade98c959afd0e6dc12cb1c4`。该标识用于区分现场源码，不替代实际测试或提交身份。

## 显式标准勘误的已确认边界

标准勘误修正固定 schema，前置生态兼容修正运行时同源响应；两者不可合并成通用 provider 字段路径规则。用户接受可外部审计的勘误结构，不等于接受 Gemini 建议中的全局 response pattern 放宽或无条件未知字段合入主值。非法受保护值仍不得投票，Extensions／Diagnostics／有预算的原始来源证据继续遵守既有合同。

每条 erratum 必须携带版本／commit、目标请求或结果、输入／输出摘要、理由、依据和退出条件；上游漂移、重复或无实际改动均失败，必须审核删除／调整，不自动跳过。当前只迁移既有 pending 债务，生效范围及行为保持不变；上游已修的五项不在 beta.7 另加本地补丁。

与原执行预期的关系：本次仅把“零本地勘误”修订为“有依据、可审计、可退出的显式标准勘误”。固定官方来源、通用 Jennifer、同一有效 schema、前置兼容后严格检查、受保护主值与附属／原始证据分通道、单执行 owner 和 47 项完整迁移目标保持原定方向。本批符合这一修订后的架构预期；公开类型全迁移、上游基线升级、原生 Simulate 与删除 gethcompat 仍按下表验收，不能将勘误层完成视作整体迁移完成。

推进顺序更新为：升级官方固定基线并重建来源及向量 → 审核已有 pending 勘误的退出／续用 → 剩余 Simulate items、getLogs 空数组分别落实独立勘误 → 补齐通用 enum／not／patternProperties／数组约束能力及业务闭环。时间默认与递增行为留在方法／客户端语义，不塞入 schema。原生 Simulate 和 gethcompat 删除仍待完成，47 项目标不变。

本批已通过全库普通／race、vet、build、tidy -diff、lint（零问题）、格式／文档互锁和生成新鲜度；末次空勘误库存与审计校验调整另做 executionapi／generator 定向 race。两个隔离副本的定向 mutation（移除 commit 绑定、提前部分提交）均由对应独立断言抓住。原始三个 bundle 摘要不变，重新生成的业务代码除审计头注释外逐字节一致。未执行完整发布流程或公网／Anvil 观察，原生 Simulate 与整体业务迁移仍未完成。

上述勘误迁移批次提交前核证的源码基于 HEAD `10c135d243573a9547ceb4c4523ed67a5439c52c` 的工作树。按上一批算法，并额外纳入实际嵌入的 errata/README.md，对 743 个 Go／module／spec 数据文件累计 SHA-256 为 `8352c05d1c0d688e7e56714fc9f49c555b1b3bf76f128c7f1c5d03df7f4bbdf1`；公开生成文件 SHA-256 为 `1eb664c44e6e360883e11f9ec73331bde2ee9cea585a630235bc2470efdb2d4d`。勘误文件与实际输入／输出摘要可由 `go test ./internal/executionapi -run '^TestErrata' -v` 独立列出，不以这些摘要替代行为验证。

## 当前推进顺序

| 业务工作 | 下一步 | 验收出口 |
| --- | --- | --- |
| 公开生成类型与单源 schema | Jennifer、公开 ethapi、共享 executionapi 和显式 errata 已落地；43 方法请求清单当前消费 CallMsg；先升级官方基线和审计勘误退出，再处理剩余 schema 冲突及请求模型 | 外部调用者可使用生成类型，生成可重复，presence／union／精确数字和接受域不回退；不复制另一套模型，不把整个内部 wire 包公开 |
| 原生 SimulateV1 纵切 | 统一生成类型→参数冻结→方法注册→reference→多源处理→规范结果→root/view 与证据 | 真实 HTTP／raw WSS 下 root、view、raw Call 全路径；成功、内层失败、错误、冲突、取消和 Close 的独立见证通过 |
| 其余业务类型迁移 | 按简单量／DATA、call／费用、transaction／receipt／block、写入／订阅逐族替换 geth 输入、结果、selector 等业务类型 | 47 项逐入口记录本地生成签名、官方语义和错误／证据；旧签名检查被新类型合同替换，不是放宽为只检查方法名 |
| 删除兼容入口 | 原生 simulate 可用后迁 canary、anchor fleet、CI、选项诊断及文档，再删公共 `gethcompat`；内部 geth adapter 的生产调用与差分 oracle 分开归位 | 没有悬空入口、错误指引或绕过原生执行的退路；历史 geth 证据不重标 native；公共 API 有明确迁移说明 |
| 局部正确性与既有 WIP | 配合新类型逐族审查旧测试，修精确断言；block 两字段／slotNumber 和 feeHistory 请求关联保持明确待办 | 独立正反例；不为旧 geth 公开签名另起完整测试工程 |
| 最终质量收口 | 补逐方法、订阅同源 Pull／generation／资源、生成新鲜度及发布完整性证据 | 全局门禁针对最终源码运行；未测的公网观察、漏洞扫描或性能组合继续列缺，不能宣称全部完成 |

这不是要求每族重写。局部正确性修复可穿插；Receipt Candidate 等进一步性能改造排在类型主线和正确性之后，先完成自己的等价电池再接 seam。

## SimulateV1 可复用的实现与不可混淆的语义

结果模型已有公开 `ethapi.NewSimulateV1Result`；生成器、`rpc/internal/wire/prune.go` 和 `rpc/projection/official.go` 已通过同一个 `internal/executionapi.Read` 读取三个固定 bundle，Simulate 已进入运行时字段处理；`rpc/methods/spec_inventory.go`／catalog 仍未注册。当前 `Client.Call` 会本地拒绝该 RPC，不能把构造器存在记成已经接通。

官方业务模型已从内部 wire 实际迁到公开 ethapi 包，共享编码原语位于 internal/ethcodec；内部保留 normalization／mask／admission 职责。root 返回该生成结果加 CallResult，view 返回同一结果加 error，共享一个执行函数；不再手写 geth SimulateOptions／Result 镜像，不以 internal 类型 alias 作为长期公开出口。

执行可复用 Strict、canonical-bytes 和既有 reference／pending 机制。第二参数是 number／tag／裸 hash，不能套 EIP-1898 对象；现有 `ReferenceShapeHashAt` 可写裸 hash。省略／nil base 的默认化必须在建立 reference 前明确完成。pending-local、Forwarded 保留自己的语义，不能只实现 canonical processor 路径。

内层 calls 的 status=0／error 是正常模拟结果，不是顶层 eth_call revert。`[]` 与 `null` 不等价，VM error 的 `{}` 是已有合法分支。模拟块、交易和日志是合成结果，不验证其真实链归属；number gap 可插入空块，不能强行要求结果块数等于请求块数。现有模拟日志正例只含 transactionHash，不能因 geth Log decoder 更严而拒绝官方允许值。

固定请求 bundle 有待解释的缺陷／歧义：blockStateCalls 为 array 却没有 items、blobVersionedHashes 的 string 声明、state/stateDiff union 与 balance-only 的关系、time 描述与锁定 geth 实现的差异。原始 bundle 不改；迁移已改为原始来源＋显式 Spec Errata，优先升级可固定的官方修复，禁止生成器 AST 特判或静默放宽约束。pending query 已迁为独立勘误；Simulate 请求及 getLogs 空结果仍按顺序处理。官方 beta.7 实有 91 个 Simulate `.io`，现已固定入库并区分 62 个成功 result、27 个顶层错误和 2 个 schema／向量冲突；来源与接受域见 [wire-codegen.md](wire-codegen.md#simulatev1-官方来源向量与待解决冲突)。旧 adapter 简化 fixture 不替代这些官方来源。

## gethcompat 删除的实际影响

该公共包同时拥有 geth 传输的 `Dialer/New` 和独立单源 SimulateClient。两项职责均可被原生路径替代，没有必须保留它才能实现 SimulateV1 的技术要求；但删除前须处理：

- `internal/livecanary/run.go`、live-canary 工作流默认 adapter、recheck 对历史 manifest 的 geth 回退；新执行可迁 native，旧证据保持原身份，无法原样重跑时明确说明。
- `internal/verification/integration/anchor_transport_test.go` 的默认 geth fleet；迁原生 WSS 须显式设置 TransportWSS，不能只替换构造函数。
- client/runtime 的 HTTPAdapterGeth、WithHTTPAdapter、DialerOption 与错误提示；区分过期通道选项和仍有实际用途的通用注入接口，不留指向已删除包的诊断。
- 架构 interlock、CI、README、审查配置和现行合同；新门证明原生执行与生成业务类型，不是直接删掉旧失败断言。历史 ADR 只记取代关系，不改写旧决定。
- release 脚本／workflow 中“预发布必须恰好缺 SimulateV1”的临时模式；补齐后须恢复零业务缺口，避免方法已实现反而预发布失败。

内部 `internal/transport/geth` 另供 nativesmoke、pool 和 HTTP 差分测试使用。删除公共包不等于同步抹掉这些 oracle；可隔离保留测试用途，剩余生产用途必须迁走。去业务类型依赖不自动等于去掉所有 go-ethereum 算法依赖，也不授权重写密码学原语或削弱承诺校验。

## 接手与自检规则

主控负责公开接口、schema／保护域、跨模块整合与最终核证；最多同时两个辅助 agent，复杂分析用用户指定的 gpt-6.1-sol/high，边界清楚的重复工作可用 terra。辅助不再派生子 agent，共享文件只有一个修改 owner。

每个小批次说明独立预期，完成实现、针对性验证和自检后更新本文与正式方法证据。文件／测试／提交按业务命名，不带任务编号。正例必须成功；负例检查完整错误和来源事实；关键防线用定向 mutation 检验，不接受双方失败、err 非 nil、字符串片段或测试数量替代性质证明。

最终依据发布合同核对 `go test ./... -count=1`／`go test ./... -race -count=1`／vet／lint／架构／生成新鲜度和 release 检查，必要的 Anvil 与 integration shuffle 分别取证。新改动、失败或未关闭问题才扩大验证，不反复重跑历史阶段。网络不可用的漏洞扫描、未做公网观察及资源矩阵保持列缺。每次交付报告实际源码身份、改动、运行验证和剩余项。

## 正式合同与证据 owner

- 业务目标与公开类型声明：[go-ethereum-compatibility-policy.md](go-ethereum-compatibility-policy.md)；逐方法／传输／证据：[method-transport-coverage.md](method-transport-coverage.md)。
- 执行与依赖归属：[codebase-topology.md](codebase-topology.md)；方法前置兼容／官方检查：[method-contracts.md](method-contracts.md)；数据通道：[return-value-design.md](return-value-design.md)。
- 生成模型与来源：[wire-codegen.md](wire-codegen.md)；reference／pending：[moving-tag-matrix.md](moving-tag-matrix.md)；执行 seam：[resolver-seam-contract.md](resolver-seam-contract.md)。
- 验证义务：[method-verification-pipeline.md](method-verification-pipeline.md)；真实观察：[external-rpc-test-framework.md](external-rpc-test-framework.md)；门禁：[release-contract.md](release-contract.md)。

freshness 的样本年龄／请求范围／默认策略、跨传输完整证据和真实历史 fork／moving-tag 观察继续保留。SyncProgress 的三个官方字段及附属／原始计数证据不替代公网对象样本；生成类型不提供 geth Done()。Corroborated 始终只是 provider-backed cross-source evidence（多源 provider 证据），不是 Ethereum consensus 或 cryptographic finality。
