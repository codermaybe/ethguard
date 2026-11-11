# WSS Provider Evidence

状态：**截至 2026-11-01 的三轮真实公开 provider 观察；provider-backed 四态 golden
证据门仍未满足，显式 `error.data:null` 未观察到。** 本文是 WSS presence 与连接
行为的 dated evidence 正文，不是 wire 或订阅契约，不作为当前 provider 兼容性
清单。0056.2 已批准 raw presence 生产保真，0064 已接入统一 Source；这些决定
不关闭本证据门。HTTP live canary 不提供 WSS 传输证据。

## 1. 采集方法与保留范围

- 网络：Ethereum mainnet，成功连接的 provider 逐条校验 `eth_chainId == 0x1`。
- 工具：opt-in `cmd/wsscapture`，不进入默认测试/CI。首轮为 Go 1.25.3、
  `github.com/gorilla/websocket v1.5.3`；capture 记录 commit、GOOS/GOARCH、
  provider class `public-unauthenticated`。这是当时的工具环境，不是当前 wire 依赖声明。
- 无需凭据的公开 WSS gateway；首轮匿名编号 p01–p06。具体 host/provider 映射、
  完整 frame 与 capture index 保存在仓库外，不在此发布 endpoint；编号不证明独立性。
- 每个 probe 新建连接；主请求后在同一连接尝试一次 `eth_chainId` follow-up。
  只读、不广播交易、不做负载测试、不刻意诱发限流、超大响应或迟到响应。
- 五个基础 probe：`chain-id`、`unknown-method`、`revert-with-reason`
  （USDC `transfer` 超余额）、`revert-without-reason`（WETH `transfer` 超余额）、
  `invalid-params`（非法地址的 `eth_getBalance`）。后两轮增加
  `block-hash-position`：把 EIP-1898 object 放在 `eth_getBlockByNumber` 的 block 位置，
  观察拒绝 envelope，不据此承诺该方法支持这种参数。
- 工具只持久化 `scheme://host`，丢弃 userinfo/path/query；request/response/error
  文本脱敏并二次扫描。capture 目录不得提交入库。

```text
ETHGUARD_WSS_ENDPOINTS=<comma-separated endpoints> \
  go run ./cmd/wsscapture -out /tmp/ethguard-wss-evidence-<date> \
  -endpoint-env ETHGUARD_WSS_ENDPOINTS -timeout 15s -chain 1

go run ./cmd/wsscapture -replay /tmp/ethguard-wss-evidence-<date>
```

## 2. 三轮观察与离线回放

| 日期/轮次 | 主请求记录与 chain checks | protocol error presence | `eth_call` policy checks |
| --- | --- | --- | --- |
| 2026-10-31 首轮：6 provider × 5 probe | 30 条，6 个 chain check | 24 帧：missing=13、null=0、empty-hex=1、value=10 | 12 |
| 2026-10-31 复测：8 endpoint × 6 probe | 48 条；2 endpoint 拨号失败，6 个 chain check | 30 帧：missing=16、null=0、empty-hex=1、value=13 | 12 |
| 2026-11-01 补采：3 provider × 6 probe | 18 条，3 个 chain check | 15 帧：missing=10、null=0、empty-hex=0、value=5 | 6 |

三轮分别回放，不跨轮合并为更多独立 source。补采复用了既有公开 endpoint，
参数为 `-timeout 10s -pause 750ms`，仓库外路径为
`/tmp/ethguard-wss-unified-evidence-20261101`。其余 capture 的完整路径/映射见
仓库外 index；本文不是可独立下载核验的 attestation。

### 2.1 首轮 presence 明细

| 形态 | 数量 | probe / provider |
| --- | --- | --- |
| missing | 13 | unknown-method：p01–p05；revert-without-reason：p01/p02/p04/p05/p06；invalid-params：p02/p03/p05 |
| null | 0 | 未观察到 |
| empty-hex | 1 | p03 的 revert-without-reason：`"data":"0x"` |
| value | 10 | 6 provider 的 revert-with-reason；p01/p04/p06 的 invalid-params 字符串 data；p06 的 unknown-method 结构化对象 data |

无 reason 的 revert 不等于 missing：同一类 probe 在 p03 是 `0x`，其余 provider
省略 data。invalid-params 的 data 可为字符串、对象或 missing；不能将某一拼写
升级为通用 provider 契约。六个 provider 的带 reason revert 均保留 `Error(string)`
bytes，只是该样本内结论。code（如 `-32601`、`-32003`）和 message 的 dialect
差异不进入比较键或 Decision。

### 2.2 连接与失败细节

- 首轮 p02 在 unknown-method 和 invalid-params 后 `1006` 异常关闭，后续同连接
  chain-id 失败；其他 provider 在这些 probe 后仍能完成 follow-up。
- 同日复测的两个 dial-error 为 HTTP 401 和约 15 秒握手 i/o timeout，未进入
  JSON-RPC；没有观察到握手 429、read limit、调用级响应超时或迟到响应。
  新增 block-hash-position 的拒绝 data 为 missing 或字符串，无 null；未观察到
  新 close code，也未重复首轮的 `1006`。
- 2026-11-01 的 18 个 follow-up 中 15 个成功；p02 在 unknown-method、invalid-params、
  block-hash-position 后为 `1006 unexpected EOF`。这是 `follow_up.close_code`，
  不是主请求顶层 `close_code`；只查顶层字段或 replay presence 摘要不能断言无断连。
  本轮未观察到握手 429、RPC 限流、read limit、响应超时或迟到响应。
- 每 probe 新连接，不观察持久会话的普遍可靠性。capture 客户端与 raw WSS adapter
  均不自动重连；provider 断连不授权 presence 工具改变连接生命周期或引入 replay。
  连接与 pending 归属见 [wss-wire-contract.md](wss-wire-contract.md)。

### 2.3 回放能证明什么

`cmd/wsscapture -replay` 不联网，逐错误帧经 `rpcwire.DecodeResponse` 检查
jsonrpc/ID/result/error envelope，再核对 `rpc.ErrorDataPresence` 与
`aggregator.ObservationRPCError` 的 `RPCError.DataPresence`。表中的错误帧均通过
当时的回放；missing 不改写为 null，null 也不改写为 missing。

`eth_call` 回放使用真实 `methods.CallPolicy`，以两个相同捕获错误构造两源观察：
`Decision.Status=Corroborated`、support=2；missing 的 data 维度弃权，`0x` 与具体
bytes 的 data 维度返回原值，missing/`0x`/value 不跨形态合并比较键；
`Decision.Value` 只按既有 rule 4e 处理。**复制同一捕获做两源离线回放只检查政策路径，
不证明真实 source 独立性或真实多源交叉验证。** 未新增比较键、弃权规则或生产语义。

## 3. 未关闭的证据门

| 项 | 实际证据与限制 |
| --- | --- |
| `error.data:null` | 三轮均为 0；不能用合成样本或进程内四态测试补为真实 provider null |
| WSS 握手 429 / RPC 限流 | 未观察；每 endpoint 仅 5–6 probe，不制造负载；已观察 HTTP 401 与握手 timeout 不等于 429 |
| read limit | 未触发；不向公开端点制造超过 adapter 上限的 frame |
| response timeout | 未触发；成功进入主请求的读取均在有界 deadline 内完成；与握手 timeout 分开 |
| late response on old connection | 未观察，未制造取消后旧响应条件 |
| auto-reconnect | capture/raw adapter 不实现；provider 侧“重连”不是本轮可观察项 |
| 其他方法形状 | blobBaseFee、syncing object、EIP-1898、feeHistory 舍入等仍由方法卡片登记，不能用本 presence 样本关闭 |

原阶段名 W4 的 **provider-backed 四态 golden 出口仍未满足**：进程内证据能证明
adapter 保留 missing/null/`0x`/具体 data，真实 frame 目前只证明 missing/`0x`/value。
2026-11-01 没有 `0x` 不推翻前两轮已有观察；没有 null 也不表示 provider 永远不返回
null。0056.2/0064 的生产决定不受该 golden 门阻塞，生产批准也不豁免真实证据门。
原 geth adapter 的 unknown 限制不能套给 raw WSS 解码。

本记录不关闭 T11/freshness，不证明长期兼容性或 provider 独立性，不生成 golden，
不改变默认 HTTP、pool 成员、生产裁决或写路径。后续只能以新的真实采集更新观察，
保留未观察项；不对公开 endpoint 制造负载，不用合成 capture 填缺口。
