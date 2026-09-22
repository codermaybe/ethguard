# T00 交付：规范、兼容与迁移基线

状态：2026-09-07 基线记录；第 6 节迁移方向已按 0035–0037 接受，逐方法细节待落实，真实样本未采集，轻客户端为初审非终审。本文件是规范基线记录；目录和实现边界见 [architecture-record.md](architecture-record.md)。第 3.3、4、5 节的“代码事实”是基线时点的快照，部分已被后续实现取代（如 0019 内容承诺、0020 准入、0021 Uncertain、0028/0029 固定多数与 H=2、0030 单源转发、0036/0037 迁移）——当前状态以 [../decisions.md](../decisions.md) 为准，本文件不再同步改写历史快照。

依据决策：0019–0034（见 [../decisions.md](../decisions.md)）。旧代码与旧方法表是迁移前事实，不是新契约。

## 1. 版本基线（固定）

| 依赖 | 版本锚点 | 固定物 | 用途 |
| --- | --- | --- | --- |
| go-ethereum | v1.17.5（commit 9621c6a，发布 2026-07-27，go.mod 已钉） | ethclient 兼容面、types 解码、hexutil/rpc 语义 | 迁移矩阵与类型兼容的对照基准 |
| execution-apis | 最新 tag v1.0.0-beta.7（2026-06-10） | `src/eth/*.yaml` 方法 schema（编译为 openrpc.json） | 0024 逐方法必要字段依据 |
| consensus-specs | v1.7.0-beta.0（2026-09-03，commit 756f49c） | `specs/altair/light-client/`（Altair 起的 living spec） | 轻客户端规则初审与 T12 复核依据 |

execution-apis 与 consensus-specs 是 living spec，tag 会漂移；本基线以今日 tag 为锚，T12 复核时重新固定。hive 默认追 execution-apis `main`，因此兼容验收必须记录采集当日版本，不能引用“当前”。标签内容存在性已于 2026-09-07 直接核验：consensus-specs v1.7.0-beta.0 含 `specs/altair/light-client/sync-protocol.md` 与 `specs/capella/light-client/sync-protocol.md`；execution-apis v1.0.0-beta.7 含 `src/eth/*.yaml` 方法 schema（block、state、transaction、filter 等）。

## 2. 分叉适用性（内容承诺的生效条件）

0019/0024 的内容承诺按分叉条件生效。主网口径（具体链以其自身分叉表为准，ethguard 不绑定主网）：

| 分叉 | 主网时间 | 对内容承诺的影响 |
| --- | --- | --- |
| pre-Merge | — | 叔块列表可非空；uncle hash = keccak(rlp(完整叔块头列表))——必须逐块取得叔块头并校验（0019） |
| Merge/Paris | 2022-09 | 叔块恒空，uncleRoot 必须等于空常量；difficulty/totalDifficulty 语义废止（部分供应商仍返回 totalDifficulty，按厂商扩展处理） |
| Shanghai/Capella | 2023-04 | withdrawals 字段与 withdrawalsRoot 出现（EIP-4895）；后 Capella 区块两者为必要内容 |
| Cancun/Dencun | 2024-03 | type-3 blob 交易；头字段 blobGasUsed/excessBlobGas；交易根按编码规范计入全部类型 |
| Prague/Pectra | 2025-05 | type-4 EIP-7702 交易（authorizationList） |
| Fusaka/BPO | 2025-12 | blob 参数变更（BPO） |
| Amsterdam | 未上线（Geth 1.17 系列实现中） | 不纳入基线字段 |

分叉条件字段的解码由 Geth v1.17.5 types 层承担；ethguard 的职责是按 0024 在准入时声明“哪些字段在哪些条件下必要”。

## 3. ethclient 兼容面与迁移矩阵

对照 v1.17.5 ethclient 全方法面（`ethclient/ethclient.go`）与 [ethadapter](../../ethadapter/client.go)。

### 3.1 同形支持（签名与 Geth 返回类型一致）

ChainID、BalanceAt、BlockByHash、BlockByNumber、BlockNumber、BlockReceipts（BlockNumberOrHash 形态）、TransactionByHash、TransactionReceipt、NonceAt、StorageAt、CodeAt、CallContract、EstimateGas、FilterLogs、SuggestGasPrice、SuggestGasTipCap、PeerCount、SyncProgress、Close。

### 3.2 ethclient 独有（无类型化入口）

| 方法组 | 缺失原因 / 替代路径 |
| --- | --- |
| HeaderByHash / HeaderByNumber | 替代：BlockByHash + `block.Header()`；代价是拉取完整交易。轻量头读取未设计 |
| Pending* 全族（Balance/Storage/Code/Nonce/TransactionCount/CallContract） | pending 语义按 0032 逐方法待列（见第 6.1 节提案）；当前无入口 |
| BalanceAtHash / StorageAtHash / CodeAtHash / NonceAtHash / CallContractAtHash / EstimateGasAtBlock / EstimateGasAtBlockHash | 策略层内部可锚定，但 hash 形态未暴露类型化入口（根层 Call 可达，参数需手拼） |
| TransactionSender / TransactionCount(块内) / TransactionInBlock | 未设计；TransactionSender 需要签名恢复本地计算 |
| NetworkID / FeeHistory / BlobBaseFee / SimulateV1 | NetworkID 与 FeeHistory 在根层 Client 存在（Unwrap 可达）；BlobBaseFee、SimulateV1 未注册 |
| SubscribeNewHead / SubscribeFilterLogs / SubscribeTransactionReceipts | 订阅通道未设计：pool 仅请求/响应式 CallContext，ws 端点可拨号但无订阅聚合语义 |
| SendTransaction / SendTransactionSync / SendRawTransactionSync | 0003 范围外；Unwrap().Call 转发为 Forwarded，无交易生命周期管理 |

### 3.3 语义差异（方法存在 ≠ 兼容）

| 差异 | ethclient v1.17.5 | ethadapter 现状 | 目标 |
| --- | --- | --- | --- |
| `BlockByNumber(ctx, nil)` | 单 provider 自然头 | 先建立跨源验证参考再按 hash 读取（0018） | 保持，文档说明成本 |
| 负数块参数 | `-1`→pending、`-2`→latest、`-3`→finalized、`-4`→safe、`-5`→earliest（`rpc.BlockNumber.String()`） | `-2`/`-3`/`-4` 按各自标签建立参考再按 hash 读取；`-5` 作为 earliest 的显式高度 `0x0`；未知负数本地拒绝。类型化 `BlockByNumber(-1)` 本地拒绝（不支持 pending block typed projection），不锚定、不发业务请求、不编码为 `-0x1`。策略层 `pending` 标签仍是本地观测，不是类型化区块。状态读取的 `-1` 是 `pending` 字符串，不是 `-0x1`。`eth_getLogs` pending 范围本地拒绝 | 保持上述边界。field-level pending observation 仍未实现。EIP-1898 object 样本已脱敏收录（形状证据，不是兼容性结论）。`eth_estimateGas` / `eth_createAccessList` 的对象形态已与 T02 状态读取对齐 |
| `BlockReceipts` 的 tag | BlockNumberOrHash 携带 tag 字符串 | blockRefArg 正确映射 pending/safe/finalized/latest 标签，但随后被锚定改写（见差距总账 6） | T03 |
| fullTx | Header* 走轻量头（`eth_getBlockByNumber(tag, false)`） | adapter 恒传 true（整对象语义）；fullTx=false 的决策会被类型化解码拒绝 | 保持；Header 轻量形态未设计 |
| 错误面 | 原样上游/传输错误 | 分类哨兵（ErrConflict/ErrNoValidResponse/…），`errors.Is` 可判 | 保持（0009） |

## 4. 已知差距总账（现状 → 决策 → 任务）

| # | 差距（代码事实） | 目标决策 | 任务 |
| --- | --- | --- | --- |
| 1 | byNumber 归一化仅比较 `number\|hash` 头，体不参与跨源比较 | 0019/0024 | T04 |
| 2 | DecodeBlock 不验交易根、withdrawals 根、叔块承诺；只查 wire hash==header hash 与空叔列表 | 0019 | T04 |
| 3 | byNumber 将 null 判无效；byHash 将 null 当可比较值（不对称） | 0022（null 统一为合法回答按门槛裁决） | T04 |
| 4 | 默认 Strict=全一致、单回答 Selected；quorum 为显式选项 | 0028/0029（固定成员多数 + H=2 默认；一致模式为显式模式） | T01/T02 |
| 5 | eligible 动态分母：健康冷却（3 连败/30s）与 tip 过滤改变每次参与集合 | 0027（使用方管理成员）/0025（调用内固定基准） | T01/T03 |
| 6 | 标签改写与等级丢失：`rpc/anchor.go` 的 movingTags{latest,safe,finalized} 一律改写为同一锚定 hash，且锚定函数不接收原标签——latest/safe/finalized 等级丢失，普通历史块的存在即可冒充 safe/finalized 覆盖；负数参数见 3.3 | 0032（保持标签语义）/0036（库统一标签感知参考）/0033 | T03 |
| 7 | 跨调用参考缓存可用（WithAnchorCaching/anchor_cache_ttl） | 0026（退出本轮目标） | T03（迁移，见 6.4） |
| 8 | 调用内快照缺失：Collect 单次固定，参考建立与业务读取不共享分母 | 0025 | T03 |
| 9 | 准入、内容承诺、Uncertain 均未实现 | 0020/0019/0021/0034 | T04/T05 |
| 10 | revert 类回答压成单键（仅比较“是否回滚”） | 0023 | T08 |
| 11 | 单源模式不存在（单源下 Strict 单回答=Selected，类型化读取拒绝） | 0030 | T02 |
| 12 | 429 与其他失败共用连败计数（`rateLimited` 仅单独统计） | 0031（限流单独观测、自适应拥塞） | T11 |
| 13 | `toCallArg` 丢弃 Geth v1.17.5 CallMsg 的 BlobGasFeeCap/BlobHashes/AuthorizationList 编码（`maxFeePerBlobGas`/`blobVersionedHashes`/`authorizationList`） | ethclient 请求参数兼容（T00 复核确认，源码对照） | T08 |
| 14 | `blockRefArg` 返回裸 hash、丢弃 BlockNumberOrHash.RequireCanonical，且不识别 earliest 常量（-5 落入报错分支） | EIP-1898 / Geth 入口兼容（T00 复核确认） | T07（共同参数设施归 T03） |
| 15 | 状态读取的块参数被 WithAnchor 改写为裸 hash 字符串，非 EIP-1898 对适用方法规定的对象形态；soak 中厂商接受裸 hash 不证明标准可移植 | EIP-1898 | T03/T06 |
| 16 | adapter 的 RevertError 仅实现 Error()，decodeCallResult 丢弃 JSON-RPC code；Geth 侧 `RevertErrorData` 依赖 rpc.Error/rpc.DataError 接口 | 0023（code/message/data 原样保留） | T08 |

## 5. 三层校验区分（框架）

| 层 | 职责 | 决定 | 现状 |
| --- | --- | --- | --- |
| 归一化（normalization） | 等效表示归一（0x1/0x01、键序、hex 大小写） | “比较什么” | 已实现（rpc/methods 归一化器） |
| 完整结构校验（admission） | 必要字段/类型/长度/范围/请求绑定/允许 null | “能否投票”（0020） | 零散（DecodeBlock 的 wire-hash、叔块、bloom 检查是局部实例，未成体系、且在裁决后而非投票前） |
| 内容承诺校验（content commitments） | header hash、交易根、withdrawals 根、叔块承诺 | “内容与佐证身份一致”（0019） | 未实现 |

厂商扩展字段不属于任何一层：不参与比较、不制造冲突，按 0021/0024 进 Uncertain。

## 6. 迁移选择（逐项记录裁决状态）

以下为基线记录中的明确迁移选择。标注【提案】的项仍需单独决策；本文件不再维护开发任务状态。

### 6.1 pending 逐方法支持

现状两个入口行为各异：BlockByNumber 改写为已出块，违反 0032；状态族编码为非法 `-0x1`，违反参数兼容要求。

- 【已接受，2026-09-07，决策 0035】逐方法支持 pending：支持的方法保留请求含义，以节点本地观测语义返回，永不因同值晋升 Corroborated；未支持的方法明确报错。不选择“一律拒绝”作为目标行为。
- T03-A（2026-09-22）：`eth_getLogs` 的 pending 范围当前本地拒绝，field-level pending observation 尚未实现。类型化 `eth_getBlockByNumber(-1)` 当前本地拒绝，不把 pending block 提升为普通 block；策略层 `pending` 标签仍是本地观测。这两种拒绝不是 provider 不支持，也不是 Unknown 业务裁决。`Corroborated` 仍只表示 provider-backed cross-source evidence。
- 路由由请求语义决定，不能由 quorum 失败触发；不能改写成已出块读取，也不改变成员／运行模式。类型化入口保留 Geth 签名，执行适用的本地准入。
- 逐方法支持矩阵、来源选择、结果分类可见性与错误契约仍需提交验收，不因方向接受而视为已实现；不自动承诺补齐全部 Pending* 便捷方法。

### 6.2 负数块参数映射（safe/finalized/earliest）

- 【已接受，2026-09-07，决策 0037】ethadapter 的 `toBlockNumArg`/`BlockByNumber` 采用与 ethclient v1.17.5 相同映射：`-1` pending（按 6.1 裁决）、`-2` latest、`-3` finalized、`-4` safe、`-5` earliest（earliest 以显式高度 0 语义处理）。safe/finalized 各自走 0032 的同级参考建立；不足即拒绝。
- 未知或超范围负数明确报参数错误，不得读取其他块；共同转换及 BlockNumberOrHash 等入口按各方法合法形式覆盖。旧“负数=锚定 latest”行为退出，pending 按逐方法支持返回本地观测或明确报错；迁移说明须列出变化。

### 6.3 自定义 resolver（WithAnchorResolver）

- 【已接受，2026-09-07，决策 0036】项目未投产，允许完整迁移：本轮替换旧 AnchorResolver／WithAnchorResolver 扩展面，不保留兼容适配，也不静默忽略注入。库统一负责接收标签、建立共同参考和验证对应证据；迁移仓库调用、测试与示例。
- 自定义 resolver 在大部分计划任务完成后另行评估，不阻塞当前交付，也不影响已接受的可切换裁决模块。该迁移方向已关闭，不能继续按“保留接口、用户负责标签”实施。
- 特性定位为“保持标签语义的多源共同参考与调用内固定”。显式标签已由 Ethereum RPC 定义（[EIP-1898](https://eips.ethereum.org/EIPS/eip-1898)）；多源选取与佐证是本库增加的能力。具体公开 tag 便捷接口未在此定案，标准 ethclient 调用签名保持。

### 6.4 旧缓存配置（WithAnchorCaching / anchor_cache_ttl）

- 【已接受，2026-09-07，决策 0037】本轮禁止实际启用跨调用缓存。正 TTL 在构造期显式报配置错误，指出配置项／选项与决策 0026；未配置或零值表示禁用，非法格式、溢出及负值报错。
- 文件配置与程序选项使用相同边界，不静默忽略启用请求；迁移仓库示例和测试。不限制连接复用与调用内参考复用。
- 自定义 resolver 的完整迁移授权不扩大为任意删除其他接口；未来缓存能力仍需重新评估。

## 7. 轻客户端规则初审

对照 `consensus-specs/specs/altair/light-client/sync-protocol.md`（固定 tag v1.7.0-beta.0，内容 2026-09-07 直接核验；Capella 版 `specs/capella/light-client/sync-protocol.md` 同 tag 已核）。本节两处按 [T00 复核](t00-review.md) 纠错：执行载荷绑定的路径与超时路径的比较。结论分四类：一致 / 不适用（信任模型不同）/ 未证实（能力未建或证据不足）/ 待办（后续任务对齐）。

| 规则域 | 轻客户端要求 | 本设计 | 结论 |
| --- | --- | --- | --- |
| 可信启动 | 一个 `trusted_block_root` 锚点；bootstrap 的头哈希必须等于它，委员会经 Merkle 证明；此后一切密码学可验证 | 无可信根；信任=使用方管理成员（0027）+ 跨源佐证 | 不适用（不同信任模型）。RPC 佐证不能替代可信启动 |
| 签名验证 | BLS `FastAggregateVerify` + 参与位图 | 无任何签名验证；提供者证言 | 未证实（设计边界，非缺陷）：佐证不含密码学证明，须如此表述 |
| 法定人数 | 同步委员会 2/3（权益抽样）；跨更新存在交集性质 | Q=max(H, floor(N/2)+1)，单位票、使用方管理身份（0028） | 不适用：身份≠验证者、无跨操作交集；0028 已声明票权相等不使任意共识协议适用 |
| 最终性 | 验证过 finality 证明才推进；`UPDATE_TIMEOUT` 为有前提的可选恢复路径（见超时降级行） | finalized=来源报告覆盖+候选 hash 一致（0032）；不足即拒绝 | 未证实：非密码学最终性；不足即拒绝。与 LC 恢复路径的信任前提不同，不作保守性比较 |
| 乐观/终局区分 | optimistic（“最近合理安全头”）与 finalized 分离，阈值不同 | 决策状态不区分证据级别；标签语义保留（0032） | 未决：治理文档 7.1 已预留证据强度分级，不在本轮 |
| 执行载荷绑定 | beacon 头经 `body_root` 以 Merkle 分支（gindex 25）绑定 BeaconBlockBody 内的 ExecutionPayloadHeader（Capella 起；pre-Capella 其执行字段为空） | 0019：体承诺对照佐证头（header hash/交易根/withdrawals 根/叔块承诺） | 方向相似但对象不同：CL 侧是 SSZ `hash_tree_root` 承诺，EL 侧是 keccak trie 根，两者不可直接等同；0019 校验 EL 协议编码承诺，证明“内容↔佐证头一致”，不证明头属规范链（无签名链） |
| 新鲜度 | `signature_slot` 不可信，以本地时钟定时效 | 一致性与新鲜度分离（0033），策略缓议 | 未决（已接受缓议）；tip/finalized 过滤现状是待对齐实现事实 |
| 分叉/版本正确性 | 签名域含 fork version | pool 链 ID 预检 + 0024 逐方法分叉条件字段 | 待办：T04+ 字段契约落地后复核 |
| 缺失数据 | 不虚构数据；bootstrap 必须 | 0022：null 合法、不补空对象、不虚构 | 一致 |
| 超时降级 | `UPDATE_TIMEOUT` force-update 是有前提的可选恢复路径：超时、且已存有验证过的最佳更新、且其 finalized slot 不超前，才以 attested 头替代 finalized 推进同步周期；不是“超时即证明最终性” | 佐证不足即拒绝，无对应恢复路径 | 不适用（信任前提不同，不作保守性比较） |

**初审结论**：未发现与轻客户端规则“违反”级冲突——本设计不实现轻客户端路径，各规则域多为不适用/未证实/待办。跨源 RPC 佐证不能替代：可信 checkpoint 启动、BLS 签名验证、同步委员会交集、独立最终性验证、乐观/终局证据区分。对外表述必须维持该边界（治理文档 7.1、0032 已如此声明）。T12 对最终实现复核并重新固定规范版本。

## 8. 真实样本采集与脱敏方案

目的：0023（revert 包装等价）、0019（编码与字段兼容）、0024（字段差异）的兼容证据来源。**当前未采集；本节是方案，不宣称任何兼容已验证。**

- 采集器：新增 `cmd/samples`（复用 soak 的 pool/config 设施）：以一个可信参考给出的高度/hash 为钉点（该钉点仅作采样基准，不进入任何“已验证”声明），对每个上游逐方法发原始 JSON-RPC，落 JSONL 记录：`{provider_label, family, timestamp, method, params, response, http_status, elapsed}`。
- 采样矩阵（每格需至少两个不同厂商族）：
  - 方法 × 参考形态：number / hash / latest / safe / finalized / pending（pending 仅记录厂商行为，不作裁决样本）；
  - 区块类型：空块、含 type-0/1/2/3/4 交易的块、后 Capella withdrawals 块、pre-Merge 含叔块的历史块、大区块（交易根/叔块承诺校验的素材）；
  - revert 样本：`Error(string)`、`Panic(uint256)`、自定义错误、空回滚，以及可被误判的超时/限流错误体；
  - 缺失/裁剪：归档节点裁剪历史、null 与缺字段的差异。
- 脱敏：端点 URL 替换为厂商族 label；API key（URL query 与 header）一律剥离后才落盘；状态读取使用保留地址，不采集账户私有数据；采集日志不落凭据。
- 存放：`docs/samples/`（与设计文档同生命周期，随交接物携带）。
- 诚实标注：现有覆盖仅为 2026-09-05 一轮 soak 的请求形态验证（见 [wire-forms.md](wire-forms.md)）；0019/0023/0024 需要的字段级与错误体样本全部待采集。

## 9. T00 完成状态

| 清单项 | 状态 |
| --- | --- |
| 固定 Geth 依赖、Execution API/链规范版本和分叉 | 完成（第 1、2 节） |
| 方法分类表、现状差距、轻客户端规则初审 | 完成（第 3、4、7 节；初审非终审，T12 复核） |
| 覆盖已注册方法及 adapter 派生方法；不把方法存在等同于完全兼容 | 完成（第 3 节，含语义差异表） |
| 真实最小迁移示例 | 完成（[examples/migration](../../examples/migration)，`go build` 通过；不支持方法与类型限制列于第 3.2/3.3 节） |
| 已知差距清单 | 完成（第 4 节总账，逐项挂决策与任务） |
| 归一化/结构校验/内容承诺区分 | 完成（第 5 节） |
| pending、负数参数、自定义 resolver、旧缓存的迁移选择 | 四项迁移方向均已接受（0035–0037）；逐方法细节及实现验收见第 6 节 |
| 真实 RPC 样本采集与脱敏方案 | 方案完成（第 8 节）；样本未采集，兼容验收保持待验证 |
| T00 补正（2026-09-07） | 完成：复核六项差距并入第 3.3/4 节（总账 13–16）；轻客户端两处纠错（body_root 绑定路径、UPDATE_TIMEOUT 前提）并固定标签内容证据（第 1、7 节）；迁移示例能力声明分列（根层类型化/原始转发/订阅分别说明） |

未覆盖项：样本采集依赖真实端点与凭据，属运行期工作；第 6 节迁移方向均已关闭，6.1 的逐方法细节仍按相关切片处理，其余路径可继续。6.3 的自定义扩展已延后，不再阻塞本轮标签感知参考实现。整体验收补正见 [T00 复核](t00-review.md)。
