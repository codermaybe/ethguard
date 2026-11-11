# 外部 RPC 与长连接测试框架设计

状态：**持续测试设计；transaction 兼容方案已确认，可开始实施**（2026-11-11）。现有 rpcsample、livecanary、HTTP/WSS 测试设施可复用；当前工作树已有 transaction 采集和 Anvil 相关入口，存在代码不等于已执行通过，不据旧规划重复建设。本文拥有测试入口、证据分层及实施依赖；规则边界由 [方法级 key 兼容设计](transaction-semantic-matching.md) 维护。现行验收约束仍见 [验证管线](method-verification-pipeline.md)、[capture/golden](capture-golden-contract.md) 和 [发布合同](release-contract.md)。

## 证据分层

| 层 | 回答的问题 | 不能代替 |
| --- | --- | --- |
| Contract／本地协议故障 | 在明确输入或故障下，公开客户端如何校验、裁决及交付证据？ | 真实 provider 是否返回过该形态 |
| Anvil 实际进程 | 给定受控链状态，pending／mined／回滚等请求关系是否正确？ | 不同 provider 的 wire 方言、扩展或代理行为 |
| 真实 provider 定点采集 | 固定对象在本次来源与时刻实际返回什么？ | 执行正确性、永久兼容或链共识证明 |
| 离线回放 | 已归档输入经过当前生产链是否仍满足合同？ | 新的外部观察或真实状态转换 |

这些是不同证明能力，不是必须依次完成的等级。真实采集与最小 Anvil 可以在共享场景合同确定后分别推进；每条归一规则只等待其实际需要的证据，不等待全部层和交易类型齐全。

外部证据必须具有真实采集来源与可追溯记录，不能由本地手写 JSON、合成 fault、Anvil 输出或离线重放的执行结果冒充。真实 capture 可以离线重放，但外部来源资格来自原始采集及 provenance，不来自本地测试通过；manifest／digest 用于追溯和完整性核对，不能单独证明真实来源。由真实响应变换得到的故障数据仍须标记为派生／注入场景，不得回写成 provider 实测记录。未实际观察到的形态继续保留证据缺口。

## 现有 seam 与候选工具

| 能力 | 复用／候选方案 | 尚需决定 |
| --- | --- | --- |
| HTTP 对端／响应变换 | testing、httptest、net/http、RoundTripper；真实代理拓扑才用 ReverseProxy | 故障装饰器的最小形状与隔离，不另造一套客户端 |
| WSS Upgrade／帧／Close | 复用 internal/testkit/wstest，直接发送原始 payload | 生命周期与帧故障不要混成不透明反向代理 |
| 写阻塞／取消 | seam 已接受 net.Conn 时可评估 net.Pipe | Pipe 不提供 TCP half-close；不能为测试扩生产 Transport Port |
| 固定 EVM 状态 | 新增最小 Anvil 专项，固定版本、hardfork、genesis 与本地测试账户 | 启动／就绪／清理、真实状态场景与显式执行入口尚待实施；fork 另列在线专项 |
| provider 行为 | 定点形态优先复用 rpcsample；livecanary 保持其固定 plan／报告职责 | 补本轮显式采集计划、批次元数据及可消费入口；不将固定 canary 当任意 transaction 采集器 |

没有明确缺口，不引入 go-vcr、Toxiproxy、Hardhat、额外 Geth server、断言／属性／泄漏框架。第三方工具带来的进程、网络和运行时成本须单独评估；go-ethereum 仍是版本化类型／行为对照，不代表所有 provider。

## Anvil 最小专项

**先无 fork 的确定性状态场景，不先做完整 fork 平台。** 它只负责当前 transaction 切片需要的状态语义；真实历史 fork 仍保留独立完成出口，不把本地链产物叫作真实历史观察。

- 工具：固定 Anvil 版本和二进制摘要；固定 hardfork、chain ID、genesis 时间、gas 环境与仅限本地的测试账户。禁止默认 `latest` hardfork 随工具升级漂移。
- 生命周期：仅监听 loopback；使用 OS 分配端口，避免固定端口冲突。启动后在有界期限内核对 chain ID、已挖区块与账户状态，不只探测 TCP；日志按预算保留。正常、断言失败、取消和启动失败都关闭进程并等待退出，必要时有界强制终止。
- 最小场景：关闭自动挖矿，snapshot；用本地测试密钥签一笔交易并只发送到该进程；检查 pending 回答及未 mined receipt，手动挖块后验证 by-hash 与 by-block/index 指向同一交易、receipt／block 上下文一致；revert 后直接查询证明余额、nonce、区块及交易可见性恢复。Anvil 的 mempool／receipt 还原行为必须实测，不以 revert 返回 true 代替恢复证明；若不完整，重启可重建 genesis 保证场景隔离并明确该限制。
- 历史关系：交易 mined 后将其 block hash 冻结，再推进链头；查询该已封存本地区块仍返回同一对象。它证明固定历史对象与后续状态的关系，不证明真实主网历史数据。
- 消费入口：沿公开 Root／EthClient 的 native HTTP 和 raw WSS 读取同一实例，使用生产 Admit、Encode、Decision 与 typed。不复制 decoder，不修改生产 API 方便测试。第一条交易先覆盖链上身份及状态关系；以后按对象切片增加类型，不一开始穷举全部交易与 fork。
- 多源边界：一个 Anvil 或同一实例的多个连接不是多个独立 provider。不能将其组成“真实多源佐证”；门槛与异见仍由本地可控协议对端验证，并标记为合成场景。

### Fork 与离线执行不是同一件事

- fork 专项用于确实依赖真实历史状态、moving tags 或 EIP-1898 的后续问题：先明确请求适用范围，再固定远端 chain ID、block number **及核对其 hash**、来源代号、版本、启动参数摘要和状态／缓存摘要。仅对远端只读，本地变更隔离恢复。
- `--dump-state`／`--load-state` 不自动提供完整远端历史，fork cache 也可能按需联网。本机 Anvil 帮助明确说明 offline-start 缺缓存仍会取远端；离线资格必须在阻断公网访问后重新执行所需请求证明，不能凭有 dump 文件认定。
- 需要 fork 的场景显式列出网络、凭证、请求预算和截止时间；缺凭证或缺归档状态保留未执行，不伪造 passed，也不阻塞无关的官方 key 收敛。交易本地生命周期不以 fork 连接可用为前置条件。
- 普通测试保留离线回放；显式 Anvil 专项必须真正启动进程。录制 JSON 不能替代状态转换验证；Anvil 输出不能放入 provider golden 充当真实观察。现有 mainnet-only provider replay 也不能直接承载本地 chain ID，第一切片通过公开 integration 入口消费本地进程，不扩造第二份 golden 体系。

## 故障场景与断言

每个场景明确 Name、Transport、Source、Method、Request、Mutation、Expected。名称表达业务故障，不按阶段编号；变换不修改全局状态或依赖执行顺序，scenario 标识不污染业务 Decision。

| 层 | 有价值的候选覆盖 |
| --- | --- |
| HTTP | 429、错误 data 的 missing/null/empty/value 四态、非法／截断 JSON、延迟／取消、中途 EOF、vendor 字段 |
| WSS 连接／帧 | Upgrade 拒绝、异常 EOF、实际半关闭设施、Close code／重复 Close、资源清理、分片与控制帧、非法 opcode、超长／截断 payload、写阻塞 |
| WSS payload | 原始 missing/null/empty/value 与 provider envelope，通过同一生产准入／裁决入口 |

断言同时检查协议分类、业务 status／固定分母／Value／typed 及 fault／reference／presence／来源证据，不只检查最终值。并发时序优先使用现有确定性工具；不把真实 sleep 时长当正确性证明。

## 实施条件与边界

1. 按下节先交付最小场景和真实采集入口；Anvil 生命周期、显式执行与异常装饰器逐项实现。不能将下面的目标路径或命令当作现有 TestAnvil／workflow 能力。
2. 第一切片完成 transaction 的固定本地历史对象、pending → mined 和可恢复状态隔离；真实历史 fork、moving tag、EIP-1898 及完整 HTTP/WSS 故障矩阵后续各按真实依赖实施，不成为所有归一规则的全局前置门。
3. 不引入第二套 WSS server、typed decoder 或执行链，不为测试增加生产专用接口。
4. 所有公网访问显式声明凭证、预算、超时、结果状态与重跑规则；普通 PR 不隐式联网。
5. WSS 四态的合成注入不能关闭真实 null／provider golden 证据缺口；尚无 not_observed 顶层状态时，按现有 schema 使用 inconclusive 加 state_reason。
6. fuzz／race／专项命令从发布合同和实际入口读取；本提案不再另定与当前计数型 fuzz 门冲突的“短时间预算”规则。

工具归属见 [tooling.md](tooling.md)，WSS 原始保真见 [wss-wire-contract.md](wss-wire-contract.md)，provider 证据规则见 [live-canary-contract.md](live-canary-contract.md)。未裁决的标准化候选不属于本契约；本文规划本身不授权新增 retry、后台重连、自动 golden 更新或生产行为变化。

## 当前执行路线：先最小入口，双线取证，逐 key 收敛

### 已核对的起点与职责

2026-11-11 读取到：`cmd/rpcsample` 接受显式 source／request 计划，输出 HTTP `index.json` 和原始 records；**它不自动复制计划、生成 batch.json／expect.json 或判定兼容性**。`cmd/shapeprobe` 当前是固定 mainnet 形态探测，不是通用 transaction 计划入口。`livecanary` 有固定 plan/hash 与 report/verify 契约，不能静默加任意任务。本机可执行 `anvil --version`，输出 1.7.1、commit `4072e48705af9d93e3c0f6e29e93b5e9a40caed8`；这只是工具可用性，不是状态场景通过，也不代表仓库已经固定该版本。

| 顺序 | 可执行交付 | 开始依赖与完成出口 |
| --- | --- | --- |
| A．冻结最小场景与运行入口 | 第一对象仍为独立 transaction；明确官方／附属边界、本地交易设置、provider 来源集合及真实历史对象选择程序，登记网络、方法、hardfork、预期状态与凭证输入 | 不依赖 samples，也不要求先联网才能开始 C；官方字段事实源为 pinned execution-apis；实际历史 tx 身份在 B 的发现请求中冻结，旧样本仅作历史回归 |
| B．真实 provider 定点采集 | 用现有 rpcsample 计划采同一历史交易的 by-hash、by-block/index 及必要 block/receipt/clientVersion/chainId；保留成功和失败来源 | A 后即可执行，不等 Anvil；先在线发现并冻结 tx hash、block hash/index，再对事先选定的多个来源执行同一计划，保留发现记录；核对网络和对象身份，完成脱敏、摘要、批次封装与字段归类，不由当前 encoder 生成期望 |
| C．最小 Anvil 状态验证 | 小型启动器＋一个 transaction 生命周期场景，公开 HTTP/raw WSS 读取，有界资源与失败清理 | A 后独立执行，不等 B；实际证明 pending → mined、固定已封存区块查询及恢复隔离，记录原始回答与当前生产入口的成功／失败基线；不要求先修好归一才能交付状态基线，不声称覆盖全部 fork 或 provider 方言 |
| D．规则裁决及单切片实施 | 依据 B 的实际形态与协议定义，在对应方法文件按官方 key 静态集中实现；涉及状态关系的规则利用 C 的相关基线 | 每条规则给出完整 key 的等价正例、不可合并反例与拒绝边界；不进入 key 内部局部表示，不建 MethodPolicy／实体框架。兼容不了就拒绝，不为通过率填零、忽略字段或混源；外部 key 继续既有附属流程 |
| E．公开端到端验收及证据冻结 | D 后证明安全解析 → 方法级 key 修正 → 官方与方法校验 → 逐源 EncodeCheck／比较 → Decision → typed 及既有附属交付；审核新输入并接回放 | 原始形态按审核后的输入接受域检查，规范输出经独立官方 oracle 验证，不要求兼容原文也满足严格输出拼写；生成链差异单列。验证明确合同违规的来源归因及健康调度隔离，一次尝试只最终记账一次、固定成员与分母不变；HTTP golden 与 raw WSS 证据分列 |
| F．下一对象及后续专项 | 独立 transaction 闭环后接 block 内嵌 transaction；再按请求范围接 fork/reference/moving-tag、订阅故障等 | 不将完整框架、所有交易类型或全方法面完成作为 D 的起点；整体兼容／发布仍须满足现有完整门禁，不能把一个已闭合切片叫 complete |

依赖是 A → B、A → C；B → provider 兼容规则的 D，C 的相关状态基线 → 状态规则的 D/E；D → E → 下一对象。C 暴露生产路径缺陷时保留可复现失败基线，修复后的通过属于 E，避免“先归一成功才能建测试”的循环依赖。纯官方 required 修复可根据规范先裁决和实现，不必等待真实坏 provider；外部兼容声明仍需相应新观察。不是“框架全建完 → 攒齐所有证据 → 开始修改”。

### 下一轮实施边界与落点

以 **A、B、C 的 transaction 最小场景与已有入口**为起点，按已确认设计直接推进可执行的 D／E，不等待另一轮全局架构评审。B/C 的证据来源和结论独立；来源尚不可用只阻塞依赖该观察的兼容规则，不阻塞官方 required／nullable、保真路径和拒绝行为修复。没有实际形态差异不造规则，不能用 Anvil 填平 provider 缺口。不附带全方法采集器、完整 fork 缓存平台、WSS 代理或重试／重连改造。

- 采集代码 owner 保持 `cmd/rpcsample`／`internal/rpcsample`；先提交可审阅的无凭证场景计划，再仅补本轮确实缺少的批次包装／入口。保存计划、提交标识、spec 版本、脱敏采集参数、record/index 摘要和 batch 元数据；审核后生成 expect，禁止当前实现自动更新期望自证。
- 初轮跨来源形态比较至少需要两个不同 provider 来源的同对象回答；更多来源按实际差异缺口补，不为凑数量全网扫描。端点不同、clientVersion 不同不证明底层独立，报告只声明可核实的来源身份；生产 quorum 与“至少两个观察来源”是不同要求。采集来源事先冻结，失败不静默换源或从报告消失。
- Anvil owner 为 `internal/testkit/anvil` 的进程生命周期 helper 与 `internal/verification/anvil` 的独立场景；当前工作树已有相关实现，优先核对和复用，不再按“尚未创建”的旧规划重建。不做通用 runner 或另一套断言 DSL；普通 JSON-RPC 控制请求只在测试侧使用。
- 入口区分默认离线测试、显式本地 Anvil 专项、显式在线 provider capture；实现时给出真实可运行命令。Anvil 专项可用 `anvil` build tag 隔离，主动选择后缺二进制／版本不符即失败，不通过静默 skip 宣称通过。安装过程固定版本与校验，PR 不运行隐式 provider 请求；在线 capture 用显式手动任务，扩展既有工具而非改 canary 固定 plan。
- 运行产物先作为可定位的 run artifact 交付；审核后的最小原始输入才归档到适当 replay 入口。这样 repo samples 是本轮执行的派生回归资产，不是资格前提。现有 samples 不删除，也不据其数量判断进度。

### 验收与停止条件

一次切片分别报告四项：官方合同裁决、完整公开行为、实际执行证据、未覆盖边界。B 的凭证／权限不足、缺目标交易类型或 C 的工具／状态不支持都保留具体原因，不将只成功返回的来源重新组成更小分母。来源数用于观察覆盖，实际聚合仍使用调用入口冻结的成员，不把采集成功数冒充 N。

Anvil 必须证明真实状态变化和失败清理；provider 必须有本次实际请求与可追溯原始记录；回放必须消费记录驱动生产链而非注入计算好的 Decision。三者单列，不用“framework passed”合并。公开端点可用于显式无凭证采集；若所选来源需要凭证而无法获取，则报告未执行，不能宣称已观察。缺一类证据只阻塞依赖它的规则或完成声明，不阻塞其他可执行切片。

本轮已实际执行固定 Anvil 1.7.1 的无 fork transaction 生命周期，单个本地来源经 HTTP／raw WSS、Root／EthClient 共 88 条消费断言通过，并核对恢复后状态及重启隔离；记录位于 `/tmp/ethguard-anvil-controller-03/transaction-lifecycle.json`。审阅后的最小 pending 原始样例及摘要已接入方法测试。它不构成外部 provider 证明；本轮未执行新公网采集。当前实施检查点按用户要求收束，后续执行由审阅结果决定，详见 [主设计](transaction-semantic-matching.md#12-当前实施检查点)。
