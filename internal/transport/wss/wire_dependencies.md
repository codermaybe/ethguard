# WSS wire 层依赖清单（W1）

本清单冻结 `internal/transport/wss` 的依赖边界；`dependencies_test.go` 是它的
可执行形式。规则来源：[wss-wire-contract.md](../../../docs/design/wss-wire-contract.md)
§0 与 §1。

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
只允许导入 Go 标准库。按层：

| 层 | 标准库集合 |
| --- | --- |
| 握手 | `bufio`、`context`、`crypto/rand`、`crypto/sha1`、`crypto/tls`、`encoding/base64`、`encoding/hex`、`errors`、`fmt`、`io`、`net`、`net/http`、`net/http/httptrace`（如需）、`net/url`、`strconv`、`strings`、`time` |
| 帧编解码 | `bufio`、`crypto/rand`、`encoding/binary`、`errors`、`fmt`、`io`、`math/bits`（如需）、`sync`、`unicode/utf8` |
| 连接生命周期 | `context`、`encoding/json`、`errors`、`fmt`、`io`、`net`、`sync`、`time` |

 adapter 文件 `wss.go` 额外允许 `encoding/json`、`math/big`、`net/url`、
`reflect`、`bytes` 等既有标准库导入。

## 3. 第三方 WebSocket 库

- 生产代码（非 `_test.go`）禁止导入任何第三方 WebSocket 库，包括但不限于：
  `gorilla/websocket`、`nhooyr.io/websocket`（`coder/websocket`）、
  `golang.org/x/net/websocket`、`fasthttp` 的 upgrade 实现。
- 过渡期例外：遗留 adapter `wss.go` 在 W5 之前仍绑定 gorilla；W5 换用自有
  handshake/frame/connection 后该例外自动消失（测试按文件名放行，改绑后
  任何生产文件的第三方导入都会失败）。
- 测试文件（`_test.go`）过渡期允许 gorilla 作为 server-side peer 与 W6 差分
  oracle；W7 移除依赖后不得再出现。
- `cmd/wsscapture` 在 W7 前允许 gorilla；W7 改绑自有 wire 层。

## 4. 链接级边界

- `go list -deps ./internal/transport/wss` 不得包含 `go-ethereum/rpc`、
  `go-ethereum/ethclient`；
- 默认构建（根包依赖图）不链接 `ethclient`（既有
  `TestDefaultBuildExcludesEthclient` 继续生效）；
- W7 出口：`go list -deps .` 与 `go list -deps ./internal/transport/wss
  ./cmd/wsscapture` 的输出都不包含 `gorilla/websocket`。
