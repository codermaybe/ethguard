# WSS Provider Evidence（W4 证据门，2026-10-31）

状态：**两次真实 provider 证据已采集并离线回放（6×5 与 8×6）；出口未满足，
决策 0056 未修订**。本记录只登记 live observation 及其离线 replay，不把结果
升级为 provider 兼容性结论，也不生成 WSS 四态 golden。

## 1. 结论

- 真实 WSS provider 上观察到 `error.data` 的 **missing**、**`0x`** 和
  **具体 value** 三种形态；**`null` 未观察到**。2026-10-31 的第二次
  8 provider × 6 probe 复测仍未观察到 `null`。
- 24 个真实错误帧全部通过 `rpcwire.DecodeResponse` 解码；
  `rpc.ErrorDataPresence` 与 `aggregator.ObservationRPCError` 产生的
  `RPCError.DataPresence` 与捕获形态一致。
- 12 个 `eth_call` 错误帧在真实 `methods.CallPolicy` 下完成离线回放：
  两源支撑、缺失 data 弃权、`0x`/具体 data 返回、比较键不跨形态合并；
  `Decision.Value` 只按既有 rule 4e 处理，未出现新的裁决行为。
- 真实 provider 上未观察到显式 `data:null`。WSS 四态 golden 继续禁止；
  决策 0056 保持原状。进程内四态证据只能证明 Adapter 能保留四态，不能替代
  真实 provider 的 `null` 证据。
- 观察到 provider 在特定协议错误后直接断连；这支持“连接生命周期不能由
  presence 采集顺手改变”的既有边界，但不构成 reconnect 语义决策。

## 2. 方法与环境

- 日期：2026-10-31。
- 网络：Ethereum mainnet，`eth_chainId` 为 `0x1`；每个 provider 的 chain ID
  在回放中逐条校验。
- Provider：6 个公开、无需凭据的 WSS JSON-RPC gateway，记为 `p01`–`p06`。
  具体 host 只保存在仓库外的本地 capture index；本记录不写 endpoint。
- 客户端：`cmd/wsscapture`，原始 WebSocket JSON-RPC probe；Go 1.25.3，
  `github.com/gorilla/websocket v1.5.3`。capture 记录 commit、GOOS/GOARCH
  和 provider class `public-unauthenticated`。
- 每个 provider 发送 5 个只读 probe，每个 probe 使用新连接；错误后在同一
  连接上再发一次 `eth_chainId`，记录连接是否仍可用：
  - `chain-id`：`eth_chainId`；
  - `unknown-method`：未知方法，通常产生无 data 的错误；
  - `revert-with-reason`：USDC `transfer` 超余额，产生 Solidity
    `Error(string)` data；
  - `revert-without-reason`：WETH `transfer` 超余额，通常产生空 revert；
  - `invalid-params`：非法地址的 `eth_getBalance`，观察参数错误 envelope。
- `eth_call` 只读，不广播交易，不改变链上状态。
- capture 目录位于仓库外；工具只持久化 `scheme://host`，丢弃 userinfo、
  path、query，并对 request/response/error 文本做脱敏和二次扫描。命令：

  ```text
  ETHGUARD_WSS_ENDPOINTS=<comma-separated endpoints> \
    go run ./cmd/wsscapture -out /tmp/ethguard-wss-evidence-<date> \
    -endpoint-env ETHGUARD_WSS_ENDPOINTS -timeout 15s -chain 1

  go run ./cmd/wsscapture -replay /tmp/ethguard-wss-evidence-<date>
  ```

  该命令不进入默认测试或 CI；不要把 capture 目录提交到仓库。

## 3. Presence 矩阵

30 条记录（6 providers × 5 probes），其中 24 条是协议错误：

| 形态 | 数量 | 出现的 probe / provider |
| --- | --- | --- |
| `missing` | 13 | `unknown-method`（p01–p05）；`revert-without-reason`（p01/p02/p04/p05/p06）；`invalid-params`（p02/p03/p05） |
| `null` | 0 | 本轮未观察到 |
| `empty-hex` | 1 | p03 的 `revert-without-reason` 返回 `"data":"0x"` |
| `value` | 10 | 6 个 provider 的 `revert-with-reason` 返回具体 revert bytes；p01/p04/p06 的 `invalid-params` 返回字符串 data；p06 的 `unknown-method` 返回结构化对象 data |

观察到的 envelope 差异：

- 同样是无 reason 的 revert，p03 返回 `"data":"0x"`，其余 provider 省略
  `data`；因此“无 reason”不等于“provider 一定省略 data”。
- `invalid-params` 的 data 可能是字符串、对象或缺失；不能把某一种拼写当成
  通用 provider 契约。
- 具体 revert data 在 6 个 provider 上均出现，说明“provider 会保留
  `Error(string)` data”在这一样本内成立；这不是所有 provider 或所有错误码
  的普查。
- 错误 code 和 message 拼写存在差异（例如 `-32601`、`-32003` 和不同
  message）；这些仍属于 provider dialect，不进入比较键或 Decision。

### 3.1 2026-10-31 复测：8 provider × 6 probe

在同一天用同一个 opt-in 工具对 8 个公开 WSS endpoint 复测，新增
`block-hash-position` probe（EIP-1898 object 放在 `eth_getBlockByNumber`
的 block 位置），用于观察 provider 的拒绝 envelope 是否出现显式
`data:null`：

- 48 条记录：8 个 endpoint × 6 个 probe；其中 2 个 endpoint 在拨号阶段
  失败（`dial-error`，未进入 JSON-RPC；1 个 HTTP 401，1 个握手 i/o
  timeout），6 个 endpoint 完成 probe；没有观察到握手 429；
- 6 个 endpoint 的 chain check 全部通过；30 条协议错误中
  missing=16、`0x`=1、value=13、**null=0**；
- `block-hash-position` 的拒绝形态是 missing 或字符串 data，没有显式
  `null`；没有观察到新的 close code，也没有重复前一轮的 `1006`；
- 该复测只增加 live observation，不改变 W4 出口、决策 0056 或四态 golden
  禁令。

回放聚合：

```text
capture: 8 providers, 48 records, chain=1, class=public-unauthenticated, level=live-observation
chain checks: 6
call policy checks: 12
states: missing=16 null=0 empty-hex=1 value=13
unobserved state: null
W4 exit not met: decision 0056 is unchanged; WSS four-state goldens remain prohibited
```

## 4. 连接与生命周期观测

- p02 在 `unknown-method` 和 `invalid-params` 后以 close code `1006`
  异常关闭；同一连接上的后续 `eth_chainId` 失败。其他 provider 在这些
  probe 后仍能完成 follow-up `eth_chainId`。
- 2026-10-31 复测的两个 `dial-error` 分别是握手 HTTP 401 和握手 i/o
  timeout（约 15 秒）；本轮没有观察到 429、read limit、调用级响应超时或
  迟到响应。
- 每个 probe 使用新连接；本轮没有观察到 provider 主动重连，也没有观察到
  旧连接上的迟到响应。capture 客户端和 WSS Adapter 都明确不自动重连。
- 该断连行为是 provider 侧的生命周期事实，不应由 presence 采集或 Adapter
  静默改写；reconnect、pending 归属和旧响应处理仍是独立契约
  （`wss-lifecycle-extension-contract.md`）。

## 5. 离线回放

`cmd/wsscapture -replay` 不联网，执行：

1. `rpcwire.DecodeResponse` 解码每个真实错误帧，要求 jsonrpc/ID/result/error
   形状合法；24 个错误帧全部通过。
2. 断言解码后的 `rpc.ErrorDataPresence` 与捕获形态一致。
3. 断言 `aggregator.ObservationRPCError` 的 `RPCError.DataPresence` 与形态
   一致；missing 不被写成 null，null 也不被写成 missing。
4. 对 12 个 `eth_call` 错误帧，使用 `methods.CallPolicy` 和两个相同错误
   观察做离线回放：
   - `Decision.Status = Corroborated`，支持数为 2；
   - missing data 的 `data` 维度不返回；
   - `0x` 和具体 data 的 `data` 维度返回，值等于捕获 bytes；
   - missing / `0x` / value 的 `eth_call` 比较键不跨形态合并；
   - 没有新增比较键、弃权规则或 `Decision.Value` 语义。

回放结果：

```text
capture: 6 providers, 30 records, chain=1, class=public-unauthenticated, level=live-observation
chain checks: 6
call policy checks: 12
states: missing=13 null=0 empty-hex=1 value=10
unobserved state: null
W4 exit not met: decision 0056 is unchanged; WSS four-state goldens remain prohibited
```

第一次真实 run 的原始输出、provider 映射和完整 frame 在仓库外的 capture
目录；本记录只保留聚合结论。

## 6. 未观察项（不得用合成样本关闭）

| 项 | 状态 | 原因/影响 |
| --- | --- | --- |
| `error.data: null` | 未观察 | 公开 provider 本轮全部省略 data 或返回具体值；无法据此升级 0056 |
| rate limit（WSS 握手 429） | 未触发 | 本轮每个 provider 仅 5–6 个 probe，不制造负载；复测只观察到 1 个 HTTP 401 和 1 个握手 i/o timeout |
| read limit | 未触发 | 需要超过 Adapter 上限的 provider frame；不对公开端点制造超大请求 |
| response timeout | 未触发 | 需要 provider 停滞；本轮所有读取在有界 deadline 内完成 |
| late response on old connection | 未观察 | 需要 provider 在取消后仍发送旧响应；未制造该条件 |
| auto-reconnect | 不适用/未观察 | capture 客户端和 WSS Adapter 都不重连；provider 侧重连不是本轮可观察项 |
| 其他方法形状（blobBaseFee、syncing object、EIP-1898、feeHistory 舍入等） | 仍按方法卡片登记 | 本记录只采集 error.data presence；方法证据另行推进 |

## 7. W4 出口判定

W4 的出口要求真实 provider 与进程内证据都满足，才能修订决策 0056、增加
WSS 四态 golden，或明确继续禁止。本轮：

- 进程内：W2/W3 已证明 raw-call Adapter 可保留 missing/null/`0x`/具体 data；
- 真实 provider：missing、`0x`、具体 data 已被实际 frame 证实；**null 仍未
  被实际 frame 证实**；
- 因此出口**未满足**；决策 0056 不变，WSS 四态 golden 继续禁止。

这不是“W4 已关闭”。下一步可以寻找会显式返回 `data:null` 的真实 provider，
或由主维护者接受“null 仍不可知”的限制；在得到证据前不得用合成 provider
capture 关闭。

## 8. 回滚与影响

- 不改生产代码、默认 runtime、pool、aggregator、reference、typed、quorum
  或写路径；
- 新增内容仅为 `cmd/wsscapture` 手动证据工具和本文档；删除两者即可回滚；
- 决策 0056、WSS golden 禁令、WSS 生产化决策均保持不变。
