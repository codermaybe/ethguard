# WSS 统一 Source 与写路径语义

状态：**已接受并实施（2026-11-01，用户授权）**。
本文是统一 Source 的主要设计参考；实施检查与真实 provider 观察见完成报告，
生产范围遵循 [已接受矩阵](wss-productionization-decisions.md)、0056.2 与 0064。

## 1. 统一 Source，不新增只读角色

显式配置的 WSS source 与 HTTP source 进入同一个 `pool.Transport` seam、读调用、
写广播及共享 health 调度。WSS 不是 readonly；`eth_sendRawTransaction` 通过既有
`pool.Broadcast` 发送给固定成员，不引入 WSS 专用业务分支或独立只读组合。

本决定统一的是错误解释与调度规则，不保证不同传输具有相同的 fault 分布。
不改变固定成员、冻结 basis、分母、门槛、单位权重、reference、method plan 或
typed projection。cooldown 只 gate 调度；未发送的成员仍在 basis 中，
`Attempted=false` 不产生新的失败。HTTP/WSS 指向同一 backend 不构成两个独立
信任来源；协议或连接数量不是 provider 独立性的证据。

每个 source 显式选择 transport，缺省为 `native-http`。composition root 负责
adapter dispatch；业务方法不得按 URL scheme 路由，也不得创建第二套 source
registry。WSS 必须显式 opt-in，不改变默认 HTTP 行为。

### 组合根路由

- `config.Upstream.Transport` 接受 `native-http` / `wss`；省略保留 native HTTP。
- 显式 transport 与 endpoint scheme 在配置校验期匹配；不根据 scheme 隐式换 adapter。
- 组合根构造 `pool.Member{Name, URL, Dialer}`，每个 source 固定持有该 Dialer；
  nil 继承 pool 默认。没有 URL→adapter 辅助 registry，也无额外 source 身份。
- `DialerOption`/`gethcompat.New` 仅服务未显式声明 transport 的配置；与显式
  per-source transport 混用直接拒绝，避免两套选择优先级。
- 改 transport 需重建 Client；不新增运行时成员或 adapter 热切换。


## 2. 调用证据、写裁决与 health 分离

一次调用的结果、`Decision`/`WriteDecision` 中的 Support/Reject/Faults 与 source
health 是不同事实。收到 JSON-RPC error 不等于连接失效或 provider 全局不可用。

| 调用结果 | 调用/裁决证据 | 共享读写 health |
| --- | --- | --- |
| 成功返回 | 保留正常结果；写 hash 可进入对应 cluster | success，连续失败归零 |
| `-32601` method not found | 本次方法的 capability/rejection evidence，不是全局 failure | 健康响应，记录 success 并重置连续失败 |
| 其他正常业务 protocol rejection | 保留本次 rejection，不推断 transport 故障 | 按通用健康响应分类 |
| server/parse/invalid-request 等不可用类别 | 保留本次 error/fault | 按通用 failure 规则 |
| RPC `429`、`-32005`、`-32016` 或已识别的 rate-limit capability | 保留 rejection/fault | failure 并减慢 pace，连续失败可进入 cooldown |
| transport/read failure、read limit、本地调用超时 | 保留本次 transport fault | 对可归因的 attempted emission 记录 failure |
| 父 context 取消、等待共享 dial、未获得本地 slot、未发送 gate | 不伪造 provider rejection | 不惩罚 source |

不增加“方法不支持即退出 health”规则。读写使用同一 source health，因此写调用
产生的健康结果也影响后续读调度，反之亦然。failure 按实际 attempted emission
计数，不按 socket disconnect 事件计数：一次断连可能使多个已尝试调用失败，
不是合并成一次失败；未尝试调用不得因共享断连被惩罚。既有连续失败/cooldown
门槛不因 WSS 改变。

## 3. 写结果的知识边界

WSS 写成功与 HTTP 一样按返回 hash 参与既有写裁决。方法不支持只是本次写的
rejection/capability evidence，不自动证明 raw transaction 无效。

**`WriteRejected` 可以与其他 emission 的未知 transport delivery 同时存在。**
它不是交易无效、从未提交、provider 未接收或链上不会执行的证明。frame 已写出
或写入中发生失败时，交易是否到达 provider 可能未知；pool 不把未知改写为确定
成功或确定未提交。receipt 查询仍是调用方的后续确认步骤，不新增恢复或幂等机制。

任何 transport 都不得在当前 invocation 内自动重放请求。下一次独立调用可以由
pool 惰性重拨，但不得补发上一次调用。

## 4. 生命周期边界

以下是不同事件，不能都描述成“取消即 abort”：

- **显式 Close**：拒绝新调用；已有 pending 继续由各自 context 控制，收到响应或
  取消后释放 socket。Close 本身不取消在途调用，也不是 provider fault。
- **等待响应时取消**：移除/完成本次 pending，保留 socket，其他调用继续；迟到响应
  不得交付给其他 pending。取消不证明请求未发送。
- **等待写入机会时取消**：未实际写 frame 时，不因取消破坏共享连接。
- **实际 frame write 失败或被中断**：frame 完整性不再可靠，必须 abort 连接及相关
  pending；不得继续复用或重放。只有这种写阶段失败要求因取消而 abort。
- **read failure / peer disconnect**：连接已失效，终止相关 pending，后续独立调用
  按 pool 生命周期惰性重拨。
- **read limit**：保留 body-too-large 类别并终止失效连接，不把它伪装成普通取消。

不启用 adapter 后台 reconnect、主动 keepalive、内部队列、自动 replay 或自动
fallback。pool 丢弃失效 transport 后的下一次独立调用惰性 dial 不属于后台重连。

## 5. raw presence 的已接受生产范围

raw WSS 保留 missing、JSON `null`、`"0x"`、具体 value 四态，不在接线层降级为
unknown。`eth_call` 中 explicit null 可产生 `reverted|null`，missing 产生
`reverted|missing`；两者都在 data 维度弃权，但比较键与证据形态不同。`0x` 和
具体 revert bytes 是具体 data 断言。接受这种可见性，不声称“只是换连接”。

这是 0056.2 的生产范围补充，不是 provider-backed golden gate 的豁免。真实
provider 的 `null` 仍未观察；合成或进程内测试只能证明实现行为，不能变成真实
provider 证据。HTTP 四态合同不变。

## 6. 实现验收合同（非已通过记录）

- 默认 native HTTP 与无 WSS 配置的行为不变；显式混合 HTTP/WSS 经组合根分派。
- WSS 写成功、`-32601`、server error、RPC 限流及 transport failure 进入既有
  写证据与共享 health；`-32601` 重置连续失败，限流失败并减慢 pace。
- 固定成员/basis/权重及 cooldown 中 unattempted emission 不变；failure 按尝试
  调用计数，而非断连数。
- pending cancel 不关闭可用 socket；实际 frame write failure、read failure、
  read limit 与 Close 分别覆盖，未知写结果不重放。
- HTTP/WSS 混合 `eth_call` 保留 raw presence，未引入业务 scheme routing、
  第二 registry、只读角色或写专用 adapter 分支。

本文件记录批准的目标；实现状态和最终检查结果由集成后的交付记录说明。
