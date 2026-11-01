# WSS 生产化已接受决策矩阵

状态：**已接受（2026-11-01，用户授权）**。原推荐矩阵的接线、presence 与写路径
问题已由统一 Source 方向裁决；不再推荐 adapter-only 或独立只读组合。
本文规定已落地的生产范围；具体检查与 live observation 边界见完成报告。
详见 [WSS 统一 Source 与写路径语义](wss-unified-source-write-path.md) 和决策 0056.2。

## 1. 已接受矩阵

| # | 决策 | 已接受范围 | 不在本期范围 |
| --- | --- | --- | --- |
| 1 | 接入 config/runtime | WSS 显式 opt-in，进入与 HTTP 相同的读写 pool；默认 HTTP 不变 | 默认切换、自动 fallback、adapter-only 作为生产结论 |
| 2 | source→adapter 选择 | 显式 per-source transport，缺省 `native-http`；composition root dispatch，沿用既有 source 身份与 registry | 业务方法读取 URL scheme、第二套 registry、隐式协议路由 |
| 3 | reconnect | 失效连接由 pool 丢弃；仅下一次独立调用惰性重拨 | adapter 后台 reconnect、当前 invocation 重试/重放 |
| 4 | keepalive | 不主动启用；接受半开连接可能由下一次调用发现 | 主动 ping/pong loop、idle reaper、新 liveness 语义 |
| 5 | backpressure | 使用调用 context、pool 调度与既有 timeout；不新增内部队列 | adapter queue、新容量错误或优先级 |
| 6 | 生命周期与 health | 区分 Close、pending cancel、frame write failure、read failure、read limit；health 按 attempted emission 记录，读写共享 | 按 disconnect 数折算 failure、取消 pending 即关闭 socket、生命周期改变 quorum |
| 7 | 写路径 | WSS 不设 readonly 角色；沿用固定成员 `Broadcast`，错误解释统一 | 独立只读组合、跳过 WSS、改变 basis/成员/权重、WSS 专用业务写分支 |
| 8 | presence | raw missing/null/`0x`/value 四态直接保留；接受 explicit null 生产可见性 | 降级 unknown、用合成证据通过 provider-backed golden gate |

上述决定统一错误解释，不承诺 HTTP/WSS 有相同 fault 分布。固定成员、冻结 basis、
分母、门槛、单位权重、reference、method plan 与 typed projection 不变。
同一 backend 的 HTTP/WSS endpoint 不构成独立信任；不能因使用不同传输而声称
provider 独立性。

## 2. 错误与共享 health

正常业务 JSON-RPC rejection 是本次调用证据，不自动意味着 transport 或 source
全局失效。`-32601` 是方法 capability evidence：可进入本次写的 rejection，
同时必须作为健康响应记录 success 并重置连续失败，不新增“写能力不足即冷却”。

RPC `429`、`-32005`、`-32016` 及已识别 rate-limit capability 按通用规则记录
failure 并减慢 pace；server/transport failure 按通用规则处理。读写共享同一份
health，不建立 WSS 专用 fault policy。握手 HTTP 429 与 JSON-RPC code 429 是不同
输入；本决定不以 RPC 限流规则暗示新增握手 status 映射，通用 dial-error 分类的
限制仍须如实保留，未来映射应单独提供证据。

failure 按实际 attempted emission 计数，不按 disconnect 事件数计数。共享断连
使多个已尝试调用失败时，各调用可分别贡献 failure；未发送 gate、等待共享 dial、
未获得本地 slot、父 context 取消与显式关闭不应伪造 provider failure。cooldown
中的成员继续保留 basis 与 unattempted emission，不缩小分母或调整权重。

## 3. 写结果与生命周期合同

`WriteRejected` 只描述既有写裁决，能与其他 emission 的未知 transport delivery
共存；不能证明交易无效、未提交或不会执行。写入中断或已发出后失联时，delivery
可能未知。receipt 查询仍由调用方后续确认，不新增幂等、恢复、自动补发或 replay。

- 显式 `Close` 拒绝新调用，已有 pending 等响应或自身 context 结束后才释放 socket；
  不主动取消在途调用，也不当成 provider 故障。
- 等待响应时取消仅完成/移除该 pending，保留共享 socket；迟到响应不重新分配。
- 等待写锁/写入机会时取消且尚未实际写 frame，不关闭共享连接。
- 实际 frame write 失败或被中断必须 abort：frame 完整性已不可依赖，相关 pending
  失败且不重放。不是所有调用取消都会触发这一规则。
- read failure/peer disconnect 表示连接失效，应终止相关 pending。
- read limit 保留 body-too-large 分类并终止失效连接，不能混同普通 pending cancel。

后两类读事件与 Close 也会结束连接；“只有失败/中断的 frame write 要求 abort”
指取消的写阶段完整性边界，不意味着失效 reader 或显式 Close 应保留连接。
pool 后续独立调用的惰性 dial 不是后台重连，不恢复上一 invocation。

## 4. 0056 的生产范围补充与证据门

已批准保留 raw WSS `error.data` 的 missing、JSON null、`"0x"`、具体 value。
`RPCError.Raw()` 应保留 null、对 missing 省略 data；不在接线层统一降为 unknown。
在 `eth_call` 中 raw explicit null 可产生 `reverted|null`，而 geth unknown 路径为
`reverted|missing`。null/missing 都对 data 维度弃权，但比较键、聚类与 support
证据可能不同。这是接受的有限生产行为升级，不是无行为变化的连接替换。

0056 的 provider-backed WSS golden gate 保留：真实 provider 已有 missing、`0x`
与具体 data 的观察，**null 仍未观察**，不能据此断言不会发生。进程内确定性测试
或合成 response 证明实现保真，不证明 provider 行为，不得当作 provider golden。
本补充不改变 HTTP 四态合同，不宣称 W4 或其他外部证据门已完成。证据来源见
[wss-provider-evidence.md](wss-provider-evidence.md)。

| 事项 | 决定或证据边界 |
| --- | --- |
| per-source dispatch / 写路由 / raw presence 生产接线 | 已实施并通过最终检查；不再是待裁决阻塞 |
| W4 `error.data:null` | 未观察，继续保留 provider-backed golden gate，不阻塞已批准的生产范围 |
| 真实 handshake 429 / read limit / response timeout | 未观察；不得用进程内测试改写为 provider-backed 证据 |
| 真实断连 `1006` | 先前已观察；2026-11-01 的 p02 三个协议响应后 follow-up 再次观察，不授权后台重连 |
| provider 迟到响应 / reconnect 行为 | 未观察，不据此承诺恢复行为 |
| reconnect / keepalive / queue | 本期明确不启用，未来另立决策与验收 |

## 5. 实现验收与回滚边界

需要确定性行为测试覆盖显式混合 source dispatch、默认 native HTTP、WSS 读写
共用固定 pool、方法不支持 healthy reset、RPC 限流 failure/pace、按 emission
记录 health、cooldown basis、pending cancel 保留 socket、frame write failure、
read failure/read limit/Close、下一次调用惰性 dial 与无 invocation replay，以及
HTTP/WSS raw presence。测试必须验证真实行为而不是名称或静态字符串。
这些是验收要求，不是本文件宣称已执行或通过的检查。

接线回滚应局限于配置/组合根的显式传输选择，恢复 native HTTP 默认，不留下第二
registry、只读角色或备用写路径；不能通过回滚悄悄改变固定 trust basis。runtime
落地状态、最终检查与路线图由集成后的交付记录说明，本文件不将批准等同于完成。
