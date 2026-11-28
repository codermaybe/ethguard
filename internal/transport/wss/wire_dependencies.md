# WSS wire 层依赖清单

本清单冻结 `internal/transport/wss` 的依赖边界；`dependencies_test.go` 是它的
可执行形式。规则来源：[wss-wire-contract.md](../../../docs/design/wss-wire-contract.md)
§0 与 §1。状态：2026-11-28 共享双工核分离；测试对端和生产依赖分别记录。

## 1. 允许的项目内部依赖（生产文件）

| 包 | 用途 |
| --- | --- |
| `internal/limits` | 入站消息上限（`MaxBodyBytes`），与 HTTP 层共用同一数字 |
| `internal/transport/streamrpc` | 共享请求关联、通知预算、订阅建立及优雅关闭／故障终止 |
| `internal/transport` | Port 表面（`Transport`/`Dialer`）与中立哨兵（`ErrConnectionClosed`、`ErrBodyTooLarge`） |

除此之外的任何项目内部包（internal/rpcengine、internal/aggregator、internal/methods、internal/projection、
internal/runtime、internal/reference、internal/typed、internal/pool 等）都禁止导入；该规则同时由根目录
`TestArchitectureDependencyInterlock` 的 WSS 条目锁定。

## 2. 允许的标准库依赖（wire 层文件）

握手与帧文件只使用 Go 标准库；`websocket_connection.go` 另引用中立
`internal/transport` 哨兵，向共享核报告关闭与大小超限，不依赖具体 RPC 状态。下表是当前实际使用的集合；新增标准库导入必须先
修订本清单（同一业务意图提交内），保持清单与代码同步：

| 层 | 标准库集合（当前实际） |
| --- | --- |
| 握手 | `bufio`、`context`、`crypto/rand`、`crypto/sha1`、`crypto/tls`、`encoding/base64`、`errors`、`fmt`、`net`、`net/http`、`net/url`、`os`、`strings`、`time` |
| 帧编解码 | `bufio`、`crypto/rand`、`encoding/binary`、`errors`、`fmt`、`io`、`unicode/utf8` |
| 连接生命周期 | `context`、`encoding/binary`、`errors`、`fmt`、`net`、`sync`、`time` |

adapter 文件 `wss.go` 使用 `context`、`fmt`、`net/url`、`time`。

`streamrpc` 生产文件仅允许标准库、`internal/rpcwire`、`internal/transport` 与 `rpc`；
禁止导入 WSS、IPC 或上层执行模块，根架构互锁同步约束。

## 3. 第三方 WebSocket 库

- 生产代码（非 `_test.go`）禁止导入任何第三方 WebSocket 库，包括但不限于：
  `gorilla/websocket`、`nhooyr.io/websocket`（`coder/websocket`）、
  `golang.org/x/net/websocket`、`fasthttp` 的 upgrade 实现。无过渡期例外
  。测试 gorilla_interop_test.go 可以使用 gorilla 作为独立对端，不进入生产依赖图。
- 测试对端使用自有实现：包内测试的 raw hijack server + 同包帧编解码，以及
  `internal/testkit/wstest`（仅测试可导入，架构互锁禁止生产包引用）。
- `cmd/wsscapture` 经 `wss.DialRaw` 使用同一自有 wire 层。
- go.mod 仍直接列出 gorilla/websocket，互操作测试使用它；不能把生产 wire 不链接第三方 WS 写成整个模块已移除依赖。默认生产与 WSS 依赖图按下文 interlock 校验。

## 4. 链接级边界

- `go list -deps ./internal/transport/wss` 不得包含 `go-ethereum/rpc`、
  `go-ethereum/ethclient`；
- 默认构建（根包依赖图）不链接 `ethclient`（既有
  `TestDefaultBuildExcludesEthclient` 继续生效）；
- WSS 生产链接约束：`go list -deps ./internal/transport/streamrpc ./internal/transport/wss ./cmd/wsscapture`
  不包含 `gorilla/websocket`。根包依赖图可能通过 geth 的其他算法包间接携带 gorilla，
  仅允许该既有来源；不得误写成整个默认构建图零 gorilla。两类边界分别由
  `TestWireLayerLinkGraph` 与 `TestDefaultBuildExcludesEthclient` 锁定。
