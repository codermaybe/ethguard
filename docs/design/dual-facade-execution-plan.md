# 双切面与统一调用结果执行计划

状态：**本轮冻结范围实施完成（2026-11-06，含两轮复审修复）**。十二个阶段已执行，
首轮六项反馈及二次复审的两项 P2 内存边界问题均已修复并补回归。最新 G2/G3
在修复后代码上通过；二次复审记录见第 9.1 节，替代此前过早的“无验收欠账”结论。
交付仍为本地未提交 diff（基线 `31e6898`）。HTTP/WSS 方法扩展、订阅和后续架构精简
不属于本轮完成声明；其待执行计划见 `tmp/transport-coverage-and-simplification-plan.md`。

维护者已同意十项收敛意见，并要求 agent 按完整计划连续推进、每阶段自检、最终自检。
本文是本轮唯一实施依据，完整包含接口、行为、执行顺序和验收要求；不需要先读临时
讨论才能执行。历史背景在 `docs/design/tmp/evidence-sink-design.md` 与
`docs/design/tmp/dual-facade-api-spec.md`，其中未决项、两值 capture 签名和旧候选不能
覆盖本文。正式架构记录在实施阶段追加，不能把“已批准方向”写成“已实现”。

本计划编写时基线为 `31e6898`，工作区干净，Go 为 go.mod 中的 1.26.10，
go-ethereum 为 v1.17.5。执行 agent 开始时重新盘点，不假设 HEAD 或工具环境未变。

## 1. 执行纪律与完成定义

- 按第 5 节依赖顺序推进。每阶段实现、自检、修复、记录后继续，不等待阶段间确认。
- 不写阶段编号命名的生产文件、测试文件或任务文档，例如 phase1.go、stage2_test.go、
  task03.md。阶段编号只用于本文表格和状态记录。文件名描述职责。
- 推荐职责名：`call_result.go`、`result_capture.go`、`ethclient_view.go`、
  `execution.go`、`reference_result.go`；测试使用对应职责或行为名。优先复用现有
  归属，不为匹配建议清单创建空文件或无意义层次。
- 全程仅一份业务实现；每个操作的校验、reference、裁决、typed 投影、绑定和最终错误
  由共同实现拥有。两切面和 capture 只负责输入形状、结果交付。
- 不承担向后兼容；不能用 deprecated 别名、adapter shim 或第二套 capture 逃避迁移。
- 不创建远端 issue / PR，不 push、不打 tag、不发布。最终交付可为已验证的本地 diff；
  是否提交由执行会话的授权决定，不以生成 commit 作为本计划完成前提。
- 保留执行开始时发现的用户改动；禁止 reset、clean 或覆盖不相关工作。
- 阶段出口未满足不能勾选完成。编译迁移量大、测试要改、需要显式克隆，不是停止理由。
- 发现真实契约矛盾、既有用户修改冲突、必须新增未授权能力时，记录具体反例和影响，
  停止受影响路径；完成不受阻的工作，不擅自降级安全规则。
- 本轮完成指代码、仓库调用方、正式文档、测试证据全部完成。只写设计、只通过编译、
  删除失败测试或只迁移余额方法，都不是完成。

## 2. 冻结的公开接口

### 2.1 两个切面及结果

```go
type EthClient struct {
    owner *Client // 私有命名字段，禁止匿名嵌入
}

func (c *Client) EthClient() *EthClient
func (c *Client) Close()
func (ec *EthClient) Close()

type CallResult struct {
    Decision      *rpc.Decision
    WriteDecision *rpc.WriteDecision
    Uncertain     *UncertainResult
    Err           error
}

func (r CallResult) IsCorroborated() bool
func (r CallResult) IsWriteAccepted() bool

type ResultCapture struct { /* private state */ }
func WithResult(ctx context.Context) (context.Context, *ResultCapture)
func (c *ResultCapture) Result() (CallResult, bool, error)

// 只配置证据保留；不隐式创建 ResultCapture，不改变业务规则。
func WithSourceEvidence(ctx context.Context, budgetBytes int) (context.Context, error)
```

New、Dial、DialContext、DialUncorroborated、LoadFile 继续由根包构造原生 Client，
不新增独立 adapter 包和另一套配置。EthClient() 不联网、不新建池、不复制配置；
不承诺指针恒等或零堆分配，不用性能假设作为架构门禁。

Result 的第二个返回值仅表示公开调用已经正常发布；第三个是 capture 使用 / 发布
错误，与 CallResult.Err 分离。公开 `ErrCaptureReused` 和 `ErrCaptureAborted`；
前者表达重用失效，后者表达绑定调用因 panic 等异常退出未发布正常结果。捕获错误
不得进入业务方法 error，也不得替换 CallResult.Err。nil / 零值 capture 的 Result
返回零结果、false、nil，不伪造已完成调用。

CallResult 中 Decision 与 WriteDecision 互斥；尚无决策时可以同时为空。
IsCorroborated 只检查 Decision 非 nil 且 Status=Corroborated，不检查最终 Err；
IsWriteAccepted 检查 WriteDecision 非 nil、Status=WriteAccepted、Hash 非空。
后者不声明交易已上链。费用选择型方法成功可以是 Selected。

### 2.2 完整 typed 读取矩阵

下面 21 个方法参数在两个切面相同，原生增加 CallResult，ethclient 视图保持锁定
版本官方签名。TransactionByHash 保留 pending，所以原生是四返回值，不引入另一种
业务包装类型。导入别名 gethrpc 指 go-ethereum/rpc，rpc 指项目自身 rpc。

| 方法与完整参数 | EthClient 视图返回 | 根 Client 原生返回 |
| --- | --- | --- |
| `ChainID(ctx context.Context)` | `(*big.Int, error)` | `(*big.Int, CallResult, error)` |
| `NetworkID(ctx context.Context)` | `(*big.Int, error)` | `(*big.Int, CallResult, error)` |
| `BalanceAt(ctx context.Context, account common.Address, blockNumber *big.Int)` | `(*big.Int, error)` | `(*big.Int, CallResult, error)` |
| `NonceAt(ctx context.Context, account common.Address, blockNumber *big.Int)` | `(uint64, error)` | `(uint64, CallResult, error)` |
| `StorageAt(ctx context.Context, account common.Address, key common.Hash, blockNumber *big.Int)` | `([]byte, error)` | `([]byte, CallResult, error)` |
| `CodeAt(ctx context.Context, account common.Address, blockNumber *big.Int)` | `([]byte, error)` | `([]byte, CallResult, error)` |
| `BlockByHash(ctx context.Context, hash common.Hash)` | `(*types.Block, error)` | `(*types.Block, CallResult, error)` |
| `BlockByNumber(ctx context.Context, number *big.Int)` | `(*types.Block, error)` | `(*types.Block, CallResult, error)` |
| `BlockNumber(ctx context.Context)` | `(uint64, error)` | `(uint64, CallResult, error)` |
| `BlockReceipts(ctx context.Context, blockNrOrHash gethrpc.BlockNumberOrHash)` | `([]*types.Receipt, error)` | `([]*types.Receipt, CallResult, error)` |
| `TransactionReceipt(ctx context.Context, hash common.Hash)` | `(*types.Receipt, error)` | `(*types.Receipt, CallResult, error)` |
| `TransactionByHash(ctx context.Context, hash common.Hash)` | `(*types.Transaction, bool, error)` | `(*types.Transaction, bool, CallResult, error)` |
| `SuggestGasTipCap(ctx context.Context)` | `(*big.Int, error)` | `(*big.Int, CallResult, error)` |
| `SuggestGasPrice(ctx context.Context)` | `(*big.Int, error)` | `(*big.Int, CallResult, error)` |
| `CallContract(ctx context.Context, msg ethereum.CallMsg, blockNumber *big.Int)` | `([]byte, error)` | `([]byte, CallResult, error)` |
| `EstimateGas(ctx context.Context, msg ethereum.CallMsg)` | `(uint64, error)` | `(uint64, CallResult, error)` |
| `FilterLogs(ctx context.Context, q ethereum.FilterQuery)` | `([]types.Log, error)` | `([]types.Log, CallResult, error)` |
| `PeerCount(ctx context.Context)` | `(uint64, error)` | `(uint64, CallResult, error)` |
| `SyncProgress(ctx context.Context)` | `(*ethereum.SyncProgress, error)` | `(*ethereum.SyncProgress, CallResult, error)` |
| `FeeHistory(ctx context.Context, blockCount uint64, lastBlock *big.Int, rewardPercentiles []float64)` | `(*ethereum.FeeHistory, error)` | `(*ethereum.FeeHistory, CallResult, error)` |
| `BlobBaseFee(ctx context.Context)` | `(*big.Int, error)` | `(*big.Int, CallResult, error)` |

### 2.3 写入、原生扩展与通用读取

| 方法与完整参数 | 根 Client 返回 | EthClient 视图 |
| --- | --- | --- |
| `SendTransaction(ctx context.Context, tx *types.Transaction)` | `(CallResult, error)` | `error` |
| `SendRawTransaction(ctx context.Context, rawTx []byte)` | `(CallResult, error)` | 不暴露 |
| `CreateAccessList(ctx context.Context, msg any, blockNrOrHash ...any)` | `(*AccessListResult, CallResult, error)` | 不暴露 |
| `AnchorBlockHash(ctx context.Context)` | `(string, CallResult, error)` | 不暴露 |
| `AnchorBlockHashForTag(ctx context.Context, tag string)` | `(string, CallResult, error)` | 不暴露 |
| `AnchorBlockHeight(ctx context.Context)` | `(uint64, CallResult, error)` | 不暴露 |
| `Call(ctx context.Context, method string, params ...any)` | `(CallResult, error)` | 不暴露 |
| `Close()` | 无返回 | 无返回 |

Call 的成功 JSON 在 result.Decision.Value，原始读取没有额外重复的业务值返回。
CallResult.Err 与末尾 error 来自同一执行结论。写入 hash 从 WriteDecision.Hash
读取；删除旧 WriteResult 类型及其专有 helper，迁移全部仓库消费者。

删除公开 CallInto[T]，执行编排不搬进 internal/typed；该包继续只拥有解码 / 投影。
删除 WithUncertain、UncertainCapture 及旧捕获别名；保留 UncertainResult、Field、
Response 等新结果仍需要的材料类型和查询能力，不把材料查询误删为兼容负担。

Pool、Mode、VotingMembers、Normalize 和 Decode* 维持既有管理 / 纯函数职责，
不加 CallResult，不进入 capture，不暴露于 EthClient。保留 gethcompat 显式 transport
路径，不能把它与已删除的 ethadapter 混为一谈。

### 2.4 生态范围

EthClient 的导出方法集合严格为 21 个 typed 读取、SendTransaction、Close。
使用项目锁定版本的 `accounts/abi/bind/v2` 检查 ContractCaller、DeployBackend：

```go
var _ bind.ContractCaller = (*ethguard.EthClient)(nil)
var _ bind.DeployBackend = (*ethguard.EthClient)(nil)
```

不声明完整 ContractBackend；不添加 Header*、Pending*、订阅或同步发送桩。
它不是官方具体 *ethclient.Client，文档不能承诺所有 SDK 一行替换。

## 3. 冻结的行为契约

### 3.1 执行归属及结果发布

共同操作在最外层公开业务入口创建显式执行状态，先绑定 capture，再校验参数。
内部业务子操作直接调用共同私有实现并显式传递状态，不再次穿越公开门面。
context 可承载调用者的 capture 和材料配置，不重新传递 quorum、basis 或业务中间态。
底层现有证据采集桥可以渐进迁移，但不能把临时桥变成新的公开契约或第二事实源。

原生和视图各自的一次调用均只执行一次业务；直接通过原生方法转发的视图不得重复
绑定或发布。只有外层在 typed 投影 / 绑定或写入绑定完成后发布一次最终结果。
本地拒绝、关闭、取消、业务失败均发布正常完成结果；panic 继续传播，捕获标记异常，
不能依赖无条件 defer 发布 err=nil 的半结果。重用失效优先于异常发布状态。

锁保护 capture 的未绑定 / 执行中 / 已完成 / 失效状态。在入口原子占用；同一句柄
进入第二次独立调用，无论第一次是否已完成，永久失效，后续 Result 返回零结果、
false、ErrCaptureReused。第二次业务仍按正常规则执行，不能因 capture 重用被拒绝。
第一次已交付给用户的副本无法撤回，文档只承诺后续读取不再冒充正确归属。

Result 不等待业务完成；允许短暂锁竞争，不声称无锁。未发布返回零值、false、nil；
正常发布返回隔离副本、true、nil。锁内不执行 RPC、用户回调或等待业务结束。

### 3.2 聚合、reference 与最终业务错误

| 场景 | 顶层 Decision | Reference | 最终业务 error |
| --- | --- | --- | --- |
| 本地拒绝，尚未形成任何裁决 | 可为 nil | 无 | 参数错误 |
| 独立 anchor 成功 | Corroborated，Value 可为空 | 实际成功快照 | nil |
| 独立 anchor / 组合读取的建立失败 | Unknown，Value=nil | 有多少保留多少 | 建立失败原因 |
| 组合读取 reference 成功，但业务未形成裁决就失败 | Unknown，Value=nil | 保留 Corroborated | 实际失败原因 |
| 主业务聚合失败 | Unknown，公开 Value=nil | 实际快照 | 聚合分类错误 |
| 聚合确认收据 null | Corroborated，保留 JSON null | 如适用则保留 | ethereum.NotFound |
| 协证合约 revert | 保留实际聚合决策与数据 | 如适用则保留 | RevertError |
| 聚合成功，typed 解码 / 绑定失败 | 保留已产生的聚合事实 | 如适用则保留 | typed 分类错误 |
| 选择型方法成功 | Selected | 按方法 | nil |
| 显式 Forwarded 配置 / pending-local | 保留现有实际分类与豁免 | 按方法 | 按既定规则 |

前置 reference 的支持者不能冒充业务响应支持者。独立 reference 操作的顶层决策
明确表示查询本身；组合读取顶层只表示业务阶段。取消和关闭同样遵守这一区分。

成功 JSON 不一概承诺 canonical，遵守方法自己的结果规则。仅在主聚合失败时，
外部 CallResult 的 Decision.Value 清空；不得因为最终 error 非 nil 清空协证 null、
revert 或 typed 失败前已有的成功聚合载荷。内部 resolver 如仍需候选值可保留，
但不可通过公开失败结果泄露为成功载荷。不要清空 Support / Reject 等诊断事实。

原始响应不能转成 error 文本塞进 Faults 绕过材料预算。不开启原始材料时不额外
保留全部冲突响应。已有受方法契约定义的结构化诊断、revert 数据不因该开关被删除。
最终 error 统一来源，保留 errors.Is / As；移除外层“有 error 就强制 Unknown”的旧规则。

旧 `rpc.Decision.IsCorroborated(err)` 不再作为新的公开成功判定入口，删除该带 error
的 helper；其少量现有调用 / 测试改为明确检查聚合 error 与 Status，不能删掉原来的
失败检查。公开业务使用 CallResult.Err 与无参 IsCorroborated 分别判断两层事实。
同步修订 rpc.Status 注释与对应决策适用范围，不能保留“所有层次失败都必须 Unknown”
这种与新模型冲突的描述。

### 3.3 Call 路由

Call 是根包的高级动态读取操作，只接受已明确注册为读取且可执行的方法。
检查在任何网络调用之前完成：写入、未知、未实现、订阅方法一律 ErrInvalidRequest；
对 eth_sendRawTransaction 提示使用 SendRawTransaction。不能只写一个方法黑名单。

允许列表来自现有 catalog / ReadPlan 的唯一声明，新增方法时更新该事实源，不能另建
与其漂移的手写列表。声明为读取但计划缺失时失败关闭，不自动 Forward。
已注册读取在显式单源配置中仍可 Forwarded；禁止的是未知方法的隐式绕行。
本轮不新增公开 Forward 接口；底层 pool 的 Forward 仍为合法内部执行能力。

Call 使用和 typed 操作一致的读取接受规则，保留方法自身的选择型、pending-local、
显式 Forwarded 例外；不增加 typed 对象解码。动态 Call 与 typed 操作的业务契约不同，
不得为了复用实现把 Call 伪装为完整 typed 安全入口。

### 3.4 原始材料配置与预算

WithResult 默认不启用原始材料。WithSourceEvidence 只把不可变采集配置放入派生 ctx：
budgetBytes 必须 >0，否则返回原 ctx 与包裹 ErrInvalidRequest 的错误；不启动网络。
同一派生链最后一次配置覆盖前值。每次公开调用独立使用预算，内部 reference、业务、
准入依赖与广播材料共享此次预算；不同调用不共享已消耗计数。

启用后即使零响应，也返回非 nil、空的 UncertainResult；未启用为 nil。只启用材料
但使用官方视图且未挂 capture 时，材料可被省略以避免无消费者采集，不能改变业务。
原生始终有显式结果消费者；需要视图材料的调用必须使用 WithResult。

固定首版保留规则：总材料字节预算由参数指定；每条原始字节最多 64 KiB；整个调用
最多 256 条材料记录（Raw 与 Fields 合计）；每项可变元数据文本最多 4 KiB。
按实际保留的原始字节与可变文本 UTF-8 字节记 Bytes，重复保留分别计费，不能超过
Limit。记录数限制另行约束结构开销；不把 Bytes 描述成 Go heap 的精确占用量。
Dropped 按未完整保留的逻辑条目计数，Truncated 表示发生任何截断 / 丢弃；逐条标记
适用于被部分保留的原始响应。记录条数到限后只更新计数，不继续追加空记录。

预算在复制 / 建立附加材料之前检查；耗尽后停止额外保存与字段展开，业务收集继续。
Fields 只展开已保留 raw 前缀内的完整成员，截断或畸形尾部保留在 Raw；不扫描其后原始载荷。
每个成员扫描前检查容量；字段名按剩余元数据额度限制扫描与解码（JSON 转义最多六倍线长），
超长字段名停止展开并报告丢弃。获准保留的元数据须独立复制，不能用短字符串视图滞留大对象。
不得为了提取 Fields 把无限大小对象先完整复制成 map；已有 transport 响应上限继续
生效，采集解析要有条数 / 大小界限，不以结果截断代替过程约束。

Transport 响应体 / 消息上限独立于证据预算，超限仍走正常业务错误；证据预算耗尽
绝不能取消来源请求、提前停止 quorum 收集、截断业务载荷或改变投票基数。

### 3.5 快照与请求材料

对已知类型编写显式的递归 Clone：Reference、Value、Provenance、Support / Reject、
Extensions 的嵌套值与来源、Diagnostics 的冲突数组、Dimensions、写入 Rejection.Data、
Uncertain 和其余拥有的可变数据。不能只复制顶层 slice，不能用 reflect 深拷贝。
显式结果、capture 内部快照、每次 Result 返回副本之间互不共享可变材料。

内部可执行 `rpc.Request{Params []any}` 保持用途。公开 Decision.Prepared 改为只保存
请求事实的 `*rpc.PreparedRequest`，声明为：

```go
type PreparedRequest struct {
    Method string
    Params json.RawMessage // 准备完成的参数 JSON 数组；独立保有的字节
}
```

快照来自实际准备后的参数（包括 reference 改写），不能由事后重建或用户原对象引用
产生。对自定义 Marshaler 必须先冻结为一次受控序列化结果，业务发送与记录使用等价
事实，不能记录一次、发送时再次执行用户 MarshalJSON 得到不同内容。编码失败作为
参数错误，在业务扇出前返回；不得保留不能隔离的 any 作为兜底。缺席语义仍保留：
没有完成 Prepare 时 Prepared=nil。相关测试、replay 与报告消费者迁移为解析 Params JSON。

error 接口不做通用克隆：保留 errors.Is / As，规定发布后错误对象不可变。库拥有的
含可变数据错误（例如 RevertError）在发布前复制 / 冻结其数据，保证生产者不再修改。
错误链中外部提供对象遵守同一不可变要求，不能承诺任意用户 error 可被隔离。

### 3.6 Close 与取消

根 Client 拥有生命周期取消信号。所有公开业务操作通过共同入口同时受用户 ctx 与
owner 生命周期控制；Close 原子标记关闭、触发 owner 取消、关闭共享池，阻止新调用。
复用现有 ErrPoolClosed（pool.ErrClosed），不新增不相容的 ErrClientClosed。
新调用在触网前返回 ErrPoolClosed；带有效 capture 时仍发布该完成结果。

在途库内网络操作收到 owner 取消，不能只依赖用户传入的 Background ctx；HTTP、WSS、
reference、读与广播都要覆盖。使用取消 cause 或等价显式原因映射，用户取消 / deadline
若先发生则保留 context 错误，owner 关闭先发生则可识别 ErrPoolClosed。已经完成的
业务结果不被事后改写；失败返回尽量保留此前已形成的证据。

Close 等待库自己的后台任务退出，保留 pool 对 tip sampler 的承诺，不等待业务调用方
后续处理；在途调用被取消后自行返回。并发 Close 共享一个关闭完成信号，返回时均满足
相同的资源 / 后台任务关闭条件。不得持锁等待 RPC，不为使用 sync.Once 而重写已正确
的池实现。不得宣称不可中断的外部回调可以被强制终止。

## 4. 十项闭合记录

| 原未闭合项 | 本轮执行结论 | 验收归属 |
| --- | --- | --- |
| 根与视图 | 根原生 + 私有 owner 视图，无零分配承诺 | A01、A02 |
| 官方覆盖 | 21 读 + SendTransaction + Close；bind/v2 两接口 | A01、A02 |
| reference-only | 同一 Decision，区分查询本身和业务前置 | A05 |
| Call | 读取允许列表；拒绝未知与写；公开失败 Value 清空 | A04、A06 |
| CallInto | 删除公开入口，仅复用纯解码 | A02、A14 |
| capture 重用 | 入口绑定；Result 三返回；捕获错误不改业务 | A07、A08 |
| 材料预算 | ctx 统一配置，保留预算与网络上限独立 | A09 |
| 隔离 | 已知图递归 Clone；Prepared JSON；error 不可变 | A10 |
| Close | owner 取消在途网络，统一 ErrPoolClosed | A11 |
| 迁移 | 无 shim；部分 supersede 0065；调用方与文档同步 | A12—A15 |

## 5. 完整执行计划表

每阶段出口都包含 G1（第 7 节）。表中的定向测试包是最低范围，实际改动的消费者
必须一并覆盖。一个阶段内可以有多个可编译小切片，禁止把阶段间破损留给下一阶段。

| 序号 | 依赖 | 实施内容与主要落点 | 阶段自检与完成出口 |
| --- | --- | --- | --- |
| 1 | 无 | 盘点 HEAD / 工作区 / AGENTS、导出方法、旧 API 调用方与测试归属；运行基线；建立第 9 节日志 | 记录基线和既有失败；Go 版本可用；导出清单、21 对方法及写 / anchor / Call 消费者无遗漏；不重复删除不存在的 ethadapter |
| 2 | 1 | 追加正式架构决策，状态 Accepted, implementation pending；部分 supersede 0065，明确动态读取收窄、状态分层、预发布 break | 文档检查；原有 quorum、构造、gethcompat 等未被整条否决；正式依据不依赖 ignored tmp 文件 |
| 3 | 2 | 结果类型、互斥规则、helpers；PreparedRequest JSON 快照；已知结果图 Clone；位于 root / rpc / typed 的正确所有者 | G1；`go test ./rpc/... ./internal/typed/... -race -count=1`；A03、A10；修改嵌套副本不污染原件，Prepared 与发送事实一致；迁移本阶段全部 Prepared 消费者 |
| 4 | 3 | ResultCapture 状态机和统一材料配置 / 保留器，替换仅原始材料 capture 的内部职责；暂不切换全部公开方法 | G1；对应 capture / uncertain 包定向 race；A07—A09；并发入口重用能检测，预算不取消业务；旧 API 如暂留只作过渡，本阶段不宣称切换完成 |
| 5 | 4 | 建立共同公开操作入口、生命周期 ctx、reference 证据显式交付与外层完成发布；内部 helper 不互调公开门面 | G1；root、runtime、aggregator 定向 race + shuffle；A05、A08、A11；anchor 不能抢先完成，Close Background 请求能收敛；不复制裁决链 |
| 6 | 5 | 迁移状态 / 标量方法：ChainID、NetworkID、BalanceAt、NonceAt、StorageAt、CodeAt、GasPrice、TipCap、PeerCount、SyncProgress、FeeHistory、BlobBaseFee | G1；相关 root typed 与 projection 测试；A03、A04、A09；CallResult / Err 一致，Selected 与 Forwarded 语义保留；本组调用方同步迁移 |
| 7 | 6 | 迁移 BlockByHash、BlockByNumber、BlockNumber、BlockReceipts、TransactionReceipt、TransactionByHash 和三个 anchor 方法 | G1；block / receipt / pending / canonical / reference 测试，replay 与 verification 受影响场景；A04、A05、A10；null、hash / height 绑定、负数 tag、组合 reference 证据全保留 |
| 8 | 7 | 迁移 CallContract、EstimateGas、FilterLogs、CreateAccessList；按新规则改 Call，删除公开 CallInto，迁移通用读取和工具消费者 | G1；call/revert、logs、动态路由及 verification 测试；A04、A06；写 / 未知本地零请求，revert data presence 不变，公开失败 Value=nil；不削弱 JSON 层测试覆盖 |
| 9 | 8 | 迁移 SendTransaction / SendRawTransaction 到同一写操作；CallResult.WriteDecision；接入证据与 owner 取消；删除 WriteResult，迁移写消费者 | G1；root write、rpc/write、pool broadcast、HTTP/WSS 写入集成 race；A03、A08、A09、A11、A13；nil tx 本地拒绝，hash 绑定、固定基数、相对信任不变 |
| 10 | 9 | 实现 EthClient 精确方法集合；薄封装共同操作；补 bind/v2 接口和方法集合检查；统一 WithResult 公开用法；删除旧捕获公共面 | G1；root facade 与 API 合约定向 race；A01—A03、A07、A12；23 个导出方法无多余提升，ctx 与原生结果一致，不额外 RPC；无旧捕获 / CallInto / WriteResult 生产声明 |
| 11 | 10 | 全仓库 examples、cmd、replay、verification、README 双语、catalog、方法 cards、topology、roadmap、release migration notes 收敛；更新架构 interlock | G1；全量 build、对应集成 / 工具测试与导出符号核对；A12—A15；所有断言按业务语义迁移，旧历史说明标为 superseded；默认依赖图不引入官方 ethclient 实现 |
| 12 | 11 | 跨切面验收矩阵、完整最终门禁、逐文件 diff 审查、执行记录与完成报告；正式 decision 更新实现状态 | G2 / G3；A01—A15 全部有证据；无未解释失败 / 阻塞；记录最终 HEAD 或 diff 身份和命令，不使用早期测试结果冒充最终结果 |

阶段 6—9 改签名时允许在当前阶段同步添加必要视图薄壳或迁移消费者以保持全仓编译；
阶段 10 的职责是完成精确方法集合和跨面验证，不是要求此前必须不存在视图。
阶段 3—5 新增内部构件也须有实际调用或定向验证，最终必须删除过渡死路径。

## 6. 验收矩阵

| ID | 验收要求 | 必须提供的证据 |
| --- | --- | --- |
| A01 | 官方签名与生态承诺准确 | 21 个读取逐项函数类型检查，SendTransaction/Close 检查；bind/v2.ContractCaller、DeployBackend 编译断言；不宣称完整后端 |
| A02 | 独立方法切面与原生完整性 | EthClient 精确 23 方法集合；无匿名嵌入、Call/Pool/anchor 等泄露；原生 21 方法、写、扩展、anchor、Call 新签名完整；CallInto 不再导出 |
| A03 | 同一操作同一行为 | 固定 fixture 分别运行原生、视图、视图+capture、原生+capture；业务值、错误类、决策材料相同（时间等非确定字段按含义核验）；比较每次独立调用的 RPC 轨迹，不混用一次性 capture |
| A04 | 聚合事实与业务终态分层 | 支持不足、全失败、部分来源失败、malformed、null、revert、typed 绑定失败、Selected、Forwarded、pending-local；失败业务值清零，成功聚合事实不被最终错误擦掉 |
| A05 | reference 归属 | 独立查询成功 / 失败；组合查询 reference 失败、reference 成功后业务失败；顶层与 Reference 状态及支持者正确；只在外层结束发布 |
| A06 | 动态 Call 不能绕行 | 已声明读取按规则接受；未知、写、订阅本地 ErrInvalidRequest 且无任何 upstream 调用；显式单源下注册读取仍允许 Forwarded；不把原始材料塞入 Faults |
| A07 | 重用与并发读取 | 正常前 / 中 / 后读取；执行中与完成后二次绑定；并发两调用只有一个首次绑定；后续读取 ErrCaptureReused；两次业务均按正常契约执行；race 通过 |
| A08 | 发布完整性 | 参数本地拒绝、已取消 ctx、调用中取消、Close、panic；正常返回前已发布且仅一次；panic 传播，capture 异常不伪成功；嵌套 helper 不覆盖外层 |
| A09 | 预算独立且有界 | 默认无材料、启用空结果、超小 / 恰好 / 超额预算、条数上限、超长错误和字段；启用 / 不启用业务 RPC 轨迹与裁决一致；无耗尽后的空记录增长；采集不修改 transport 限额 |
| A10 | 已知对象隔离 | 原生返回、capture 保存、两次 Result 副本逐层变更 Reference/Extensions/Diagnostics/Dimensions/Raw/写 Rejection 等不互相污染；Prepared 不引用用户 map；自定义 Marshaler 记录与发送一致；错误 Is/As 保留 |
| A11 | 生命周期 | HTTP/WSS、读/reference/写的 Background 请求在 owner Close 后可收敛；新调用零网络且 ErrPoolClosed；并发/重复 Close、两个视图、关闭后取视图；用户取消优先原因和后台任务退出 |
| A12 | 单实现、无兼容残留 | 公开 facade 无第二份 typed 解码/绑定/业务接受规则；没有 ethadapter 包、旧 capture 公共声明、旧 WriteResult、CallInto；允许历史 decision 文字明确保留 |
| A13 | 广播契约 | nil tx、非法 bytes、接受/分歧/拒绝/不可达、错误 hash、一源模式、预算与取消；固定基数和本地 hash 绑定不变；通过视图 capture 能取同次写决策 |
| A14 | 原测试语义完整迁移 | 每个删除/改写测试登记原意图与新落点；CallInto 测试按动态 JSON 与真实 typed 分开；不能把安全断言改成仅不报错；replay / verification 保留实际业务覆盖 |
| A15 | 文档、门禁、依赖 | README 中英与示例覆盖原生、视图、WithResult、材料预算和关闭；完整 G2/G3 结果；正式决策及导出差异；默认构建不引入官方 ethclient 或改变 gethcompat 归属 |

与三种交付有关的新增测试聚焦交付契约，不复制整个 resolver 测试集。
不要写纯实现镜像测试。取消与并发优先用可控阻塞点 / 同步信号 / 既有 synctest，
不用长 sleep 假装竞态覆盖；测试必须有超时上界，避免整个门禁挂死。

## 7. 每阶段与最终自检命令

### G0：执行前基线

```bash
git status --short
git rev-parse HEAD
go version
go doc -all .
go test ./... -count=1
```

保存输出摘要与导出基线。已有失败必须定位到是否与改动相关，不删除或跳过来制造
基线通过。工具下载失败与断言失败分开记录。

### G1：每阶段自检

```bash
gofmt -l .
git diff --check
go vet ./...
go build ./...
```

gofmt -l 无输出才算通过；必要时先格式化修改的 Go 文件。每阶段另运行表中触及包
的 `go test <packages> -race -count=1`。涉及 capture、生命周期、pool、reference、
aggregator、transport 并发时，用 `-race -count=2 -shuffle=on` 替代该次定向测试，
记录失败种子。新失败修复后重跑相关检查；无变更且已通过的检查不无意义重复。

每阶段还必须人工审查 diff：行为是否超范围、是否误删原测试意图、是否让 facade
增加业务知识、是否将 evidence 错误当成业务错误。更新第 9 节再继续。

### G2：最终完整自检

```bash
gofmt -l .
git diff --check
go mod tidy -diff
go vet ./...
go build ./...
go test ./... -count=1
go test ./... -race -count=1
go test . -run 'TestArchitecture|TestPackage|TestDefaultBuild' -count=1
go test ./internal/verification/integration -run '^TestVerification' -race -count=2 -shuffle=on
go test ./gethcompat/... ./internal/transport/geth/... ./internal/transport/http/... -race -count=1
go list ./...
go doc -all .
```

所有检查在最终代码上执行。新增精准 API 合约与 capture 测试需纳入全量套件，不能
只存在于临时脚本。对比导出前后清单，逐条核对允许的删除 / 新增。
`go list` 中无 ethadapter；默认依赖 interlock 保持，不能通过放开整个白名单消除失败。

### G3：发布门禁与差异审查

```bash
./scripts/release_check.sh
git diff --stat
git diff --check
git status --short
```

当前 release_check 包含格式、tidy、vet、build、race、govulncheck；实际代码没有
“脏工作区拒绝”检查，不能套用旧临时计划要求先提交。执行时若脚本已改变则按实际
契约处理，不修改脚本绕门禁。未提交最终改动不等于验收失败。

govulncheck 会安装锁定版本工具并可能访问网络。环境无法下载时如实记录 G3 被阻塞，
保留 G2 结果，不把它写成全通过；继续完成所有独立工作。不要升级依赖或关掉扫描
来回避无关基础设施问题。API break 是授权范围，但 release CI 的 apidiff 仍需盘点
其预发布处理，提供变更说明；本计划不授权发布，也不声称本地通过等于 tag 必过。

不要求真实 RPC canary 作为本轮结构修改的必过门，不使用付费 / 用户节点提交交易。
涉及 transport 改动时运行现有本地模拟与对应 fuzz smoke（采用 CI 已有目标），不
将线上探测结果伪作离线可重复验收。

最后逐文件审查新增和未跟踪文件（git diff 不包含后者），核对没有构建产物、临时
候选实现、阶段命名文件、秘密或与任务无关变更。

## 8. 测试与消费者迁移特别要求

- root typed_* 测试：仅因返回值变化调整接收变量，不弱化 hash、height、pending、
  reference、错误分类或 null 断言；新增 CallResult 的可观察事实验证。
- verification / replay：通用 JSON 测试可通过 Call.Decision.Value 解析 fixture；真实
  typed 安全测试必须调用共同 typed 操作，不用测试私有 decoder 假装覆盖公开契约。
- 当前 CallInto 的解码失败测试要分清纯 JSON decoder 测试与 typed 最终错误测试，
  原有测试编号 / 覆盖登记同步，不用删除场景绕开 inventory interlock。
- Prepared 参数消费者、golden、报告工具：迁移为 JSON 请求快照，保持实际 prepared
  请求事实；仅因载体变化调整 golden，不能重录丢失证据的输出当作新基线。
- 各源 raw capture 不与聚合结果 clone 混为一谈；前者受预算，后者保留结果契约所需
  的正常业务说明。不得为了默认不开采集，删除标准裁决本来必须有的字段。
- examples 至少展示：原生余额与失败证据、ethclient 普通调用、WithResult 捕获、
  WithSourceEvidence 有界材料、官方 SendTransaction + 写结果捕获、共享 Close。
- 旧公开 API 的替换不影响低层 resolver 仍可内部保留诊断候选；外部结果的清理在一个
  明确所有者处完成，不能在每个 facade 各自实现。

## 9. 执行记录与最终报告

执行 agent 直接维护下表；不建立 phase-N.md 等文件。长日志放临时目录或既有日志
位置，在表中记录命令、结果、日期和关键定位，不只写“测试通过”。

| 阶段 | 状态 | 修改与契约证据 | 自检命令 / 结果 | 未完成项 |
| --- | --- | --- | --- | --- |
| 1 基线盘点 | 完成（2026-11-06） | HEAD=31e6898d0316fc9eef83b8a62c6be4e2f7b57950，工作区仅含本计划文档（未跟踪）。Go go1.26.10 linux/amd64，go-ethereum v1.17.5（accounts/abi/bind/v2 存在，ContractCaller=CodeAt+CallContract、DeployBackend=TransactionReceipt+CodeAt）。根导出 21 读取方法与计划矩阵逐一对应（ChainID…BlobBaseFee）；写 SendTransaction/SendRawTransaction 返回 WriteResult；扩展 CreateAccessList、3 个 anchor、Call(rpc.Decision)；CallInto/WithUncertain/UncertainCapture/WriteResult/IsCorroborated(err) 待删除。待迁移消费者：CallInto→typed_state_wire_test、verification{integration,method_scenarios,typed_projection}_test；WriteResult→verification write_test；WithUncertain→verification integration_test；Decision.Prepared(*rpc.Request)→aggregator/invocation.go preparedRequest、aggregator/read_plan_test、verification{method_scenarios,field_lifecycle,scenario_harness}_test；IsCorroborated(err)→rpc/decision_test、aggregator/aggregator_test。无 ethadapter 包（48d62dd 已删）；gethcompat 为保留 transport seam。interlock：root 允许 import gethrpc，仅 internal/transport/geth 允许 import ethclient。导出基线快照存 /tmp/ethguard-doc-baseline.txt，测试日志 /tmp/ethguard-baseline-test.log | `go test ./... -count=1`：42 包 ok，0 FAIL；`go doc -all .` 545 行已保存。G0 完成 | 无 |
| 2 正式记录 | 完成（2026-11-06） | decisions.md 追加 0066（Accepted, implementation pending）：部分 supersede 0065；根原生+私有 owner 视图、CallResult 两层事实、单执行归属、材料预算、隔离、生命周期、Call 读取收窄、CallInto/WithUncertain 删除；quorum/构造/gethcompat 未被否决；依据仅引用本执行计划，不依赖 ignored tmp 文件 | 文档人工核对通过 | 无 |
| 3 结果与快照 | 完成（2026-11-06） | 新增 rpc/prepared.go（PreparedRequest{Method,Params json.RawMessage}+FreezeParams 受控单次序列化，编码失败包 ErrInvalidRequest）、rpc/clone.go（Decision/WriteDecision/ReferenceEvidence/ExtensionSet/FieldDiagnostics/DimensionEvidence/RPCError/SourceAnswer/RevertContext/PreparedRequest 显式递归 Clone）；Decision.Prepared 改 *PreparedRequest；rpc.Status 注释改为新两层事实模型；删除 Decision.IsCorroborated(err)；aggregator prepare 冻结参数（wireParams 发送、preparedJSON 记录、prepared 保留 Go 值供 plan 查询/admission）；根包新增 call_result.go（CallResult+IsCorroborated/IsWriteAccepted）、errors.go 增 ErrCaptureReused/ErrCaptureAborted。消费者迁移：livecanary run/evidence+tests、rpc/decision_test、aggregator/aggregator_test（显式 rpc.Unknown 断言）、read_plan_test（JSON 解析）、verification scenario_harness/method_scenarios。新测试 rpc/clone_test.go、rpc/prepared_test.go、aggregator/prepared_wire_test.go（wire 事实=快照字节、自定义 Marshaler 单次执行） | G1：gofmt -l 无输出、git diff --check 干净、go vet ./... 、go build ./... 全过；`go test ./rpc/... ./internal/typed/... ./aggregator/... -race -count=1` 12 包 ok；verification+livecanary 全过 | 无 |
| 4 捕获与材料 | 完成（2026-11-06；复审修复轮更正） | internal/uncertain 重写为统一材料配置/保留器：Config{BudgetBytes} 经 ctx 携带（WithConfig/ConfigFromContext，链上最后一次生效）；WithCollector/CollectorFromContext 承载每次调用的 collector；保留规则实现（总预算、单条 64KiB、Raw+Fields 合计 256 条、可变元数据 4KiB、Bytes=保留原始字节+元数据文本、重复保留分别计费、到限只计 dropped 不追加空记录）；字段展开改为 json.Decoder 有界逐值 walk（预算/条数到限即停，不整对象复制成 map，transport 32MiB 上限独立不变）；retain 单一归属 dropped 计数。根包新增 result_capture.go：ResultCapture 五态锁保护状态机（unbound/running/completed/invalidated/aborted）、bind 并发原子占用、publish 入口克隆、Result 三返回（未发布零/false/nil；重用 ErrCaptureReused 永久优先；异常 ErrCaptureAborted）、CallResult.Clone；WithResult、WithSourceEvidence（budget<=0 返回原 ctx+ErrInvalidRequest 包裹错误，不触网）。aggregator 每调用解析 collector（entry 安装优先，否则按 config 新建）；WithUncertain 过渡 shim 改挂统一 collector（DefaultBudget=1MiB），老公共面暂留待阶段 10 删除。新测试：internal/uncertain/capture_test.go（隔离/上限/条数/元数据/预算恰耗尽/config 语义）、result_capture_test.go（发布隔离、永久重用、运行中可读、并发绑定恰好一次、nil/零值、互斥谓词、预算校验）。修复 publish 未克隆导致的材料别名 | G1 全过；`go test ./internal/uncertain/ -race -count=2 -shuffle=on` ok；根包 `-race -count=2 -shuffle=on` ok；全量 ./... 42 包 ok | WithUncertain 双调用共享一个 collector（过渡 shim 与老语义的偏差，阶段 10 随删除消失）。**复审修复（2026-11-06）**：首轮实现的记账有缺陷——来源/错误文本字节在载荷保留后无条件追加（1 字节预算实测 Bytes=2>Limit=1）、Stage/Method 未计费、Fields 重复保留的来源文本未重复计费；截断不报告（8192 字节错误截到 4096 仍 Truncated=false）；预算耗尽后仍用 countRemainingKeys 扫描全部弃置内容且先解码完整字段值。修复：元数据（stage/method/source/error）先于载荷计费，元数据放不进剩余预算则整条丢弃，Bytes 永不超过 Limit，字段记录重复计费自身元数据；metadataText/metadataRef 返回截断事实并计一次 dropped（一条目至多一次，retain 不再自行计数）；字段展开改为无分配的字节区间扫描器（scanObjectMembers，key 去引号、malformed 即停），耗尽即停且弃置尾部只计一次。回归：TestCollectorTinyBudgetDropsWholeEntry、TestCollectorFieldWalkStopsAtExhaustion、TestCollectorFieldScanIsBoundedAndFaithful、元数据截断断言更新 |
| 5 执行与生命周期 | 完成（2026-11-06） | 新增 execution.go：execution 状态（capture 先绑定后校验、ctx 合并 user+owner、AfterFunc 挂 owner 取消、per-call collector 消费者规则：原生必采/视图无 capture 省略/视图有 capture 采集）、operation/writeOperation 泛型入口（closedGate 触网前拒绝、mapLifecycleError 以 context.WithCancelCause 映射 owner 关闭→errors.Join(ErrPoolClosed, ctx err)、用户取消保留 context 错误、panic 传播+capture 标记 aborted、finish 单次发布并回填 Uncertain）、establishAnchor 私有 anchor 执行（显式交付 ReferenceEvidence）。client.go：lifeCtx/cancelLife/closeOnce/closed 字段，New 建 lifecycle，Close 原子标记+owner 取消+共享 pool 完成信号。anchor.go：三个公开 anchor 改走 operation（tag 校验移入体内使本地拒绝也发布），anchorResult 组装独立查询决策（顶层=查询本身，Reference 携带证据，不复制裁决链）。eth.go：BlockNumber/BlockByNumber/blockByNegativeNumber 内部锚定改调 establishAnchor（不穿越公开门面）。采集适配器下沉 internal/uncertain（Capture.InvocationCapture），aggregator 删除本地 adapter，runtime facade 建立轮经 facadeInvocation 挂接 collector（anchor 建立材料可交付）。新测试：client_lifecycle_test.go（发布一次+材料、本地拒绝发布、Background 在途读 owner Close 收敛、关闭零网络拒绝+发布、用户取消保留原因、并发 Close 共享完成——复审发现其完成信号不覆盖 transport 关闭，修复落在 pool.Close 并由 close_completion_test.go 锁定，见阶段 12）、execution_internal_test.go（子操作不触 capture、panic 传播+aborted、业务失败正常发布、重用时第二次业务照常、材料三规则）。修复：finish 按值丢 Uncertain、facade 建立轮无 collector、tag 校验先于绑定 | G1 全过；root/runtime/aggregator/uncertain `-race -count=2 -shuffle=on` ok；全量 42 包 ok | 无 |
| 6 状态与标量 | 完成（2026-11-06） | eth.go：12 个状态/标量方法迁入 operation 入口并带 CallResult（ChainID/NetworkID/BalanceAt→(*big.Int,CallResult,error)、NonceAt/PeerCount→(uint64,…)、StorageAt/CodeAt→([]byte,…)、SuggestGasPrice/SuggestGasTipCap/BlobBaseFee→readQuantity、SyncProgress/FeeHistory→typed 投影保留聚合事实）；aggregateInto 替代 typedInto（聚合失败清 Value、解码失败保留）、decisionOf、readQuantity；校验移入体内（本地拒绝 Decision=nil）。消费者同步迁移：examples/basic、examples/migration、cmd/soak、dial_test、typed_{reads,state_wire,pending_wire}_test、verification{integration,typed_projection,voting,field_lifecycle}_test。新增 call_result_facts_test.go：Err=尾错误、聚合失败清值保诊断（Support 保留）、Selected/Forwarded 语义、本地拒绝无 Decision、材料随结果交付 | G1 全过；root+verification `-race` ok；全量 42 包 ok | 无 |
| 7 区块与 reference | 完成（2026-11-06） | anchor 三方法签名加 CallResult（AnchorBlockHash(ForTag)→(string,CallResult,error)、AnchorBlockHeight→(uint64,…)），anchorResult 组装独立查询决策（成功=Corroborated+Reference 证据、失败=Unknown+证据保留、无 Value）；anchorReference 把建立失败证据带给组合读取。eth.go：BlockByHash/BlockByNumber→(*types.Block,CallResult,error)、BlockNumber→(uint64,…)、BlockReceipts→([]*types.Receipt,…)、TransactionReceipt→(*types.Receipt,…)、TransactionByHash→(*types.Transaction,bool,CallResult,error)（经新增 operationPair 双业务值入口，复用 operation 单一规则）；blockAt 返回 (block,decision,error)，聚合失败清 Value、typed 绑定/投影失败保留事实；全部校验移入体内。消费者迁移：cmd/soak、internal/livecanary/run、examples/migration、client_lifecycle_test、typed_{block_number,state_wire,reads,receipt_canonical,block_receipts_pending,pending_wire}_test、verification{anchor_tag,anchor_transport,content_boundary,integration,return_value_acceptance,typed_projection,voting,field_lifecycle}_test。新增结果事实测试：null 收据保留 Value+Support 且 NotFound、anchor 成功/失败证据、本地拒绝等。过程记录：一次过宽脚本与一次 git checkout 误回滚造成迁移噪声，已全部修复并复核（vet/测试全绿）；transport 自身 ChainID 同名方法未被改动 | G1 全过；root `-race -count=2 -shuffle=on` ok；verification `-race` ok；全量 42 包 ok | 聚合后 typed 绑定失败保留事实的行为由 blockAt/aggregateInto 实现并由代码审查覆盖：准入层在聚合前即拒绝请求-应答不匹配，公开面可达的保留形态为收据 null（已测）与 revert（阶段 8 测）。**复审修复（2026-11-06）**：组合读取（BlockByNumber(nil)、负数 tag、BlockNumber）在 anchor 成功建立后仅传 hash 给业务读取，业务失败时 Decision.Reference=nil——违反 A05。修复：attachEstablishedReference 把已建立证据附回业务决策（成功与失败路径都附），顶层状态保持业务阶段语义、reference 支持者不进入业务来源表。回归：TestCallResultCompositeReadKeepsEstablishedReferenceOnBusinessFailure、TestCallResultCompositeReadCarriesReferenceOnSuccess |
| 8 调用与路由 | 完成（2026-11-06） | call.go 重写：Call(ctx,method,params...)→(CallResult,error) 经 operation，checkCallRoute 以 c.plans（catalog/ReadPlan 唯一事实源）做触网前路由检查——未注册/未实现/订阅一律 ErrInvalidRequest（eth_sendRawTransaction 附 SendRawTransaction 指引），计划缺失即失败关闭，注册读取保留自身 Selected/pending/显式单源 Forwarded 语义；成功 JSON 在 Decision.Value，聚合失败清公开 Value；删除公开 CallInto 与 unknownDecision（Decode* 委托保留）。eth.go：CallContract/EstimateGas/FilterLogs/CreateAccessList 迁入 operation 带 CallResult。消费者：cmd/soak（classify/canonicalQuantity/detail 系列改 nilable Decision）、internal/livecanary/run；测试：root 各 typed 文件经 callDecision 适配（91+24 处机械转换+语义修复）、integration 包新增 callDecision/callIntoJSON helper，CallInto 解码失败测试按 §8 分层重归属（jsonsafe 纯解码层），typed_projection 的 9 处 CallInto 改走 Call+JSON 解码，method_scenarios Note 更新，冲突行断言迁移为公开清值契约（scenario_harness/protected_mutations/voting/method_scenarios：聚合失败 Value=nil，NotFound 的 null 保留）。新增 call_route_test.go（写/订阅/未知零上游、单源注册读取仍 Forwarded） | G1 全过；root+verification `-race` ok；全量 42 包 ok；无生产 CallInto 残留 | 无 |
| 9 广播写入 | 完成（2026-11-06） | write.go 重写：SendRawTransaction/SendTransaction→(CallResult,error) 经 writeOperation（同一写实现 sendRawTransaction 私有共享，nil tx/空 bytes 本地拒绝先于 MarshalBinary，修复 nil 解引用 panic）；WriteResult 及 Accepted/Hash helper 删除，写决策在 CallResult.WriteDecision；广播 emissions 经 ctx collector 记录（stage=broadcast，含 collect 错误记录）；owner 取消由合并 ctx 覆盖。消费者迁移：unified_source_write_test、verification write_test（result.WriteDecision 解引用、IsWriteAccepted）。新增结果事实测试：写决策到 capture（含广播材料交付、固定基数 3）、nil tx 本地拒绝+发布+零上游 | G1 全过；root+verification `-race -count=2 -shuffle=on` ok；全量 42 包 ok | 无 |
| 10 官方视图 | 完成（2026-11-06） | 新增 ethclient_view.go：EthClient{owner *Client 私有命名}，EthClient() 不联网不建池；精确 23 方法（21 官方签名读取+SendTransaction(ctx,tx) error+Close），Close 与 owner 共享生命周期；视图经 operation(view=true)/writeOperation(view=true) 走同一共享 exec（eth.go 各方法抽取 *Exec 私有实现：readQuantityExec、balanceAtExec、nonceAtExec、storageAtExec、codeAtExec、blockByNumberExec、blockNumberExec、blockReceiptsExec、transactionReceiptExec、transactionByHashExec、peerCountExec、syncProgressExec、feeHistoryExec、callContractExec、estimateGasExec、filterLogsExec），writeOperation 增加 view 参数；视图无 capture 时省略材料采集。删除旧捕获公共面：WithUncertain/UncertainCapture/Snapshot/Uncertain 别名删除，uncertain.go 只留 UncertainResult/Field/Response/FieldsFor；verification 消费者迁移为 WithResult+WithSourceEvidence。新增 ethclient_view_test.go：bind/v2 ContractCaller/DeployBackend 编译断言、反射精确 23 方法集合检查（无泄露、owner 未导出）、原生/视图同 fixture 同值同错、视图零额外 RPC 轨迹（锚定+业务各一轮）、视图 Close 停 owner、视图+capture 材料交付。修复 cloneUncertainResult 自拷贝 bug（make 后 copy 自身导致 Raw 全零——正是计划禁止的顶层切片假隔离；isolation 测试补 Raw 内容断言）。go.mod/go.sum 因测试导入 bind/v2 由 go mod tidy 更新 | G1 全过；全量 42 包 ok | 无 |
| 11 全仓收敛 | 完成（2026-11-06） | examples/basic 重写（原生读+失败证据、anchor 结果、Call 拒写演示）；examples/migration 重写（视图调用、WithResult 捕获、WithSourceEvidence 有界材料、SendTransaction+写捕获、共享 Close）；README.md 与 README.zh-CN.md：Layout 更新（新文件清单）、双切面四证据层段落、Usage 改 CallResult 签名、WithUncertain 段替换为 WithResult/WithSourceEvidence/预算规则/重用语义/视图与共享 Close；docs/design：method-contracts 0.17 标注 superseded（保留历史）、project-roadmap 历史缺口标注、decision-implementation-index 更新 0021/0034/0038 行并入 0066、return-value-design 加 0066 载体说明、method-integration-cards 的 CreateAccessList 卡片注明不进视图、codebase-topology 根清单更新；architecture_interlock_test.go 的默认构建门新增 bind/v2 不入默认依赖图的断言（A15）。replay/verification/tools 已在阶段 6—9 迁移，无残留旧 API 声明 | G1 全过；全量 42 包 ok；`go test . -run 'TestArchitecture|TestDefaultBuild'` ok | 无 |
| 12 最终验收 | 完成（2026-11-06；复审后复验） | 首轮 G2/G3 全绿；维护者复审以带 race 的反例确认三项计划符合性失败：证据预算可被突破（internal/uncertain/capture.go 记账次序）、组合读取丢失已建立的 reference（eth.go 组合路径仅传 hash）、并发 Close 提前返回（pool.Close 的完成信号不覆盖 transport 关闭），另确认截断不报告与字段展开过程无界两项规范缺陷。修复后复验 G2 全绿：gofmt/diff-check/tidy-diff 干净、vet/build ok、`go test ./... -count=1` 与 `-race -count=1` 42 包 ok、TestArchitecture/TestPackage/TestDefaultBuild ok、verification `-race -count=2 -shuffle=on` ok、gethcompat+transport geth/http `-race` 3 包 ok、`go list ./...` 无 ethadapter；G3 复跑 `./scripts/release_check.sh` exit 0（govulncheck 0 可达漏洞）。导出差异逐条核对（删除 37 项、新增 58 项）全部属于冻结接口；未跟踪文件逐个审查（17 个计划内新文件，含复审修复轮的 pool/close_completion_test.go，examples 构建二进制已删除）；最终 diff 统计 69 文件 +2272/−1154。A05/A09/A11 定向复验：TestCallResultCompositeRead{KeepsEstablishedReferenceOnBusinessFailure,CarriesReferenceOnSuccess}、TestCollector{TinyBudgetDropsWholeEntry,FieldWalkStopsAtExhaustion,FieldScanIsBoundedAndFaithful}、pool TestConcurrentCloseWaitsForResourceClosure 全部通过。首轮记录中『全部完成、无验收欠账』的声明作废，以本行及各阶段修复记录为准；决策 0066 状态更新为 Accepted and implemented。最终 HEAD=31e6898（未提交 diff），全部检查在修复后代码上执行 | 无 |

最终报告必须包含：

1. 最终 HEAD / diff 身份、改动范围和接口迁移摘要。
2. 阶段完成情况与 A01—A15 对应测试位置。
3. G2、G3 实际结果，未执行 / 失败 / 环境阻塞分别列出。
4. 正式 decision 编号及 README / examples 位置。
5. 未解决项；若仍有实现或验收欠账，不能宣布全部完成。

本计划的实施和复验记录见上表；后续复审发现及其关闭证据继续追加，不能用早期测试通过代替最终代码的验收。


### 9.1 二次复审修复及最终复验（2026-11-06）

上一轮“已修复”的预算过程约束仍存在两个反例：预算检查前解码超长字段名，以及
截断字符串通过底层引用滞留完整大对象。本节是本轮最新验收记录；阶段表中旧的
diff 统计、未跟踪文件数及“无”遗留项只代表当时记录，不作为二次复审前已闭合的证据。

| 复审项 | 实际修复 | 持久化验收证据 |
| --- | --- | --- |
| A09：耗尽后仍扫描和分配大 key | Fields 的扫描输入改为已保留 raw 前缀，最多 64 KiB；每个成员扫描前检查预算/条数，key 的线长按剩余额度限定，超长即停；成员值不整段 Decode，只对有界区间检查 JSON 语法；遇到截断或畸形尾部停止，不继续访问后续原始载荷 | `internal/uncertain/retention_bounds_test.go` 的 TestCollectorFieldWalkerChecksCapacityBeforeScanning、TestCollectorFieldExpansionAllocationsAreBounded、TestCollectorExpandsOnlyCompleteRetainedMembers；后者包含截断数字、嵌套畸形值及 UTF-8 字段名 |
| A09：短字符串滞留大对象 | Record 在预算确认且记录确实保留后，独立复制 capped stage、method、SourceRef 文本、error；字段路径在获准保留后复制；短输入也复制，以解除调用者大字符串子串的引用 | TestCollectorMetadataOwnsBoundedStorage：8 MiB 长字符串及其 17 字节子串，检查全部六项元数据内容、记账与底层引用隔离 |
| 元数据截断的逐条报告 | Response.Truncated 同步包含元数据截断事实，而不仅是 raw 截断 | TestCollectorMetadataOwnsBoundedStorage 与既有 TestCollectorMetadataTextCap |
| 文档一致性 | 0066 明确保留单一 runtime owner，公开形态改为双切面；删去本计划末尾“未开始”陈述；§3.4 写明有界字段展开规则 | `docs/decisions.md` 0066；本计划 §3.4、§9.1 |

分配反例使用预先构造的约 6 MiB 转义 key，输入构造不计入测量。修复前在 1 字节预算
耗尽后仍约分配 8,397,200 B/op；修复后的定向测试测得约 232 B/op；raw 已保留 64 KiB
且尚有余量时约 65,760 B/op，主要为实际保留的 raw。回归断言使用宽松固定上界，
不依赖墙钟时间或精确的编译器分配次数。预算指标仍不是精确 Go heap 计量。

最终代码验证：

- `go test ./... -count=1`：42 个测试包通过，日志 `/tmp/ethguard-memory-fix-tests.log`。
- `go test ./... -race -count=1`：42 个测试包通过，日志 `/tmp/ethguard-memory-fix-race.log`。
- `go test ./internal/verification/integration -run '^TestVerification' -race -count=2 -shuffle=on`：通过，日志 `/tmp/ethguard-memory-fix-verification.log`。
- `go test . ./gethcompat/... ./internal/transport/geth/... ./internal/transport/http/... -race -count=1`：通过，包含根包全部 API / 架构 interlock；日志 `/tmp/ethguard-memory-fix-compat.log`。
- `./scripts/release_check.sh`：exit 0，gofmt / tidy-diff / vet / build / race / govulncheck 通过；0 个可达漏洞，另报告 1 个导入包层面的不可达漏洞。日志 `/tmp/ethguard-memory-fix-release.log`。
- `go list ./...` 无 ethadapter；`go doc -all .` 已复核。本次修复只改内部采集实现、测试和文档，没有新增公开签名、依赖或传输行为。`git diff --check` 通过。

本轮新增 `internal/uncertain/retention_bounds_test.go`，旧断言未弱化。后续分析计划写入
`docs/design/tmp/transport-coverage-and-simplification-plan.md`；该目录被仓库 gitignore
忽略，本地文件已交付，未擅自 force-add 或删除历史文件。其未来任务尚未执行。
临时日志用于本次审阅，持久化复现依据是上述仓库回归测试与验收命令。
