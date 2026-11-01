# WSS 统一 Source 与写路径语义

状态：**自包含讨论稿（2026-11-01），待主维护者裁决**。

本文记录一种明确的产品意图：WSS source 不因为传输协议被默认划成只读源。
它与 HTTP source 一样进入 `pool.Transport` seam、读调用、写广播、fault 记录和
health 调度。WSS 不支持某个 RPC 方法时，按该 source 的协议结果或 transport
失败处理；是否达到 cooldown，由现有 pool health 规则决定。

本文补充但不删除 [WSS 生产化推荐决策矩阵](wss-productionization-decisions.md)。
矩阵中关于“独立只读组合”的建议需要按本文的统一 source 意图重新评估。

## 1. 先区分三种结果

一次 WSS 调用同时产生三个层面的事实，不能把它们合成一个“失败”概念：

1. **调用结果**：这个 source 对本次 JSON-RPC 请求返回了什么；
2. **写/读裁决证据**：本次结果如何进入 `Decision`、`WriteDecision`、Support、
   Reject 和 Faults；
3. **source health**：这次错误是否应该增加连续失败、降低调度资格并进入 cooldown。

当前 pool 已经把这三层分开：一个 JSON-RPC protocol error 可以是本次广播的
rejection evidence，但仍然是一个健康的、可通信的 provider 响应；transport
断连、超时或 server failure 才通常会降低 source health。

## 2. 现有代码事实

### 2.1 写路径使用固定 pool 成员

`Client.SendRawTransaction` 调用 `pool.Broadcast`，而 `Broadcast` 默认遍历
`VotingMembers()` 的固定成员。它不读取 adapter 类型，也不判断某个 source 是否
“声明只读”。因此只要 WSS source 进入同一个 pool：

```text
HTTP-A ─┐
HTTP-B ─┼─ eth_sendRawTransaction
WSS-C  ─┘
```

WSS-C 就会收到写请求。这是统一 source 模型的自然结果，不是额外的 WSS 特例。

### 2.2 Broadcast 不使用 freshness gate

写广播不因 source tip 落后而跳过 source，也不在第一个成功后停止。每个可请求
成员都有一条 emission；cooldown 中的成员保留在结果中，`Attempted=false`，
不会因为本轮未发送而产生新的 health failure。

### 2.3 失败与 cooldown

对已实际发送的调用，`pool.dispatchOne` 根据 `isHealthyRPCResponse` 决定是否
记录 source failure：

- `nil` 成功：健康，连续失败归零；
- JSON-RPC protocol error：通常是健康响应，除非属于 server/parse/invalid-request
  等被定义为不可用的类别；
- transport error、timeout、连接关闭、read limit 等：通常记录 failure；
- 父 context 取消、等待共享 dial、未获得本地调度 slot：不应惩罚 source。

当前默认健康门槛是：连续 3 次可归因失败后进入 30 秒 cooldown。冷却期间 source
仍在固定成员和分母中，但读写请求都会得到 gated emission，直到后续 probe 或调用
允许恢复。

## 3. 具体场景

### 场景 A：WSS 支持写方法并成功

```text
WSS-C 收到 eth_sendRawTransaction
返回同一交易 hash
```

结果：

- `WriteDecision` 可把它加入 hash cluster；
- source 记录 success，连续失败归零；
- 不需要任何 WSS 专用写分支；
- 和 HTTP source 的处理完全相同。

### 场景 B：WSS 不支持写方法，返回 `-32601`

```json
{"code":-32601,"message":"method not found"}
```

当前 pool 语义下：

- 该 emission 是写路径的 rejection/evidence；
- `WriteDecision` 可以记录 Reject 或 Fault 相关证据；
- `-32601` 是一个正常收到的 JSON-RPC protocol response，当前
  `isHealthyRPCResponse` 不会把它当成 transport failure；
- 因此 source 不会仅凭一次“方法不支持”进入 cooldown。

这点必须明确：**“报错进入写结果”与“source 跌出 health”不是同一件事。**
如果产品要求“不支持写方法的 WSS source 自动退出 health”，就需要新的 health
分类规则或 source capability 语义，不能只依赖当前通用 pool fault 逻辑。

### 场景 C：WSS 写调用断连

```text
请求已写出或正在写出
socket 关闭
调用返回 transport.ErrConnectionClosed
```

结果：

- 本次 emission 是 transport failure；
- source 记录 failure；
- 交易是否已到达 provider 未知，adapter 不自动重放；
- 连续达到 3 次后进入 cooldown；
- 下一次独立调用可能重新拨号，但当前 invocation 不补发。

这和 HTTP source 的连接失败属于同一 health 机制；未知交易结果仍然是写路径
自己的幂等和重试问题。

### 场景 D：WSS 返回 server error 或限流

例如 `-32603`、已识别的 rate-limit code 或带 `RateLimitError` capability 的错误：

- 写结果保留该 source 的 rejection/fault；
- health 记录相应的 server error 或 rate-limit 计数；
- 调度 pace 变慢，连续失败达到门槛后进入 cooldown；
- 其他 source 仍按固定成员规则参与广播。

### 场景 E：同一个 pool 中 HTTP 与 raw WSS 混合

对于 `eth_call`：

```text
HTTP-A: error.data 缺失       → reverted|missing
WSS-C:  error.data:null       → reverted|null
```

raw WSS 保留 wire presence 后，`null` 与 `missing` 仍是不同的证据形态。两者
都属于 data 维度的非具体断言，但比较键、Support/Reject 证据和 replay 形态不会
被强行合并。

如果 WSS 返回 `"0x"` 或具体 revert bytes，它是 data 维度的具体断言，可能与
missing/null 的弃权形成不同的 data 结果。这是 presence 保真带来的业务可见性，
不是单纯换连接。

## 4. 统一 Source 模型的收益与代价

### 收益

- pool 只维护一个 Transport、fault、health 和 cooldown 语义；
- 读写 source 行为一致，不需要把协议能力硬编码成“只读/可写”角色；
- WSS 不支持的方法可以通过普通 protocol response 或 transport failure 自然
  暴露；
- 固定成员、广播证据和 health 调度保持同一个来源模型。

### 代价

- 接入 WSS 后必须接受它会收到写请求；
- provider 的“方法不支持”可能是写 rejection，但不一定触发 health cooldown；
- 传输错误可能导致未知交易结果，不能靠 adapter 自动重放解决；
- raw presence 可能使混合 HTTP/WSS 的 `eth_call` 证据形态出现差异；
- 如果未来确实需要只读 provider，必须新增 source capability 或独立组合，不能
  通过当前 pool 的隐式约定实现。

## 5. 待裁决问题

### 5.1 是否接受 WSS 进入统一写广播

选项：

1. 接受统一 source 模型：WSS 进入现有 pool，写请求照常发送；错误和 health
   按通用规则处理；
2. 引入“方法不支持”health 规则：例如对明确的 `-32601` 或配置声明的能力缺失
   记录 source failure；这会改变当前 protocol error 的 health 语义；
3. 引入 read-only source 角色或独立只读组合：这会改变成员角色、写广播分母和
   组合根结构。

用户当前意图对应选项 1；选项 2 和 3 需要新的架构决策，不能在 WSS 接线时顺手
加入。

### 5.2 presence 行为

选项：

1. raw WSS 保留 missing/null/`0x`/value 四态，接受 `null` 的生产可见性；
2. 接线层把 WSS missing/null 降级为 unknown，保持与 geth WSS 的旧行为一致；
3. WSS 继续 adapter-only，直到真实 provider 观察到 `data:null` 或主维护者明确
   接受长期不可知。

这项选择独立于“WSS 是否参与写广播”。统一 source 模型并不自动决定 presence
如何向上暴露。

### 5.3 未知写结果

无论选择哪种 source 模型，都必须保持：

- adapter 不自动重放未知交易；
- pool 不把一次断连改写成成功或失败的确定事实；
- receipt 查询是调用方后续确认步骤；
- reconnect、幂等、重放和写结果恢复必须另立写路径决策。

## 6. 进入实现前的验收

如果采用统一 source 模型进入生产接线，至少需要：

- WSS 成功写请求进入 `Broadcast` 的进程内测试；
- WSS `-32601`、server error、rate-limit、transport failure 的 WriteDecision、
  fault 和 health 分类测试；
- 连续 3 次可归因失败进入 cooldown，cooldown 期间固定成员和 emission 语义不变；
- source 恢复后下一次独立调用重新拨号，不在当前 invocation 内重放；
- HTTP/WSS 混合 `eth_call` 的 presence 与 data dimension 回归；
- `SendRawTransaction` 不新增 WSS 专用分支，不改变固定成员、广播分母和 receipt
  后续确认边界；
- 默认 HTTP 配置和无 WSS source 的行为完全不变。

本文不授权接入，也不裁决 presence。它把统一 source 意图与当前 pool 实现之间
的真实关系固定下来，供 WSS 生产化决策、代码评审和多方意见比较。
