# WSS wire 层依赖清单（W1）

本清单冻结 `internal/transport/wss` 的依赖边界；`dependencies_test.go` 是它的
可执行形式。规则来源：[wss-wire-contract.md](../../../docs/design/wss-wire-contract.md)
§0 与 §1。状态：W7 已完成（2026-11-03），本清单为最终状态。

## 1. 允许的项目内部依赖（生产文件）

| 包 | 用途 |
| --- | --- |
| `internal/limits` | 入站消息上限（`MaxBodyBytes`），与 HTTP 层共用同一数字 |
| `internal/rpcwire` | JSON-RPC 请求编码、响应解码、协议错误与 presence（与 native HTTP 共用） |
| `internal/transport` | Port 表面（`Transport`/`Dialer`）与中立哨兵（`ErrConnectionClosed`、`ErrBodyTooLarge`） |
| `rpc` | `ParseHexQuantity`（ChainID probe 的数量解析） |

除此之外的任何项目内部包（aggregator、rpc/methods、runtime、reference、typed、
pool 等）都禁止导入；该规则同时由根目录
`TestArchitectureDependencyInterlock` 的 WSS 条目锁定。

## 2. 允许的标准库依赖（wire 层文件）

`websocket_handshake.go` / `websocket_frame.go` / `websocket_connection.go`
只允许导入 Go 标准库。下表是当前实际使用的集合；新增标准库导入必须先
修订本清单（同一业务意图提交内），保持清单与代码同步：

| 层 | 标准库集合（当前实际） |
| --- | --- |
| 握手 | `bufio`、`context`、`crypto/rand`、`crypto/sha1`、`crypto/tls`、`encoding/base64`、`errors`、`fmt`、`net`、`net/http`、`net/url`、`os`、`strings`、`time` |
| 帧编解码 | `bufio`、`crypto/rand`、`encoding/binary`、`errors`、`fmt`、`io`、`unicode/utf8` |
| 连接生命周期 | `context`、`encoding/binary`、`errors`、`fmt`、`net`、`sync`、`time` |

 adapter 文件 `wss.go` 额外使用 `bytes`、`context`、`encoding/json`、
`errors`、`fmt`、`math/big`、`net/url`、`reflect`、`sync`、`time`。

## 3. 第三方 WebSocket 库

- 生产代码（非 `_test.go`）禁止导入任何第三方 WebSocket 库，包括但不限于：
  `gorilla/websocket`、`nhooyr.io/websocket`（`coder/websocket`）、
  `golang.org/x/net/websocket`、`fasthttp` 的 upgrade 实现。无过渡期例外
  （2026-11-03 起 gorilla 已从生产与测试路径移除，W7 出口达成）。
- 测试对端使用自有实现：包内测试的 raw hijack server + 同包帧编解码，以及
  `internal/testkit/wstest`（仅测试可导入，架构互锁禁止生产包引用）。
- `cmd/wsscapture` 经 `wss.DialRaw` 使用同一自有 wire 层。
- go.mod 中 `gorilla/websocket` 仅作为 go-ethereum 兼容通道
  （`go-ethereum/rpc`）的 indirect 依赖残留；直接依赖已删除，默认构建与
  WSS 生产构建均不链接它。

## 4. 链接级边界

- `go list -deps ./internal/transport/wss` 不得包含 `go-ethereum/rpc`、
  `go-ethereum/ethclient`；
- 默认构建（根包依赖图）不链接 `ethclient`（既有
  `TestDefaultBuildExcludesEthclient` 继续生效）；
- W7 出口（2026-11-03 达成）：`go list -deps .` 与
  `go list -deps ./internal/transport/wss ./cmd/wsscapture` 的输出都不包含
  `gorilla/websocket`；该状态由 `TestWireLayerLinkGraph` 与
  `TestDefaultBuildExcludesEthclient` 锁定。
