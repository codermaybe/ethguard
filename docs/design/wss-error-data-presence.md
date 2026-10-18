# WSS error-data presence：根因、边界与决策路线

本文件是 `project-roadmap.md` T08 审计遗留第 4 条的展开参考，也是未来 WSS
transport 立项的第一手技术依据。它只记录事实、边界和可执行判断，不改变
`decisions.md` 0056 的结案状态：在该决策被显式升级前，WSS golden 继续拒绝，
HTTP 四态回放保持有效。

结论先行：**WSS 的 `error.data` presence 坍缩发生在 go-ethereum 的
`decodeError` 类型化步骤，不在 codec。** 但这只说明“损失点在库内某一步”，
**不等于 EthGuard 能通过公开接口取回原始字节。** 因此按“先承认、后改造”处理：

1. **先承认**：承认当前 seam 下无法判定 presence，先把“不知道”准确表达出来。
2. **后改造**：在 pool 内建立自己的调用接口，再评估自行管理 WSS 请求响应或
   采用上游扩展点的成本，决定是否实现。

本文件当前状态是**讨论稿**：根因与边界已核实，候选机制与成本仍需进一步验证，
不作为实施依据。

---

## 1. 现象与影响面

### 1.1 触发场景

- 上游使用 `ws://` / `wss://`，并返回 JSON-RPC `error` 对象。
- 当前唯一有方法级裁决影响的路径是 `eth_call` 的执行 revert：`CallPolicy`
  会把 RPC error body 纳入 revert 解析（`rpc/methods/eth_call.go` 的
  `WithRPCErrorBodies()`）。
- 不涉及成功响应的 `result: null`，也不改变普通对象查询的 null/not-found 语义。

### 1.2 线上的数据形态

`error.data` 的 wire 状态：

| 形态 | 例子 | 是断言吗 |
| --- | --- | --- |
| 缺失（没有该成员） | `{"code":3,"message":"execution reverted"}` | 否，弃权 |
| 显式 JSON `null` | `...,"data":null}` | 否，弃权 |
| 空 DATA 字符串 | `"0x"` | 是，concrete |
| 具体 revert bytes | `"0xdeadbeef"` | 是，concrete |
| 非 hex 字符串/对象等扩展 | `"oops"`、`{...}` | 是，concrete |

**缺失和 `null` 是两种 wire 状态，不能解释成空字节。** 前两者在裁决上都是
`data` 维度弃权，但它们在证据、诊断和 replay 上是可区分的。

### 1.3 HTTP 行为（现状，正确）

收集层保留原始 error 对象：

- 缺失 → `Data == nil` 且 presence=false；
- 显式 `null` → JSON `null` 且 presence=true；
- `0x` / 具体 bytes / 其他非 null JSON → 保留原值。

实现：`pool/rawbody.go` 的 `capturingTransport` + `preservedDataError`，
由 `TestPreserveErrorDataDistinguishesMissingAndNull` 锁定，并接入
`aggregator/aggregator.go` 的 `observationRPCError`。

### 1.4 WSS 行为（现状）

仍走 go-ethereum 解码路径。缺失与显式 `null` 在进入 `Observation` 前都变成
geth `DataError.ErrorData() == nil`（根因见 §3），最终 `rpc.RPCError.Data`
未设置；`eth_call` 的比较键因此都表现为 `reverted|missing`。非 null 的 `0x`、
具体 bytes 和其他 JSON 数据仍保留为 present。

### 1.5 业务影响（不变）

在 `eth_call` 中，缺失和 `null` 都只对 revert 的 `data` 维度弃权：

- 不会进入 `Decision.Value`，也不会互相补全；
- `execution` / revert 维度仍可正常聚合，具体 `data` 仍按动态门槛单独裁决；
- 混合 HTTP/WSS 时，同一个线上 `data: null` 可能分别表现为 `reverted|null`
  和 `reverted|missing`，但两者都不为 `data` 投票，核心结果语义不变。

损失的是 WSS 的 wire presence、诊断和 replay 精度。**不能把 `null` 当作空
bytes，也不能据此生成 WSS 四态 golden。**

---

## 2. 待改动字段清算（讨论中，不是前置闸门）

本节只是与 WSS 相关字段的讨论登记处，**不是“冻结前禁止改造”的前置条件**。
当前没有确定字段，因此不设出口闸门，也不阻塞 WSS 或任何其它工作。

讨论纪律：

- 只登记**可能被调用级 seam 波及**的字段；与 seam 无关的优化不进来。
- 每项写清：字段名、位置、改动动机、与 WSS presence 的关系（前置/无关）。
- 登记是信息性的，不构成排期承诺。

| 字段/接口 | 位置 | 为何与 seam 相关 | 状态 |
| --- | --- | --- | --- |
| （待讨论） | — | — | 讨论中 |

---

## 3. 根因：坍缩发生在 `decodeError`，不在 codec

逐帧核实 go-ethereum v1.17.5：

1. `websocketCodec` 把每个 frame 交给 `jsonCodec.readBatch()` →
   `conn.ReadJSON(msg)`，解进 `jsonrpcMessage`。
2. `jsonrpcMessage.Error` 是 **`json.RawMessage`**（`rpc/json.go:64`）。到这一步，
   `error` 对象的原始字节完整保留。
3. `Client.CallContext` 拿到响应后调用 `resp.decodeError()`
   （`rpc/client.go:367`）。`decodeError` 做
   `json.Unmarshal(msg.Error, &jsonError{... Data interface{} ...})`
   （`rpc/json.go:113-119`）。
4. `Data interface{}` 把“缺失”和“JSON null”都变成 `nil`；
   `jsonError.ErrorData()` 只能返回 `nil`。
5. `observationRPCError` 只能通过 `errors.As(err, &dataErr)` 看到 `nil`，
   于是两种状态都落成 `reverted|missing`。

响应分发路径 `c.read()` → `readOp{msgs}` → dispatch →
`handler.handleResponses` → `respWait[msg.ID]` → `op.resp`，把**整条
`*jsonrpcMessage`** 投给调用方 goroutine（`rpc/client.go:649-740`、
`rpc/handler.go:448-511`）。

### 3.1 关键限定：库内存在 ≠ EthGuard 可取得

原始字节在 geth 内部可达，**但 geth 没有把它暴露给外部包**：

- `rpc.ServerCodec` 虽然名字导出，但方法（`readBatch`/`close`/`peerInfo`）与
  参数类型 `*jsonrpcMessage` **全部未导出**，外部包**无法实现**该接口
  （`rpc/types.go:43`、`rpc/json.go:208`）。
- `DialOptions(...)` 返回的仍是 geth `Client`（`rpc/client.go:193`），
  `resp.decodeError()` 由 `Client.CallContext` 内部执行
  （`rpc/client.go:367`）；替换 `WithWebsocketDialer` 只改拨号方式，
  **绕不过这一步**。

因此准确表述是：

> 坍缩发生在 `decodeError` 的类型化步骤，这个根因已核实。
> 但“修复点”不是“实现公开的 `ServerCodec`”——该接口外部不可实现。
> 真正要做的是在 **EthGuard 自己的调用层**里取得并解析原始 error，
> 其机制与成本仍需在 §4 中评估。

---

## 4. 候选路线（成本待评估，不预设行数）

### 路线 1（近期，最低成本）：把“不知道”准确表达出来

当前 `ErrorData() == nil` 时的准确表述是：

> 无法判断上游是省略了 `data`，还是显式返回了 `null`。

而不是“已确认缺失”。近期切片只做这一件事：

- **不改裁决行为**：保持 `execution`/`data` 的弃权、分母和门槛不变。
- **不引入新的比较键**：不新增 `reverted|unknown-presence`。那会把传输信息
  引入方法比较键，牵动 `callComparisonKey`/`classifyCall`/`dataSplit`/
  `projectRevertData` 及 replay/分类测试，超出“只承认缺陷”的范围。
- **载体优先放在调用级诊断信息**，而不是给 `rpc.RPCError` 直接加字段。

关于新增字段的谨慎项（若最终选择字段方案）：

- 现有代码多处直接构造 `rpc.RPCError{}`（`aggregator/aggregator.go:450`
  及大量测试）。新增 `bool` 的零值会让这些对象突然变成“未知”，必须先明确
  默认值与构造兼容性，不能只加一个字段。
- 未知状态必须依据**是否取得保真信息**判定，**不能仅凭 URL 是 WSS** 判定。

完成标准：**调用方或诊断能看到该限制**，且既有 `RPCError{}` 构造全部兼容。

### 路线 2（后续，评估阶段）：在与上游之间建立自己的调用层

方向：在 **pool 内**把“Source 如何调用上游”收窄为自己的调用接口，接口表达
raw result、structured RPC error、`error.data` presence、以及可选的有界诊断。

需要并行比较的实现路线（本阶段只评估，不实现）：

1. **自建最小 WSS 调用层**：自己管理长连接、request/response demux、
   presence 保留、取消、inflight 清理、read limit、close。
2. **上游扩展点**：核实 geth 是否提供可用的公开对接口；若不提供，
   评估提 issue/PR 的可行性与时间成本。
3. **依赖与维护成本**：自建层与 geth 版本升级、语义漂移的长期负担。

在评估完成前，**不给出实现行数估计**，不把 seam 建立视为 presence 已恢复。

### 路线 3（不推荐）：fork geth / 反射未导出字段

维护成本高、与 `go.sum` 脱钩、随版本失效。与 0056“不复制完整客户端、
不引入第二 socket、不建新长连接信任模型”的边界冲突。

---

## 5. 边界（不可越界）

- 不使用“快速扫描 ID”替代完整 JSON envelope 校验。
- 不以 `sync.Pool` 零拷贝优化优先于证据切片的防御性拷贝。
- 不为当前没有订阅接口的项目预先设计 Call/Subscribe 双连接池。
- 不以实现行数作为生产可行性的证明。
- 写路径的重连和未知结果语义必须单独验收，**不能因 WSS 读取保真而改变交易
  广播契约**。
- 不把 RawWire 放入 `Observation`、比较键或 `Decision.Value`。
- 不改变 Quorum / 动态门槛、Aggregator、Policy 或方法数量。

---

## 6. 执行顺序（“先承认、后改造”）

1. **近期：把“不知道”表达出来。** 保持 `execution`/`data` 的裁决行为、分母、
   门槛；WSS 四态 golden 继续拒绝。只有调用方或诊断能看到该限制，
   才算完成这一切片。
2. **后续：评估 transport seam。** 比较自建最小 WSS 调用层、上游扩展点、
   依赖维护三条路线的成本，再决定是否实现；不预设行数。
3. **保真实现验收后：升级 0056。** seam 的建立本身**不代表** missing/null
   已恢复；只有四态在 WSS 路径下经进程内 WebSocket 测试成立，才升级决策并
   增加 WSS 四态 golden。

HTTP capture hardening 保持既有优先级，但**不是**研究 WSS seam 的技术前提。

---

## 7. 未决问题

- 路线 1 的载体：调用级诊断的具体形态（日志/`UncertainCapture`/其它）
  尚未确定。
- 路线 1 若最终选择 `RPCError` 字段，其默认值与构造兼容性方案尚未确定。
- 路线 2 的自建最小 WSS 调用层，是否需要完整接管 ping/pong，还是可复用一个
  更薄的传输封装，需进一步验证可插拔点。
- geth 是否存在可用的公开扩展点，需在评估阶段确认。
- WSS presence 保真后，`eth_call` 之外是否还有其它方法会因为 error body
  presence 进入裁决（当前判断：没有）。
