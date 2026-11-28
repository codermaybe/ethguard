# 订阅合同

状态：2026-11-28。NewHead 使用单订阅 Tip+DAG、连续路径与审计模型；Logs 与 Receipts 保留原生连续流。S6 默认策略、显式预算移交、独立终态观察及外围 channel 桥已通过本地验收与最终全仓普通／race。Windows 真实 pipe 运行门禁仍待 CI。WSS 字节和连接生命周期见 [WSS wire 合同](wss-wire-contract.md)，真实 provider 证据见 [WSS 观察记录](wss-provider-evidence.md)。

## 1. 公开接口与来源

Root Client 与 EthClient view 共用以下 NewHead 接口：

~~~go
SubscribeNewHead(context.Context, HeadSubscriptionPolicy) (*HeadSubscription, error)
(*HeadSubscription).Next(context.Context) (HeadEvent, error)
(*HeadSubscription).Basis() HeadEvidenceBasis
(*HeadSubscription).Close() error
~~~

不保留旧 NewHead channel、RoundTimeout、PathBytes、逐高度 gap 错误或 V2 出口。Open 冻结成员、校验策略并预留控制空间；首次网络建流和初始锚点建立属于流内阶段。建立 context 在返回前检查，返回后的订阅生命周期由 Client owner、订阅 Close 和 Next context 控制。当前从现场观察建立初始锚点，不提供历史起点、持久化或跨进程恢复。

成员来自构造期声明支持有界订阅的固定来源快照；pool 不按 URL scheme 推断能力，当前支持 WSS 与 IPC；Unix IPC 已实测，Windows pipe 运行门禁仍待 CI。HTTP 能力不产生 push 来源，也不能借其他 endpoint 补票。strict 使用固定多数 Q，并至少要求两个成员；multi-source Client 只有一个可订阅来源时拒绝建立。单源 Client 使用 Forwarded 保证，不升级为 Corroborated；没有公开独立 Selected 订阅模式。

Client关闭后，root/view的三个订阅入口均在网络前返回ErrPoolClosed，不创建流句柄。非法查询/策略保留各入口既有的检查顺序；HTTP-only及strict仅一个可订阅来源的能力拒绝也在本地完成。

Basis 返回方法域、固定成员、Q 和证据等级的副本。DomainID 以 newHeads/<进程内递增 ID> 区分本进程的订阅实例，不是跨进程或重启后的全局唯一 ID。不同订阅有独立账本、版本、缓存、预算和终态。

Logs 的 root/view 入口已统一迁移并完成本地方法验收：

~~~go
SubscribeFilterLogs(context.Context, ethapi.FilterQuery, LogsSubscriptionPolicy) (*LogsSubscription, error)
(*LogsSubscription).Next(context.Context) (LogsEvent, error)
(*LogsSubscription).Basis() HeadEvidenceBasis
(*LogsSubscription).CommittedTip() (AnchorNode, uint64)
(*LogsSubscription).Close() error
~~~

不保留旧 chan Log 或第二个版本。Logs 的 Basis 表示其私有 Head 的固定路径成员和 Q，不能解释成日志载荷已被 Q 源逐字段投票。CommittedTip 是 Logs 已原子入队的提交位置，不是调用方已处理或持久化的位置。路径、内容与资源合同见第 7 节。

Receipts 的 root/view 使用同一完整连续流接口：

~~~go
SubscribeTransactionReceipts(context.Context, *ethapi.TransactionReceiptsQuery, ReceiptsSubscriptionPolicy) (*ReceiptsSubscription, error)
(*ReceiptsSubscription).Next(context.Context) (ReceiptsEvent, error)
(*ReceiptsSubscription).Basis() HeadEvidenceBasis
(*ReceiptsSubscription).CommittedTip() (AnchorNode, uint64)
(*ReceiptsSubscription).Close() error
~~~

原生入口保持 Next 连续流；外围 `ethadapter` 的 geth channel 桥另见[兼容政策](go-ethereum-compatibility-policy.md)。Basis 表示私有 Head 的固定路径成员和 Q；每块收据的字段支持另由 ReceiptSupport 表达。CommittedTip 是内容完整且已原子入队的位置，不是业务 Ack。范围、字段和资源合同见第 8 节，实际验收状态见第 9 节。

三类原生流还提供 `Terminated() <-chan struct{}`、`TerminalError() error` 和 `NextWithBudget(ctx, destination)`。前两项观察 producer 终态；后一项供需要独立内存账本的消费者原子接收完整事件，并返回幂等 release。普通调用方继续使用 Next 即可，geth 调用方由适配器自动使用这些边界。

## 2. NewHead 的支持与连续路径

每个来源只有一个有效当前 Tip。来源订阅 SourceSession、会话内 ReadSequence、ObservationRevision、EvaluationRevision、PathVersion 与对象 RequestID 分别表达不同身份。旧会话、旧读取序号和旧请求结果不能覆写新状态；断连先撤销支持，再执行可能等待网络的退订。故障、超时与重连不缩成员或 Q。

共享 Header 内核验证声明 hash 与重算 hash；DAG 只沿 parentHash 匹配且高度相差 1 的父边传播支持。同一来源沿祖先路径至多一票。未知父边不推断支持；先到的非法 child 不能反向否定后到的合法 parent。重复 push 不增加票数，可以刷新该来源的本地观测时间。

收到 Q 份回答不代表同一目标获得 Q 支持。评估在串行观察切面冻结；迟到通知进入后续评估，保留其他有效来源。准备期间 A/B 证人变为 A/C 时，用新的冻结快照和当前证人提交，复用已经取得的对象。

设 C 为已提交 tip，H* 为当前最高达标节点：

| 关系 | 交付行为 |
| --- | --- |
| 无 H*、H*=C、H* 是 C 的祖先 | 保持 C，支持变化进入必要审计 |
| C 是 H* 的祖先 | 补齐全部中间块，再原子提交 |
| 两者分叉 | 找共同祖先，交付完整旧路径撤回与新路径追加 |
| 关系未知 | 按 hash 恢复关系，不猜测分叉或同枝 |

同枝支持下降不产生纯祖先撤回，是明确的稳定性政策。初始化只交付一个出生锚点，不自动补播它之前的历史。

每个 HeadEvent 包含：

| 字段 | 含义 |
| --- | --- |
| Path.BaseVersion / NewVersion | 已提交版本与下一版本，成功提交增加一次 |
| Path.CommonAncestor | 本次变更的共同祖先；初始化无旧路径 |
| Path.Retracted | 旧 tip 向共同祖先方向的全部已交付撤回节点 |
| Path.Appended | 从共同祖先之后开始、按高度递增的完整 Header |
| Path.PreBirthProof | 连接未交付出生前路径所需的证明节点 |
| Path.Reason | InitialAnchor、ContinuousAdvance、Reorg 或 PreBirthReorg |
| Evaluation | 本次观察切面、目标和不可变路径证人 |
| Audit | 自上次交付后的必要过程事实 |

BirthAnchor 不改变；pre-birth 重组交付了更低的节点后，当前路径的已交付下界下移。以后重组必须撤回这些已交付节点。裁剪缓存不改变撤回范围：即使只缓存 104、105，重组仍可能需要恢复并撤回 105、104、103、102、101。

完整 delta、最终证据、审计前缀和队列预留成功后，一次提交同时更新账本与可见事件。失败不污染已提交版本。Next 按 FIFO 返回完整事件；未消费的 X→Y→X 保留三个变化，不能合并成最终 X。

## 3. 路径证人与对象供应

Q 支持规范头 hash 及其已验证祖先路径。每个 PathWitness 包含来源/session、读取与观察修订、评估修订、实际 Tip 和到目标的路径。它不声称 RPC JSON 可选字段的 presence 已被 Q 源逐字段比较。

ObjectSupplier 独立记录提供完整对象的来源/session、Push 或 ByHash、请求 ID 与目标 hash。Session 是发起时绑定并在完成时复核的订阅会话代次，不是 TCP 连接 ID，也不证明 hash 请求与 push 使用同一条 socket。缺失对象可从固定成员中任一合法供应者取回，不要求逐祖先再做 Q 份相同对象投票。供应者可以不在当前证人集中；按 hash 查询不更新其 Tip 或 TTL。

首个合法对象即可推进，其他请求被取消并最终由订阅清理等待。null、错包或失败不剥夺其他成员机会。对象、父 hash 和高度逐跳核验；不能仅按高度查询替代目标 hash 绑定。

已验证同枝延伸不打断有用恢复。105 恢复时跳到 107、106 未知，旧 105 暂无可证明支持也不能推断已分叉；保留受预算约束的有用工作，补明关系后重新评估。已证明不再有用途的未提交分支请求可取消，取消未退出的任务继续占并发额度。较早完整且仍受支持的目标可以先提交，再继续交付已就绪的新目标，无需等待另一条通知。

## 4. 显式策略与时间

HeadSubscriptionPolicy 不设隐含业务期限。以下资源值必须显式为正：

| 字段 | 含义 |
| --- | --- |
| MemoryBytes | 单订阅的预算准入额度，覆盖其拥有的原文、解码预留、对象、图、证据、审计、队列与请求状态 |
| RetainedNodes | 无活动用途、可重取拓扑的软容量；活动必要节点可超出它，但仍受总预算约束 |
| MaxConcurrentRequests | 同时执行的对象请求上限，包括已取消但尚未退出的请求 |
| WireMessages | 每个来源通知队列的条数上限 |
| WireBytes | 每个来源通知队列的字节上限 |
| MaxMessageBytes | 单通知大小上限，也是 Head 解码的原文准入上限 |

关系必须满足 MaxMessageBytes ≤ WireBytes ≤ MemoryBytes。多个来源的通知共享该订阅的 MemoryBytes；WireBytes 不是每源额外增加的一份总预算。最大通知能通过尺寸检查，不表示剩余预算一定能容纳它的解码与保留材料。

`DefaultHeadSubscriptionPolicy()`、`DefaultLogsSubscriptionPolicy()` 与 `DefaultReceiptsSubscriptionPolicy()` 返回可检查、可修改的结构体值，统一数值由 `internal/subscription/defaults.go` 拥有。手工构造全零 policy 仍本地拒绝。适配器直接使用这组预设，不再要求 geth 调用方填写预算。

| 预设 | 固定默认值 |
| --- | --- |
| Head | MemoryBytes 32 MiB；RetainedNodes 128；MaxConcurrentRequests 4；WireMessages 64；WireBytes 1 MiB；MaxMessageBytes 64 KiB |
| Logs 内容 | MemoryBytes 1 GiB；MaxResponseBytes 1 MiB；MaxConcurrentRequests 2；Head 使用上述预设 |
| Receipts 内容 | MemoryBytes 2 GiB；MaxResponseBytes 1 MiB；MaxConcurrentRequests 2；Head 使用上述预设 |
| 时间 | RequestTimeout 10 s；RecoveryTimeout 2 min；RetryDelay 250 ms；Head AuditTimeout 2 min |

这些值是所有权准入额度，不是启动时预分配的 heap，也不是 RSS 上限。内容工作区沿用 `256 * MaxResponseBytes + 16384`，两个并发槽合计略高于 512 MiB；Receipts 胜出对象另按既有 64 倍规范字节记账。TTL、Gate 和 MaxRecoveryAttempts 没有新增隐含值；预设不提供 finality 或新的 F1 新鲜度保证。超出单条响应、积压或总额度时仍显式失败；更大负载可通过原生 policy 表达。默认值的负载验收独立记录于第 9 节。

RequestTimeout、RecoveryTimeout、AuditTimeout、TipTTL 和 MaxRecoveryAttempts 的零值分别关闭对应期限、TTL 或尝试限制；RetryDelay=0 表示不自动重试。负时长和矛盾上下界本地拒绝。未配置期限时不承诺有限时间恢复或审计可见，有限内存仍可能显式终止流。

TipTTL 管理当前支持的新鲜度，不证明历史安全，也不因对象恢复而刷新。RecoveryTimeout 和尝试量在同一未完成恢复期间累计，不能靠新通知或目标抖动重置；新推送不能绕过 RetryDelay。

Gate 由 Initial、Min、Max 和可选 Estimate 组成；全部零值关闭。启用时给出正值冷启动和有效上下界。Gate 到期记录 WaitExpired 并重新评估；不清票、不缩 Q、不取消 RPC、不代替重试策略。路径已具备 Q 支持且完整时立即提交。

Estimate 只接收已提交目标的同 hash 多源到达样本，单个有界 worker 在核心锁外运行，结果钳位。不同高度间的时间差不是同块到达样本，样本不足沿用初始或已有估算；待处理样本可以更新为最近一次合格提交。回调必须及时返回，不得调用本订阅的 Close；Close 会等待该 worker，库不能强制终止任意用户函数。

样本领取与停止在同一控制锁内判定。停止先发生时不再领取新样本；已领取的回调允许继续，Close等待其实际返回，不增加隐含回调期限。

## 5. 审计与终止

AuditRecord 用连续序号和有界字段保留来源改报、撤票、会话断开、非法对象、请求失败、评估替换和等待到期等必要事实。通知容量、消息过大、来源建立失败有独立原因；不默认保存任意原文或错误字符串。来源改报不是自动判定恶意。

Audit.First/Last 定义交付范围，通过 All() 逐条读取。不要把直接 JSON 编码 HeadEvent 当作完整审计导出：记录由私有链表拥有，调用方应显式遍历 All() 并保存所需字段。库不提供持久审计或重启重放。

准备冻结审计前缀，提交只转移该前缀并切断与后续记录的连接。保存旧事件不会挂住未来审计。AuditTimeout 从最早未交付事实开始，新消息与重试不延后期限；没有待交付事实时不会凭空超时。

| 结束原因 | Next 与资源行为 |
| --- | --- |
| 生产端失败 | 先排空已提交 FIFO，再返回一次 HeadTerminalError，携带 Cause 与剩余 Audit |
| 终态已返回后的 Next | ErrHeadTerminalReturned |
| HeadSubscription.Close | 丢弃未消费队列，取消并等待自身任务退出 |
| Next(ctx) 取消 | 主动取消该订阅、丢弃队列并等待退出 |
| Client / view Close | 取消 owner、关闭 pool，并等待通过 AttachSubscription 登记的交付；直接创建的原生流仍须调用自身 Close 取得完成屏障 |

主动关闭可覆盖生产端失败后的排空状态。即使生产任务已经结束，owner 随后关闭仍会清理未消费队列。Next 串行化消费者；业务上应由一个消费循环按序处理。

工作停止和交付结束分别管理。全部来源已耗尽重试机会时，FIFO 之后的终态不等待慢退订；Head.Close 仍等待来源退订、在途请求、估算回调及独立 owner watcher 真正退出。Logs/Receipts.Close 同样等待各自内容任务、私有 Head 和 owner watcher。

`Terminated` 在 producer 确立终态时关闭，独立于未读 FIFO、网络清理和 Next 消费。信号关闭后 `TerminalError` 原因稳定；主动取消为 nil，已有真实故障不会被随后 Close 覆写。该观察只保留原因，不持有带审计所有权的终态包装。完整终态审计仍由 Next 按 FIFO 交付；显式 Close 可丢弃它。

外围交付通过 `Client.AttachSubscription` 在建立前登记 `Stop`／`Done`：Stop 幂等且非阻塞；Done 仅在原生 Close、交付任务退出及 Err 关闭后关闭。Client.Close 先封闭登记并停止全部已登记项，再关闭 pool、等待全部 Done；并发 Close 共享同一个完成屏障。登记锁不覆盖 Stop、网络清理或等待。提前 detach 不生效，不能让未完成交付逃离屏障。此边界由 ethadapter 自动使用，原生流不因此隐式登记。

`NextWithBudget` 在释放源账本前调用目标 `SubscriptionMemoryBudget.Reserve`，成功后由接收方持有 release，租约可以跨越原生 Close。Reserve 必须非阻塞且不重入订阅，调用时不持原生主控制锁；Close 会等待正在移交的读取退出。目标拒绝时 payload 留在原生 FIFO；Head 的失败审计以返回的 release 单独交付，Logs/Receipts 保留完整事件及审计待重试。消费者对成功或失败返回的 release 均负责最终释放。

来源会话在同一Head控制锁内检查停止、Open和Bind；停止之后不再准入新会话。已经准入的Subscribe可以在取消后迟到返回，其inner仍由原来源任务唯一退订，不再读取该迟到流。会话先退休，再撤票，网络清理在锁外；已取消但未返回的实际请求继续占并发槽。外部实现若忽略取消或阻塞退订，所属订阅的Close也会继续等待。

公开错误可用 errors.Is 识别 ErrHeadMemoryBudget、ErrHeadRecoveryExhausted、ErrHeadAuditTimeout、ErrHeadClosed 与 ErrHeadTerminalReturned；用 errors.As 取得 HeadTerminalError 后遍历剩余审计。资源准入失败不先接受事实再静默丢弃；建流时预留最小终态控制空间，已有审计通过所有权转移进入终态。

## 6. 长期内存与保证边界

每源保留一个轻量 Tip；完整对象在无活动用途时释放，未消费事件在 Next 后转交调用方，拓扑按容量裁剪，废弃请求与定时器注销。落后来源不拖住全历史。图表缩容同时记账新旧表，避免只删除条目但保留未计费的 backing storage。

MemoryBytes 是保守的分配与所有权账本，不是 Go heap 或进程 RSS 的硬隔离。解码先为 JSON 结构、规范化、hash 校验等申请工作区，费用大于原文；需要按来源数量、最大通知、并发恢复和消费速度留出余量。已交付且由调用方保存的对象由调用方负责。库不改变全局 GC 参数，不靠反复强制 GC 实现回收。

WSS／IPC 连接 ReadLimit 及共享双工核绑定前未知 ID 缓存有独立连接上限，不能冒称已计入某个 Head 的总预算。按 hash 取数的传输原文与解析也受相应 transport 上限约束，完整设备预算必须考虑连接及并发在途数据。具体字节归属见 WSS wire 合同。

有限内存下，过慢消费、必要审计持续积压或过深恢复可能明确终止。有限缓存可以恢复深重组，但不能同时承诺任意深度、历史对象永远可得和始终成功。

保证覆盖已提交路径变化与准入后的必要审计，不覆盖未观察到的链上变化、全部原始帧、进程崩溃恢复或主动关闭后放弃的事件。Corroborated 是固定 RPC 成员的共同观察，不是共识、最终性、规范链成员证明或运营者独立性。桥等上层仍拥有最终性、业务数据验证、持久审计与资金动作政策。

## 7. Logs 完整连续流

Logs 复用私有 Head 的出生锚点和完整 FIFO 路径，拥有独立的内容准备、提交位置、预算及交付队列。没有第二套 DAG 或逐高度投票器。每个 Head delta 必须完整准备成功后才能成为一个 LogsEvent；证据缺失时等待或显式失败，不跳块，不暴露半个 delta，也不将 X→Y→X 合并为最终 X。

空过滤表示出生起全量实时日志。数字 fromBlock/toBlock 和 blockHash 只筛选出生后路径，不补出生前历史；越过 toBlock 仍处理重组。latest 表示实时边界，earliest 对应 0，pending/safe/finalized 明确拒绝；历史查询使用 FilterLogs。地址候选为 OR；topics 各位置为 AND、同位置候选为 OR，null/空位置为通配，但日志须存在该位置。查询在入口冻结，未知额外字段本地拒绝。

出生块本身完整交付；出生前未被承诺的历史不自动补播。Head 的 pre-birth 重组可能追加更低区块，Logs 仍逐块处理；不能用初始出生高度屏蔽后续合法追加或必须撤回的区间。

| LogsEvent 字段 | 交付含义 |
| --- | --- |
| Path.BaseVersion / NewVersion | Logs 自己的原子提交版本，按消费到的 Head 路径逐次推进 |
| Path.CommonAncestor / Retracted | 完整旧路径撤回；每个块身份撤回该块此前全部已交付日志 |
| Path.Appended | 按路径顺序排列的 LogsDelivery，每块都有身份及全部匹配日志；零命中是非 nil 空列表 |
| Path.PreBirthProof / Reason | 保留 Head 的出生前关系证明和路径变化原因 |
| Evaluation / Audit | 该 Head 路径的冻结证人与窗口审计；Audit 通过 All() 导出 |
| ContentAudit | Logs/Receipts共用的内容请求事实，含Kind、连续序号、attempt、session、requestID、目标块、原因和时间；不保留原文或任意错误字符串 |

每个 LogsDelivery 记录 Proof、ObjectSupplier 和 ContentSource。Proof 区分范围/hash 排除、Header bloom 否定匹配、完整区块承诺证明；无需取内容时 supplier 为空。供应来源可以不是当前路径证人，但必须属于冻结成员，发起和完成均检查绑定的会话代次。一次完整块供应任务的 RequestID 关联它的按 hash 取数及必要同源依赖调用，不等同于传输 JSON-RPC 请求编号。

Header bloom 只能用于否定匹配，阳性不能证明存在或完整性。可能匹配块通过 eth_getBlockByHash(hash,true) 验证完整交易列表的 TransactionsRoot，再通过 eth_getBlockReceipts(hash) 验证完整收据的 ReceiptsRoot；需要时复用完整 block 准入的叔块等依赖检查。首份完整且合法的供应即可使用；失败可以尝试其他固定成员，不改变 Head 的 N/Q。并发额度允许多个供应者同时取数，仍可能收到多份完整载荷；单份证明足够不代表网络永远只传一份。流不依赖原生 logs 推送或 eth_getLogs 子集投票，因而不把共同漏推当作完整性证据。

正向 Log 的 address/topics/data 由收据承诺绑定；blockHash/number/timestamp 由已验证头绑定；transactionHash/index 和全块 logIndex 由完整有序交易及收据遍历绑定。所有提供的日志定位元数据在筛选前核对，不一致则拒绝整份供应并记审计。返回值独立构造，blockTimestamp 始终填头时间，removed=false；撤回统一由 Path.Retracted 表达。日志不交付 Receipt 的 from、费用或 blob 派生字段，这些不在 Logs 的字段证明范围。根证明约束内容与已接受 Header 的关系，不证明该 Header 是共识有效或最终区块。

LogsSubscriptionPolicy.Head 管理拓扑，独立 MemoryBytes 管理内容；MaxResponseBytes 和 MaxConcurrentRequests 必须为正。RequestTimeout 限制单供应任务，RecoveryTimeout 限制当前块恢复；RetryDelay=0 关闭重试，MaxRecoveryAttempts=0 关闭尝试上限，零期限不隐含业务 deadline。当前根编码使用固定 geth 的标准交易/收据编码；非标准链无法通过时显式失败，不透明回退到较弱子集投票。取消但未退出的任务仍占并发槽；配置单槽且不设期限时不能保证其他来源及时获得机会。

正常 Head→Logs 交接先预留目的预算再释放源预算。内容 RPC 前预留解码工作区，完整结果和审计保留量另行计费；MaxResponseBytes 是应用准入上限，transport 原文仍有独立上限。失败终态若无法预留目的预算，已有 Head 审计可保持原预算租约直至终态交付或 Close。已交付内容转交调用方，库不保留历史全量日志；过慢消费、过深恢复和审计积压可显式耗尽预算。

完成事实进入下一个成功批次的审计窗口；首份成功不等待慢少数派清理，随后完成的请求进入后续窗口。生产端失败时，在锁内封闭终态窗口，记录仍未退出任务的身份和 Unfinished 事实，再让终态可见；后到结果不能修改已交付审计。生产错误先排空已提交 FIFO，再一次返回 LogsTerminalError，含 Cause、Logs 最后提交位置/版本以及剩余 Head/内容审计。Close 才是所有后台任务的完成屏障。主动 Close、Next context 取消和 owner 关闭丢弃未消费事件；不承诺主动放弃后仍保存审计，也不提供持久化/Ack/Replay。

## 8. Receipts 完整连续流

Receipts复用私有Head的固定成员、Q、出生锚点和完整重组路径，与Logs共用内容交付、预算和实际任务回收。收据具有独立提交位置；Head前进不等于收据内容已完成。以下合同已于2026-11-25通过本地方法验收；单次TransactionReceipt/BlockReceipts属于此前read验收，不代替本流验证。

nil query、TransactionHashes未设置或空列表都表示全量实时收据；非空列表在入口校验并冻结为目标交易集合。非法hash在网络前拒绝，不能通过忽略坏项扩大范围。流只订阅newHeads，通过按hash读取完整区块和收据证明内容，不依赖原生transactionReceipts推送，也不把过滤后的共同漏推视为完整性。

首个来源仅提供出生候选，最终出生块B由Head固定Q路径支持及对象验证确定。首次交付覆盖B整块的所有目标命中；初始化期间先看到100、最终在103出生，不承诺补播100–102。出生后的每次已提交推进逐块完整，有块身份的零命中是有效完成事实。pre-birth重组、完整撤回与重入沿Head合同处理，不能以初始出生高度屏蔽后来已交付的较低路径。证据不足时等待或显式终止，不跳块，也不发布半个delta。

| ReceiptsEvent / ReceiptsDelivery | 含义 |
| --- | --- |
| Path | 本内容流的BaseVersion/NewVersion、共同祖先、完整Retracted/Appended及PreBirthProof/Reason |
| Evaluation / Audit | 对应Head路径的冻结证人及窗口审计 |
| ContentAudit | 共用ContentAudit；Kind区分完整Logs供应、交易供应和收据供应，RequestID关联具体请求任务 |
| Node / Receipts | 块身份及按交易顺序排列的全部目标收据；零命中为非nil空列表 |
| BlockSupplier | 提供完整且通过交易根验证的区块来源/session/request；可以不是路径证人或Receipt字段支持者 |
| ReceiptSupport | 字段一致声明的Status、固定Basis/Required、Attempt、Representative和Suppliers；零命中/空块不虚构支持者 |
| Proof | TransactionsExcluded、EmptyBlock或BlockCommitment，区分无需收据请求与完整收据证明 |

每块先从固定成员取得一份合法完整区块，直接消费 block.AdmitBlock 的完整准入产物，验证目标头hash、完整有序交易列表和TransactionsRoot，并从保留的规范 Block 解码交易；随后冻结交易及命中索引。过滤集合零命中直接交付带身份空集合。全量空块也无需收据网络请求，但仍验证空收据列表与头的ReceiptsRoot一致，拒绝矛盾的非空收据根。

有命中时，固定成员各自提供完整收据列表。共享receipt内核验证数量、顺序、块/交易身份、type和ReceiptsRoot；所有日志的块、交易及全块索引、removed和提供的时间戳在过滤前核对。非命中行的结构、身份或根错误仍拒绝整份回答。证明成功后才本地过滤，实际交付的完整规范官方字段进入既有CanonicalOutcomeResolver的固定Q裁定；非命中行的非根声明差异不改变过滤后的候选。

ReceiptsRoot不覆盖from/to、单笔gasUsed、effectiveGasPrice、contractAddress和blob等全部官方字段。这些值获得的是Q份来源一致声明，不是独立执行或链规则验证；不引入ChainConfig，不猜测主网fork。root/status同时提供时，根编码未使用的额外status仍参与Q比较。optional/nullable按既有Receipt/Log Encode合同保留或规范化；例如Log.blockTimestamp的显式null沿既有规则规范化为缺席，不另造统一null政策。仅胜出规范值解码为ethapi.Receipt，provider扩展不自动进入稳定主值。

每成员、每块、每次收据attempt至多一槽，失败、未回答及尚未调度成员仍在固定分母中；重试清除该轮候选，重连不能新增票。成员之间的session generation不要求相等。发起及完成检查既有SessionGate，断连退休先于慢退订；完成接纳先于退休的已证明事实可保留，先退休则拒绝旧结果。显式单源保持Forwarded，不冒称Q多源互证。

ReceiptsSubscriptionPolicy.Head管理路径，MemoryBytes管理内容；MaxResponseBytes与MaxConcurrentRequests必须为正。RequestTimeout限制每个供应任务，RecoveryTimeout覆盖当前块的交易供应和收据裁定，RetryDelay=0关闭重试、MaxRecoveryAttempts=0关闭尝试上限。交易供应与收据裁定各自计attempt，共用该块恢复期限和实际并发槽。达Q可取消剩余任务，任务真正退出才归还槽及头/交易租约；慢少数派不必阻塞已达Q交付，但Close等待其清理。单槽或不设置期限时不承诺及时换源或有限时间返回。

pool.Source.Call保留真实Response，CallContext仅投影，不从raw/error重建交换和回答事实。完整Response、规范候选、resolver临时复制、胜出typed值、队列和审计均计内容预算；无法取得保留预算时显式失败，不让未计费载荷跨任务保留。正常Head交接先预留目的预算，失败审计可继续持有原Head租约，消费或主动关闭后归还；预算不是Go heap/RSS硬上限。

每次delta全部成功才原子入队并推进版本。生产失败先排空已提交FIFO，再一次返回ReceiptsTerminalError，包含Cause、最后内容提交位置/版本和剩余Head/ContentAudit；终态冻结时记录未退出任务，慢清理不延迟错误可见。主动Close、Next取消及owner关闭丢弃未消费交付；Close等待实际工作、方法资源清理和独立owner watcher。生产工作结束与交付排空是不同事实，producer结束后owner取消仍能释放未消费材料。

旧核心 channel 桥接、事件 Aggregator、同源 Pull 探测和生命期累计票据预算已删除；外围 ethadapter 通过公开原生流消费事件，不恢复旧 owner。Head 的 Mode、SessionGate、队列和滚动所有权预算保留。跨入口生命周期收敛已验收，诊断及真实 provider 观察仍独立待办；没有共享 Hub、跨订阅 single-flight、WAL/Ack/Replay、历史补播或 finality 保证。

## 9. 验证与实现入口

internal/subscription 是 Head/Logs/Receipts 唯一 owner，拥有状态机、worker、队列、session、恢复、内容证明与交付。它通过注入能力编排，不拥有连接实现；Source 接口与固定成员快照回调由 root 的 Client 生命周期适配提供，允许消费中立 transport port，禁止依赖 pool/runtime/methods/具体 transport。root 保留公开入口、稳定类型 alias、默认策略构造及外围交付生命周期登记，view 复用同一执行。私有 contentSubscription[E] 为 Logs/Receipts 共用 typed FIFO、版本提交、预算、实际任务、审计及 Close 完成屏障；具体流各自拥有过滤、证明与重试，receipts 共用 internal/rpcengine 的规范值裁决。共享 FilterQuery 冻结与拼写兼容属于 ethobject/log；unary 保留未知扩展和精确数值，stream 编译时拒绝未知扩展。白盒测试随 owner 移动，公开客户端验收留 root/verification。双工 notification 的缓存与租约由 internal/transport/streamrpc 拥有，注入来源透传中立有界订阅能力并保留真实 Response。

反例覆盖共同祖先支持、迟到合票、会话撤票、完整 gap/reorg、证人替换、未知跳高与恢复活性、发布原子性、审计前缀/期限、慢消费者、长期保留、图缩容、并发关闭和 WSS pre-bind/原文释放。合成对象测试、完整 Header、真实本地 WSS peer、Anvil 和真实 provider 是不同证据层；本轮没有新增 Anvil 或 provider 采集。

### 2026-11-28 默认预算与外围桥本地验收

`internal/subscription/defaults_test.go` 直接消费默认策略：每块 240 笔签名交易、720 条日志，calldata 为 32／128／512 B，日志 data 为 32／128／256 B、topics 为 1–3 项。代表块 JSON 262,566 B，完整收据及规范胜出值 772,413 B。高度 100→120 连续消费后，一次交付 20 块撤回及 20 块追加，再继续至 121；Logs 使用两源，Receipts 使用三源。测试包含完整交易／收据 trie 承诺，未手调策略、未减小既有 256／64 倍记账系数。

| 测量 | Logs | Receipts |
| --- | ---: | ---: |
| 实际同时执行的内容调用／持有请求 | 2／2 | 2／2 |
| 内容预算抽样最高值（B） | 550,415,399 | 1,592,123,372 |
| 私有 Head 预算抽样最高值（B） | 48,762 | 57,318 |
| Close 后内容／Head 账本 | 0／0 | 0／0 |

抽样发生于实际内容调用期间，明确排除 Head-only 恢复；同时断言两个解码工作区都已预留。上述数值是一次普通运行的抽样保留额度，受调度影响，不是 Go heap、RSS 或全时刻峰值。大负载普通与 race 均通过；这是合成代表性负载，不代表任意主网区块、任意来源数或全部执行客户端保证。

独立 `ethadapter/subscription*_contract_test.go` 及终态白盒覆盖锁定 geth 的完整 47 项签名、bind/v2 合约 watch、三类正常推进、空日志块重组、永久未读 channel、所有关闭入口、回调 panic／修改、建流 context 生命周期，以及真实本地 WSS／Unix IPC／混合来源。桥队列满、预算交接拒绝与接收方租约跨 Close 的反例均验证释放；两槽饱和后桥字节与租约账本归零。

审计先后发现并修复 streamrpc 建流失败的 typed nil 清理 panic、Head 主动关闭先暴露 ErrClosed 后发布 nil 终态的窗口，以及将读取锁持有范围扩到清理等待造成的并发 Close 回归。Head 与内容 Close 仅短暂获取读取锁，随后在锁外等待任务结束。修复后的冻结版本通过最终全仓普通／race，各 59 个测试包、10,433 个测试及子测试通过事件，另 8 包无测试，零失败、零跳过测试。完整静态／生成／平台编译门禁见[接入设计验收记录](access-plane-ipc-adapter.md#8-最终本地验收与剩余平台门禁2026-11-28)；Windows 真实 pipe 运行与新增 provider 观察仍未完成。

### 2026-11-25 生命周期本地验收

Head会话准入/退休、唯一退订、来源生产耗尽与实际退出、独立owner watcher完成屏障以及root/view三流关闭后拒绝已收口。旧实现的慢退订遮挡终态及取消后读取迟到inner均由反例复现；新增证据覆盖停止先行、重连Bind失败、建立失败仍可重试、并发Close等待Estimate与watcher、owner取消先于watcher获锁，以及能力/策略拒绝不触网。保留三流既有迟到/重组/预算/长期回收证据。

冻结代码由主控独立执行整仓普通与race（-p 2、-count=1、timeout分别180s/240s），均退出0，各51包通过、9包无测试、0失败。vet/build、lint零告警、格式和生成资产逐字节一致；build有非致命模块stat缓存只读警告。没有新增provider或发布证据，代码未提交。

每成功Head会话删除一辅助goroutine、两协调channel和一个Once；每Head增加一个watcher完成channel。实际来源任务、请求、clock、可选estimator及transport任务保留。完整Receipts流程的请求数、响应字节和消费后保留量不变，耗时样本与专项race并行，不据此宣称性能变化。关闭只承诺等待实际完成，无法强制中断任意外部回调。

2026-11-25重启前保存：用户要求已修改Go代码落Git，Receipts及生命周期的30个Go文件统一保存为本地提交f40a9e8（父提交d3b18e3），未推送。上述冻结验证对应此提交的Go内容；质量治理只读盘点没有混入代码修改。正式Markdown当前仍保留工作区差异。

### 2026-11-25 Receipts本地验收

root/view新入口、完整块证明及固定Q、共享内容owner和旧桥接删除已完成。反例覆盖完整出生/跳高/空块、非根字段及额外status的Q冲突/一致、presence、过滤前完整性、固定分母/Forwarded、跨attempt、pre-birth重组重入、后段块失败不发布半个delta、断连慢退订拒绝旧票、慢少数派、FIFO终态、并发Close、Head/内容审计以及交接/候选/胜出值预算失败。真实本地WSS覆盖root/view×单源/多源，旧通知释放和建立/重连清理保障迁到Head反例。

主控独立整仓普通`go test -json -p 2 -count=1 -timeout=180s ./...`及race（timeout=240s）均退出0，各51包通过、9包无测试；vet/build、gofmt/diff及生成Go/审计资产重新生成逐字节一致。首次lint发现一处错误哨兵直接比较，改为errors.Is后lint为0告警，修后Receipts全组race独立复验通过（4.377s）；整仓结果对应该单行修复前冻结代码，差异及定向补验分别记账。build的只读模块stat缓存警告非致命，不记作无警告构建。依赖、schema及历史采集未变；没有新增Anvil/公网provider证据，代码尚未提交。

最小完整流程benchmark包含Head输入、证明、Q及typed交付，排除网络和fixture构造。执行者两次测量Forwarded约3.51ms/770KB/14701 allocs，Q=2约4.23–4.35ms/947–948KB/17696 allocs，零命中Q=2约2.93–3.03ms/677–678KB/13045 allocs。每次分别为1 block+1 receipt/5160B、1 block+2 receipts/7524B、1 block+0 receipt/2796B；主控20次复测确认调用/字节及消费后内容保留量18,408/18,920/19,112B，耗时在并发race负载下波动，不据此推断性能提升。35块连续消费保留量不超过首块基线+4096B，关闭后Head和内容预算归零；这些是预算账本及本次负载证据，不是RSS上限。

Receipts验收时跨入口生命周期仍独立待办；准确Unsubscribe次数、迟到事件不读取、Head owner watcher完成及生产耗尽先于慢清理随后由上方生命周期专项验收，不以本方法结果代替。

### 2026-11-24 Logs 本地验收

Logs专项在root和facade共26个顶层、49个子测试普通/race通过；覆盖完整内容、官方定位字段、冻结过滤、范围与零命中、乱序补齐、pre-birth/完整重组及X→Y→X、慢少数派、旧会话/过期结果、迟到窗口审计、内存失败原子性和关闭完成屏障。真实本地WSS覆盖root/view×显式单源/多源非空交付与证据等级，未知查询字段在网络前拒绝。共享队列覆盖正常预算交接及失败审计租约，生产终态的原始原因在交接失败时仍保留。

最终工作树的 `go test -p 2 -json -count=1 -timeout=180s ./...` 和对应 `go test -race -p 2 ...` 均退出0，各51包通过、9包无测试；vet/build退出0，gofmt与diff检查通过。build保留只读模块stat缓存写入警告，不影响退出状态。未改依赖、生成模型或schema；没有新增Anvil/公网provider证据。验收后按用户授权将代码分组保存为`efe95f6`及`ee589ce`，未推送。

验收过程保留：sandbox首次禁止本地监听，申请测试环境权限后重跑；新WSS fixture遗漏to和错填日志时间戳被校验拒绝，修正fixture后四入口race通过。首次并行整仓普通另有既有同步写入250ms观察断言失败，最终降低包并发复验普通/race均通过；未修改该生产或测试逻辑。这次历史失败不记为通过，也不等同已排除该用例所有调度敏感性。

持续交付测试覆盖30个完整区块，消费后预算未随历史线性累积，Close后Head和Logs预算归零。它验证本次负载下的所有权回收，不代表RSS上限或任意负载永久存活。全部Logs合同和本地反例已收口，receipts连续流及跨入口最终验收独立继续。

### 2026-11-23 NewHead 验收与成本

主模型在执行者冻结源码后独立完成全仓普通测试与 race、vet、build、tidy 差异检查；生成 Go 与 audit 资产重新生成后逐字节一致，gofmt 和差异检查通过。普通与 race 命令均使用 `-count=1 -timeout=180s`；当日验收时尚未提交、推送或发布，后续本地保存点见上方2026-11-24记录。

长期测试区分持续保留量与累计分配量：

| 场景 | 实测结果 |
| --- | --- |
| 完整 Header、500 个高度、598 个已消费 delta | 消费后稳定状态预算峰值 39,669 B；Close 后预算归零、无活动请求 |
| 合成 tracker、3,000 步连续推进与分支变化 | 保留预算峰值 7,259 B |
| 300 块恢复后裁剪 | 图容量缩回 16，旧表与 fetch 引用释放 |

入口分别为 `internal/subscription/newhead_integration_test.go` 的 `TestHeadRealHeaderLongAdvanceAndForkRetentionPlateaus` 及 `internal/subscription/head_tracker_test.go`。这些是具体负载下的预算账本测量，不是整个运行期间的分配峰值、Go heap/RSS 上限或任意负载的证明。

Go 1.26.10、Intel i5-8300H、150ms 本地 benchmark：

| 场景 | ns/op | B/op | allocs/op |
| --- | ---: | ---: | ---: |
| tracker 三来源 | 20,034 | 25,892 | 75 |
| tracker 慢来源 | 16,892 | 20,311 | 62 |
| tracker 保留窗口 1 | 6,647 | 4,659 | 45 |
| tracker 积压 32 | 16,319 | 20,310 | 62 |
| tracker 重复头 | 2,090 | 1,472 | 17 |
| tracker 跳高 8 | 69,419 | 54,934 | 314 |
| 完整 Header 解码，原文 1,465 B | 192,084 | 45,443 | 780 |
| 4,000 个短未知字段，原文 40,355 B | 5,920,684 | 2,357,257 | 64,887 |

当前解码准入预留为 `256*len(raw)+16KiB`，上面两个解码样本分别预留 391,424 B 与 10,347,264 B。4,096 个额外字段已超过现有 JSON-safe 总键数限制，因此成功解码压力样本采用 4,000 个字段。字段多而短时结构与规范化成本明显高于原文字节，不能直接用 WireBytes 作为所需 MemoryBytes。

复现入口为 `internal/subscription/head_benchmark_test.go` 与 `internal/subscription/newhead_benchmark_test.go`。这些测量用于解释预算余量与局部成本，不代表生产吞吐、最坏情况上界或多源设计的极限。
