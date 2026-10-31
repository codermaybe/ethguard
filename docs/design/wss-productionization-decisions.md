# WSS 生产化推荐决策矩阵

状态：**推荐决策矩阵（2026-10-31），待主维护者裁决；未接入配置或 runtime，
未修改 WSS adapter**。本文件把原先的七个决策输入收敛为可审阅的推荐方案；
推荐不等于已接受决策，任何接线都必须先有明确的主维护者决策记录。

## 0. 结论摘要

- 推荐保持 WSS 显式 opt-in，native HTTP 继续是默认 adapter。
- 第一生产切片只考虑“单条显式配置的 WSS source + 只读调用”；不启用
  reconnect、主动 keepalive 和内部队列，不自动重放任何未知结果。
- **“写路径排除”不能仅由 adapter 没有写方法自动成立**：
  `Client.SendRawTransaction` 通过同一个 `pool.Broadcast` 访问固定成员；
  WSS source 一旦进入该 pool，也会收到 `eth_sendRawTransaction`。在写路由
  机制裁决前，推荐保持 adapter-only，或使用独立只读组合（见 #7）。
- **WSS adapter 的 presence 保真会改变显式 `null` 的生产行为**：raw WSS
  能区分 missing 与 null，而 geth WSS 只能表达 unknown。若 provider 返回
  `data:null`，`eth_call` 的数据维度键会从 `missing` 变为 `null`。在 0056
  未修订前，接线必须显式选择“保留四态”或“降级为 unknown”。
- 因此推荐顺序是：先裁决 #7（写路由）与 presence 行为，再决定是否接线；
  配置 schema 可以先设计，但不能先接入现有 `Client` pool。

## 1. 工作默认

以下默认用于收敛推荐方案，不构成已接受决策：

- WSS 保持显式 opt-in，不改变默认 HTTP；
- 第一生产切片不启用 reconnect、主动 keepalive 和内部队列；
- 不自动重放任何未知结果；
- 写路径继续排除；
- source 选择由 composition root 或显式配置拥有，不由业务方法读取 URL
  scheme。

## 2. 矩阵依据的当前代码事实

- `internal/transport/wss` 实现 `transport.Transport`/`Dialer`，只做 raw
  call、`ChainID` 和 `Close`；没有订阅、batch、自动重连、主动 ping 或写方法。
- `config.Upstream` 只有 `ID` 和 `Endpoint`，没有 transport 字段
  （`config/types.go`）。
- `client.New` 为整个 pool 安装一个 dialer；默认是 native HTTP
  （`HTTPAdapterNative`），Geth compatibility adapter 只经 `gethcompat`
  显式进入（`client.go`）。`pool.Member` 只有 `Name` 和 `URL`。
- `pool.Source` 惰性拨号；`collectOne` 遇到 `transport.ErrConnectionClosed`
  时调用 `discardClient`，下一次独立调用重新拨号；在途调用不会重放
  （`pool/source.go`、`pool/collect.go`）。
- `Client.SendRawTransaction` 使用 `pool.Broadcast` 遍历
  `VotingMembers()` 的固定成员（`write.go`、`pool/broadcast.go`）。因此把
  WSS source 加入该 pool 同时会把它加入写广播。
- pool 只在父 context 未取消时记录 source failure；`ErrClosed`、
  `errWaitingForDial` 和父级取消不惩罚 source；本地调用级超时会记录失败
  （`pool/collect.go` 的 `shouldRecordFailure`）。
- 限流通过 `rpc.RateLimitError` capability 或已知错误码识别；WSS 握手错误
  当前包装为通用 dial error，握手 429 不会自动成为 rate-limit 分类
  （`internal/transport/wss/wss.go`、`pool/monitor.go`）。
- WSS read limit 使当前 pending 得到 `transport.ErrBodyTooLarge` 并关闭连接；
  pool 的 `shouldDiscardClient` 只识别 `ErrConnectionClosed`，因此该 source
  会在下一次调用收到 `ErrConnectionClosed` 时才丢弃并重拨。
- 真实 provider 证据：missing、`0x` 和具体 data 已观察，`null` 未观察；
  一个公开 endpoint 在协议错误后以 `1006` 关闭。见
  [wss-provider-evidence.md](wss-provider-evidence.md)。

## 3. 推荐决策矩阵

| # | 决策 | 当前事实 | 选项 | 语义影响 | 所需证据 | 回滚 | 推荐 | 主维护者裁决 |
| --- | --- | --- | --- | --- | --- | --- | --- | --- |
| 1 | 是否接入 config/runtime | adapter 已实现；config 无 transport 字段；`client.New` 默认安装 native HTTP 单一 dialer；WSS 无生产 importer；W4 `null` 未观察 | (a) 保持 adapter-only；(b) 显式 opt-in source + composition-root 接线，默认不变；(c) 默认切换或自动 fallback | (a) 无生产变化；(b) 只有显式选中的 source 改用 WSS，成员/分母/Decision/reference/typed/method plan 不变；raw presence 会让显式 `null` 可见；写路径需另行排除；(c) 改变默认连接行为，要求完整差分与写语义，不建议 | 主维护者决策记录；W4 `null` 或明确接受“null 仍不可知”/降级 unknown；pool/runtime 等价测试；写路由机制与 presence 行为选择；基础握手/错误/断连观测（已有部分） | 删除配置字段和组合根分支，恢复 native HTTP；单提交；无数据迁移 | (b) 显式 opt-in、默认 HTTP 不变；但在 #7 与 presence 行为裁决前只做配置设计，不接入现有 `Client` pool | 是 |
| 2 | WSS source 如何显式选择 adapter | 一个 pool 一个 dialer；`client.New` 对所有 source 使用同一 adapter；`pool.Member` 只有 Name/URL；配置无 transport 字段 | (a) URL scheme 自动选择；(b) 显式 per-source 字段；(c) 仅组合根注入（自定义 routing dialer / 独立 pool）；(d) 第一切片禁止混合，一个 client 全 HTTP 或全 WSS | (a) URL 拼写变成传输行为，隐式切换且难混合 HTTP/WSS；(b) 可校验、可回滚，业务方法不读 scheme；(c) 不改 config schema，但要求组合根代码和可能的双 client；(d) 最简单但限制部署，且不解决写路径 | 混合成员测试；重复/身份校验；error/health 归属；默认路径不变；配置校验与错误信息 | 删除字段/dispatcher，恢复单一 native dialer | (b) 显式 per-source transport 字段，缺省 native-http；由 composition root 解释，业务方法不读 scheme；需要 pool 层按 source 路由 dialer 的最小 seam | 是（公共配置/API） |
| 3 | reconnect 由谁拥有 | adapter 不重连；断连/读错/写失败 abort pending；pool 在 `ErrConnectionClosed` 后丢弃 transport，下一次调用惰性重拨；无 in-flight replay；真实 `1006` 已观察；geth 自动重连未复制 | (a) pool-owned 惰性下一次调用重拨，无 adapter 后台重连（现状）；(b) adapter 后台重连 + generation；(c) 调用方 retry | (a) 当前调用可因断连失败，下一次独立调用可成功；不恢复未知结果；成员/分母不变；(b) 需要 generation、ID 空间与旧响应隔离，可能改变失败语义；(c) 违反 invocation 内不重试和证据语义 | (a) 进程内断连→pending 失败→下一次重拨回归；真实断连已有 `1006`，重拨延迟与迟到响应未观察；(b) 额外需要真实重连延迟、旧响应、限流和 generation race 证据 | 保持 (a) 无需回滚；(b) 删除重连代码恢复 adapter-only | (a) 保持 pool-owned 惰性重拨，第一切片不启 adapter 后台重连；把“in-flight 断连即失败”记录为可接受限制 | 否（维持现状）；启用 (b) 时是 |
| 4 | keepalive 是否启用 | 不主动 ping；gorilla 仍回应 peer ping；无 idle reaper 或 pong 超时；存活由 reader 和调用 deadline 发现；握手超时 10s | (a) 不启用（现状）；(b) adapter ping/pong loop；(c) pool 层 liveness probes | (a) 半开连接可能在下一次调用才发现；不引入新错误/健康语义；(b) 新 goroutine、超时分类与 Close 顺序；不得生成 RPC error 或 replay；(c) 合成读会进入 health/分母风险 | (a) 真实 idle/半开与 provider 超时行为；进程内关闭顺序测试；(b) 额外参数选择与真实 provider 证据 | 删除 loop，恢复 reader-only 检测 | (a) 不启用主动 keepalive；记录半开连接为可接受限制 | 否（不启用）；启用时是 |
| 5 | backpressure 是否需要队列 | 无独立队列；单 `writeMu`；有 deadline 时作为写 deadline，无 deadline 则清除；pending 随并发增长；pool 有 per-source 调度预算；无真实写阻塞证据 | (a) 无队列（现状）；(b) adapter bounded queue；(c) adapter 立即容量错误；(d) 仅依赖 pool 并发与 collect timeout | (a) 写入串行，pending 可能增长，依赖上层 deadline；(b) 增加等待、排序、取消、Close 清理，不得改变分母或方法优先级；(c) 新错误类别需 health 映射；(d) 不新增 adapter 语义 | 真实写阻塞或 pending 增长；队列上限/等待/取消/关闭/断连清理；race；不得改变分母 | 删除队列，保留 `writeMu` | (a)+(d) 无内部队列，依赖调用 context、pool 调度和 collect timeout；有真实证据再评估 | 否（维持现状）；启用队列或容量错误时是 |
| 6 | Close、断连、限流和 health 归属 | `Close` 等 pending；peer disconnect/read limit/write failure abort pending；pool 记录 source failure；父级取消不记录；本地调用超时记录失败；限流由 capability/已知 code 识别；WSS 握手 429 当前只是通用 dial error；连接状态不进 Decision | (a) 保持现状并明确文档化；(b) 增加 WSS 握手 HTTP status → `rpc.RateLimitError` 映射；(c) 生命周期事件进入 Decision/quorum（否）；(d) adapter 本地 retry/backoff（否） | (a) 握手 429 被计为普通 source failure，不进入 rate-limit health；断连/read limit/本地超时计为 failure；无 Decision 变化；(b) 改变 health/退避分类，需要证据；其他不变；(c)(d) 违反冻结边界 | 真实 429 握手、read limit、response timeout；分类回归；unattempted 不惩罚；reference 与业务并发；Close race | 撤销映射，恢复 neutral 分类 | (a) 第一切片保持 neutral 映射，明确记录握手 429 限制；(b) 作为单独的证据驱动后续决策 | 是（接受限制/映射选择） |
| 7 | 写路径是否永远排除 | adapter 无写方法；但 `Client.SendRawTransaction` 使用 `pool.Broadcast` 遍历固定成员；WSS source 进入 pool 后会收到 `eth_sendRawTransaction`；WSS 写失败/取消 abort 连接且不重放；0062 固定成员/分母；无 per-source read/write 角色 | (a) 写路由机制裁决前保持 adapter-only；(b) pool 增加 read-only source 角色，write 跳过 WSS（改变写 basis/门槛语义，需新决策）；(c) 独立只读组合/独立 Client，写用 HTTP client；(d) 允许 WSS 写，另立 unknown-result/idempotency 契约 | (a) 无变化；(b) 改变写路径固定成员/分母，可能降低有效多数，必须定义被跳过成员的 fault/gated 语义；(c) 每个 client 内成员/分母固定，但应用持有两个证据域；(d) 新写语义，违反冻结边界 | 证明写路径不接触 WSS 的集成测试；若 (b)，新的成员角色/门槛决策与 race 测试；若 (c)，组合根契约；若 (d)，幂等/重放契约 | 移除 WSS source，写路径恢复 HTTP-only | (a) 先保持 adapter-only；若必须接线，优先 (c) 独立只读组合，而不是 (b) 改变写路径固定成员；永不选 (d) | 是（机制） |

## 4. 关键裁决说明

### 4.1 #1 与决策 0056：presence 不是中性变化

raw WSS adapter 实现 `rpc.ErrorDataPresence`，因此 `aggregator.ObservationRPCError`
会把 missing、null、`0x` 和具体 data 分别映射到 `RPCError.DataPresence`；
`RPCError.Raw()` 对 null 保留 `"data":null`，对 missing 省略 data。
geth WSS 没有该 capability，只能落到 `ErrorDataPresenceUnknown`，其
`RPCError.Raw()` 也省略 data。两条路径对 `eth_call` 的差异集中在显式 null：

- missing → `reverted|missing`，两条路径相同；
- `0x`/具体 data → 已有 key，两条路径相同；
- explicit null → raw WSS 产生 `reverted|null`，geth WSS 产生
  `reverted|missing`。

null 与 missing 都使 data 维度弃权，但比较键不同，跨源聚类和 support 可能不同。
因此接线 raw WSS 不是“仅换连接”的无行为变化。推荐在 0056 未修订前，由主维护者
二选一：

1. 接受 raw WSS 的 null 可见性，并把它记录为有限的行为升级（不生成 golden，
   不改 HTTP 四态合同）；
2. 在 production 接线层提供一个显式降级，让 WSS source 对上层继续表现为
   unknown，直到 W4 出口满足。

不要在未选择的情况下直接接线；也不要用“null 没有观察到”推断“null 不会发生”。

### 4.2 #2：per-source 选择需要 pool 层的最小路由

`client.New` 目前只安装一个 pool-wide dialer。显式 per-source transport 不能由
业务方法读取 URL scheme 实现，也不能在上层建第二套 source registry；它需要
composition root 把一个 routing dialer（按 `pool.Source` 身份或 URL 选择 adapter）
交给 pool，或把 pool 的 dialer seam 扩展为 per-source 选择。两种实现都只改变
连接选择，不改变 source 成员、method plan 或 health 归属。

推荐默认值：未配置 transport 的 source 继续使用 native HTTP；配置为 WSS 的
source 必须显式写出 `ws`/`wss` endpoint；混合成员和重复 endpoint 在校验期拒绝
或按现规则去重。

### 4.3 #3：pool 的惰性重拨不是 adapter 自动重连

当前 `Source` 在连接失败后丢弃 transport，下一次调用重新拨号；这不会重放失败
调用，也不会把旧连接上的响应交付给新调用。它满足“第一切片不启用 reconnect”的
工作默认，但需要明确记录：

- 一次 invocation 中途断连 → 该 emission 失败（或按现有池语义成为 fault），
  不会自动补发；
- 下一次独立 invocation 可能建立新连接并成功；
- 旧连接的 pending 已失败，迟到响应没有 pending 可交付；
- 成员、分母和 reference 不变。

若未来要 adapter 后台重连，必须引入 generation 并单独裁决；不能把 pool 的惰性
重拨描述成“已经支持 geth 式自动重连”。

### 4.4 #6：WSS 握手 429 的归属需要单独决定

WSS dialer 丢弃了 `websocket.Dialer` 返回的 HTTP response，握手失败只留下通用
`wss: dial: ...` 错误。因此：

- 握手 429 目前不会满足 `rpc.RateLimitError`，会被 pool 当作普通 source failure；
- 调用级 JSON-RPC 限流错误仍可通过 `rpc.ProtocolError` code 或 adapter 的
  capability 识别；
- read limit、响应超时和断连各自映射到现有 neutral sentinel，不能进入
  Decision/quorum/reference/typed。

第一切片推荐接受该限制并在文档中写明；若要做握手 429 的 rate-limit 分类，
需要真实 429 证据和单独的 health 映射决策。

### 4.5 #7：写路径排除必须由路由机制保证

`write.go` 的 `SendRawTransaction` 使用 `pool.Broadcast` 和
`pool.VotingMembers()`；`pool.Broadcast` 默认遍历所有成员。因此：

- “adapter 没有写方法”只说明 WSS 包不实现写 API，不说明 WSS source 不会被写
  路径使用；
- 直接把 WSS source 加入现有 pool，会让 `eth_sendRawTransaction` 也走 WSS；
- 在 WSS 上写失败或取消会 abort 连接，产生未知交易结果，而当前 adapter 明确
  不重放；
- 给 pool 增加 read-only source 角色会改变写路径的固定 basis，可能降低有效
  多数，必须作为新业务语义单独裁决；
- 推荐首选独立只读组合（独立 pool，或只在只读 facade 后暴露），而不是改变
  现有 pool 的成员角色。

写路由是最直接阻塞“最小接线”的问题；presence 生产行为也必须在接线前选择。
若主维护者选择 adapter-only，则记录为可接受限制，不进入 Phase 3 接线。

## 5. 若批准接线：一期范围

只有主维护者接受本矩阵中的推荐项并写出决策记录后，才进入以下范围：

1. **配置与组合根**：增加显式 per-source transport 字段，缺省 native-http；
   composition root 负责 adapter 选择，业务方法不读取 URL scheme。
2. **只读组合**：WSS source 只进入只读组合；写路径继续使用 HTTP-only 组合，
   或以新决策明确写路由机制。集成测试必须证明 `SendRawTransaction` 不接触
   WSS source。
3. **presence 行为**：明确选择保留 raw 四态或降级 unknown；若保留四态，记录
   0056 仍禁止 golden、但生产行为接受 null 可见性。两种选择都要有回归测试。
4. **生命周期**：沿用 pool 的惰性下一次调用重拨；不启用 adapter 后台重连、
   主动 keepalive 或内部队列；不自动重放未知结果。
5. **集成验证**：固定成员、固定分母、health 归属、reference、method plan、
   typed projection、写路径隔离和关闭/取消行为全部保持不变。
6. **回滚**：所有接线由一个显式开关控制；删除配置和组合根分支即恢复 native
   HTTP 默认，不留下第二套 registry。

reconnect、keepalive、backpressure 和 T11 若未来启用，必须分别提交、分别
验收，不能混入本期接线。

## 6. 证据门与阻塞分类

| 项 | 当前状态 | 阻塞类型 | 最小下一步 |
| --- | --- | --- | --- |
| W4 `error.data:null` | 未观察 | 外部证据阻塞 | 继续用 opt-in 工具寻找真实 null；未得到前不修订 0056 |
| WSS 握手 429 / read limit / response timeout | 未观察 | 外部证据阻塞 | 只做小样本 live observation，不制造负载 |
| WSS 迟到响应 / provider 侧重连 | 未观察 | 外部证据阻塞 | 需要 provider 行为和显式实验设计 |
| WSS 断连（`1006`） | 已观察一次 | 可接受的部分证据 | 不据此授权自动重连 |
| source→adapter 选择 | 设计未冻结 | 需要主维护者决策 | 选择显式 per-source 字段或组合根注入 |
| 写路径路由 | 未解决 | 需要主维护者决策 | 选择 adapter-only、独立只读组合或新成员角色 |
| WSS presence 生产行为 | 未选择 | 需要主维护者决策 | 选择保留四态或降级 unknown |
| reconnect / keepalive / backpressure | 无实现、无证据 | 需要主维护者决策 | 第一切片维持不启用 |

## 7. 影响与回滚

- 本文件只更新决策输入，不改变生产代码、默认 runtime、pool、aggregator、
  reference、typed、quorum 或写路径；
- 推荐项被接受后，配置与组合根接线必须可单独回滚到 native HTTP 默认；
- 删除本文件不影响现有 WSS adapter、进程内 contract 或 W4 证据工具；
- 不得用本矩阵把 W4、T11、freshness 或 WSS 接线的阻塞写成已完成。

## 8. 待主维护者回答的问题

1. “写路径继续排除”是指 **WSS source 不得进入写能力 pool**，还是仅指
   **不实现 WSS 专用的写重连/幂等**？前者需要独立只读组合或新成员角色，后者
   仍会让现有 `Broadcast` 走 WSS。
2. 在 0056 修订前，production WSS source 应保留 raw 四态，还是降级为 unknown？
3. source→adapter 选择采用显式 per-source 配置字段，还是只允许 composition
   root 注入，不进入公共 config？
4. 如果 null 证据和写路由机制都无法在近期解决，是否接受 WSS 长期保持
   adapter-only（记录为可接受限制）？
5. 是否允许“全 WSS 只读部署”作为一期形态，还是必须先支持 HTTP/WSS 混合成员？
