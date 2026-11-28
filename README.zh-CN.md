# EthGuard

[English](README.md) · **简体中文**

EthGuard 是一个面向 Go 的**默认 fail-closed 多源以太坊执行层 RPC 客户端**。它按方法合同对节点响应执行 schema 校验与适用的密码学内容承诺检查，为移动状态读取（`latest`、`safe`、`finalized`）建立单次调用内的共同参考，并在构造期固定的 HTTP、WebSocket 与本地 IPC 来源集合上完成裁决。

> **状态与保证边界**
>
> 统一构造、配置推导、原生 HTTP／raw WSS／Unix IPC 传输、生成 `ethapi` 标准模型以及锁定 `go-ethereum` v1.17.10 的 47 项 `ethadapter` 适配均已通过本地普通与 `-race` 验收；Windows named pipe 运行测试也已在 PR #13 的 `3fdb6b7` 提交 CI 中通过。
>
> `Corroborated` 表示**配置节点之间的交叉佐证（provider-backed cross-source evidence）**，不是以太坊共识、规范链成员、密码学最终性或运营者独立性的证明。
> 完整 `SimulateV1` 语义与真实 provider 证据仍为独立验收专题；本预发布不宣称完整兼容或稳定发布。详见[方法覆盖清单](docs/design/method-transport-coverage.md)。

---

## 安装

需要 Go `1.26.11`（详见 `go.mod`）。示例面向 `v0.0.1-beta.4`。

```sh
go get github.com/codermaybe/ethguard@v0.0.1-beta.4
```

---

## 快速上手（推荐从 `ethadapter` 开始）

已有 `go-ethereum` 或 `bind/v2` 应用可通过 `ethadapter` 沿用熟悉的业务方法签名，同时按下文处理校验、错误和订阅行为差异。`ethadapter.Client` 精确实现了 `ethclient.Client` 的全部 47 项业务方法（不含底层 `Client() *rpc.Client`），直接满足 `bind/v2.ContractBackend` 与 `bind/v2.DeployBackend`。

```go
package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/codermaybe/ethguard/ethadapter"
	"github.com/ethereum/go-ethereum/common"
)

func main() {
	// 传入 2 个及以上不同端点（HTTP / WSS / IPC 任意混用）自动进入多源 Majority 仲裁；
	// 传入 1 个端点（如 "/var/run/geth.ipc"）自动进入单源 Forwarded 模式。
	client, err := ethadapter.Dial(
		"https://rpc-a.example",
		"wss://rpc-b.example",
		"/var/run/geth.ipc",
	)
	if err != nil {
		log.Fatal(err)
	}
	defer client.Close() // 按应用生命周期共享一个客户端；连接惰性建立并复用。

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// 移动标签（nil / latest / safe / finalized）会先建立调用内共同参考再锚定读取。
	bal, err := client.BalanceAt(ctx, common.HexToAddress("0x000000000000000000000000000000000000dead"), nil)
	if err != nil {
		log.Print(err)
		return
	}
	fmt.Println("balance:", bal)
}
```

### 按需选择调用表面

底层永远只有**唯一执行所有者 `*ethguard.Client`**，上层提供三种形态，共享同一连接池与 `Close()` 生命周期：

| 调用表面 | 导入路径／访问方式 | 适用场景 | 返回形态 |
| --- | --- | --- | --- |
| **`ethadapter.Client`** | `.../ethguard/ethadapter` | 既有 `go-ethereum` / `bind/v2` 应用直接迁移 | 原生 geth 类型（`*types.Block`、`common.Address`、`ethereum.Subscription`） |
| **`ethguard.Client`** | `.../ethguard` | 需要逐次裁决证据、原生原子重组流、或零 geth 客户端依赖 | 生成 `ethapi` 模型 + `CallResult` + `error` |
| **`ethguard.EthClient`** | `core.EthClient()` | 使用 `ethapi` 标准模型且不关心单次 `CallResult` 的轻量调用方 | 生成 `ethapi` 模型 + `error` |

通过 `ethadapter.New(core)` 包装已有核心客户端，随时通过 `adapter.Core()` 回到原生 `*ethguard.Client`，不会新建第二套连接池。

---

## 常见使用方式

### 1. 构造：单源／多源统一规则、Go 选项与极简 YAML

所有构造入口遵循**同一条构造期确定规则**：

- **0 个端点**或规范化后存在**重复端点** → 启动期直接报错。
- **1 个端点** → 自动进入 `Forwarded` 模式（各方法的校验与完整性检查依然生效；成功读取的裁决诚实标为 `Forwarded`，绝不冒充 `Corroborated`）。
- **2 个及以上不同端点** → 自动进入 `Majority` 模式（固定投票分母，最低支持门槛为 2）。**运行期即使上游掉线，多源客户端也绝不自动降级为单源模式。**

构造默认采用**惰性连接**，后续调用按需建立连接；显式启用 `WithTipTracking` 会启动周期性后台探测。`DialContext`／`OpenContext` 仅在入口检查取消状态，不会将入参 `ctx` 绑定为客户端生命周期。`Dial` 不默认固定链 ID，需要时使用 `WithChainID` 或 YAML `chain_id`。

```go
// 多源 + Go 选项（链 ID 固定、结构化日志、finalized 锚定等，无需额外 import config）：
core, err := ethguard.Open(
	[]string{"https://rpc-a.example", "wss://rpc-b.example"},
	ethguard.WithChainID(1),
	ethguard.WithFinalizedAnchoring(), // 要求至少 2 个不同来源，单源构造期直接拒绝
)
if err != nil {
	return err
}
client := ethadapter.New(core)
defer client.Close()
```

或使用 YAML 配置——**`id` 与 `transport` 均可省略**：

```yaml
# config.yaml
chain_id: 1
upstreams:
  - endpoint: https://rpc-a.example    # 自动推导：native-http，id: rpc-1
  - endpoint: wss://rpc-b.example      # 自动推导：wss，id: rpc-2
  - endpoint: /var/run/geth.ipc        # 自动推导：ipc，id: rpc-3（支持 Unix socket 与 \\.\pipe\...）
```

```go
core, err := ethguard.LoadFile("config.yaml")
if err != nil {
	return err
}
client := ethadapter.New(core)
defer client.Close()
```

端点语法说明：

- **HTTP(S) / WS(S)**：带完整协议和主机的 URL（`http://`、`https://`、`ws://`、`wss://`）。
- **IPC**：明确的绝对路径（`/var/run/geth.ipc`）、明确的相对路径（`./geth.ipc`、`../geth.ipc`，构造期按当前 `cwd` 冻结为绝对目标）、`ipc:///abs/path` 或 Windows named pipe（`\\.\pipe\geth.ipc`）。有歧义的裸文件名（如 `geth.ipc`）与畸形 URL 均直接拒绝。

### 2. 配合 `bind/v2` 合约绑定与读取 Revert 数据

```go
// bind 导入自 github.com/ethereum/go-ethereum/accounts/abi/bind/v2。
// mytoken 由 abigen --v2 为提供 totalSupply() 的合约生成。
token := mytoken.NewMyToken()
instance := token.Instance(client, tokenAddr)
supply, err := bind.Call(instance, &bind.CallOpts{Context: ctx}, token.PackTotalSupply(), token.UnpackTotalSupply)
if err != nil {
	// 核心 RevertError 不伪造 RPC error code 3，请使用 ethadapter.RevertErrorData
	//（或 errors.As 读取 *ethguard.RevertError），而非 ethclient.RevertErrorData。
	if data, ok := ethadapter.RevertErrorData(err); ok {
		log.Printf("contract reverted: 0x%x", data)
	}
	return err
}
fmt.Println("total supply:", supply)
```

### 3. 审计日志与按需获取多源证据（`WithAudit`、`WithResult`、`Core()`）

在不破坏 `ethclient` 方法签名的前提下，随时观测多源投票结果、节点故障与类型转换省略清单：

```go
// 方式 A：全局结构化日志或自定义审计回调（WithAudit 优先于 WithLogger）：
client := ethadapter.New(core, ethadapter.WithLogger(slog.Default()))
// 或：
client = ethadapter.New(core, ethadapter.WithAudit(func(ctx context.Context, ev ethadapter.AuditEvent) {
	// ev.Method、ev.CallResult（核心 Decision/WriteDecision）、ev.ConversionReport、ev.CoreErr、ev.AdapterErr
}))

// 方式 B：单次调用捕获核心结果（外层 WithResult 绝不会被 ethadapter 遮蔽）：
base, err := ethguard.WithSourceEvidence(ctx, 64<<10) // 可选：按字节预算保留原始节点响应
if err != nil {
	return err
}
callCtx, capture := ethguard.WithResult(base)
block, err := client.BlockByNumber(callCtx, nil)
saved, published, captureErr := capture.Result()
if captureErr != nil {
	log.Printf("result capture: %v", captureErr)
} else if published && saved.Decision != nil {
	// saved.Decision 始终反映核心真实结论（即使适配器后置转换失败也不改写核心结果）。
	fmt.Printf("status=%s support=%d/%d\n",
		saved.Decision.Status, len(saved.Decision.Support), saved.Decision.Adjudication.Basis)
}
if err != nil {
	return err
}
fmt.Println("block:", block.Hash())
```

方法返回的错误仍是业务结论；capture 错误仅描述证据交付。每个 capture 只能用于一次调用。原始材料受字节、记录数与文本长度限制；截断不改变投票或门槛。

### 4. 订阅：geth Channel 桥 vs. 原生原子重组增量流

仅在构造期声明支持有界订阅的来源（**WSS** 与 **IPC**）参与订阅。多源客户端若只包含 1 个可订阅来源会本地拒绝，不静默降级为单源订阅。

**方式 A — 熟悉的 geth Channel 订阅（`ethadapter`）**

无需手填内存预算，内部使用经代表性大区块负载实测验收的默认策略。由于 geth channel 无法表达原子撤回，**一旦原生路径出现任何撤回（含零匹配日志块），桥接器确定性终止并在 `sub.Err()` 返回 `ethguard.ErrReorgDetected`**，由业务方执行回滚／回扫并重连。

传给 `Subscribe*` 的 context 仅约束建立阶段。已建立的桥通过 `Unsubscribe` 或 `Close` 停止；下方循环还会响应应用 context 结束。若关闭或其他故障已先确定终态，后到的撤回不会覆盖此前原因。

```go
headers := make(chan *types.Header, 16)
setupCtx, cancelSetup := context.WithTimeout(ctx, 10*time.Second)
sub, err := client.SubscribeNewHead(setupCtx, headers)
cancelSetup()
if err != nil {
	return err
}
defer sub.Unsubscribe() // client.Close() / client.Core().Close() 也会等待交付协程完全退出，即使未读取 headers。

for {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case h := <-headers:
		fmt.Println("new head:", h.Number, h.Hash())
	case err := <-sub.Err():
		if errors.Is(err, ethguard.ErrReorgDetected) {
			// 终止订阅不会自动撤销业务已处理的状态，请在此执行回滚或回扫。
			return handleReorgAndResync(ctx)
		}
		return err
	}
}
```

**方式 B — 原生原子重组增量流（`ethguard`）**

需要连续获取原子 `Retracted` + `Appended` 路径增量与结构化证据时，直接调用 `Core().SubscribeNewHead`、`SubscribeFilterLogs` 或 `SubscribeTransactionReceipts`。Logs／Receipts 流按需针对已接受的头验证完整内容承诺；这证明内容关系，不证明共识或最终性。在配置的恢复与资源边界内，重组可通过增量交付而无需终止流：

```go
query, err := ethapi.NewFilterQuery([]byte(`{}`)) // 原生 ethapi 查询，不是 ethereum.FilterQuery
if err != nil {
	return err
}
policy := ethguard.DefaultLogsSubscriptionPolicy() // 推荐默认预设，可按需微调单个字段
sub, err := client.Core().SubscribeFilterLogs(ctx, query, policy)
if err != nil {
	return err
}
defer sub.Close()

for {
	ev, err := sub.Next(ctx)
	if err != nil {
		return err
	}
	// 原子应用 ev.Path.Retracted 与 ev.Path.Appended。
	applyDelta(ev.Path.Retracted, ev.Path.Appended)
}
```

这些流从出生锚点开始，不自动补播更早历史；历史回填使用查询方法。

### 5. 直接使用原生 `ethguard.Client` 与内建诊断

若希望业务代码完全不引入 `go-ethereum` 客户端／适配器依赖，可直接使用根包 `ethguard` 与生成的 `ethapi` 标准模型：

```go
core, err := ethguard.Dial("https://rpc-a.example", "https://rpc-b.example")
if err != nil {
	return err
}
defer core.Close()

// 根方法返回 (业务值, CallResult, error)；core.EthClient() 返回 (业务值, error)。
bal, res, err := core.BalanceAt(ctx, ethapi.Address("0x000000000000000000000000000000000000dead"), nil)
_, _, _ = bal, res, err

// 内建只读诊断（不暴露底层 pool/transport 逃生口）：
snapshots := core.Sources()                           // 构造顺序的成员身份与健康快照
rawObs, sampleErr := core.Sample(ctx, "eth_chainId")  // 原始逐源 rpc.Response，不走裁决
_, _, _ = snapshots, rawObs, sampleErr
```

可运行示例：`go run ./examples/basic -config ./config/sample.yaml` 与 `go run ./examples/migration -config ./config/sample.yaml`。

---

## 核心保证与 `go-ethereum` 行为边界

常见查询、合约调用和已签名交易发送沿用熟悉的 `go-ethereum` 接口；在涉及多源一致性与安全性的位置保持显式 fail-closed 行为：

- **移动标签与共同参考**：`nil`／`latest`／`safe`／`finalized` 先在本次调用内建立参考，再锚定业务读取。多源下参考建立失败绝不发业务请求；单源下走同一阶段产生诚实的 `Forwarded` 参考。独立 `AnchorBlockHash*`／`AnchorBlockHeight` 始终要求 `Corroborated`。`WithFinalizedAnchoring` 将默认／`latest` 路由至 finalized 候选确认算法（要求至少 2 个来源）。
- **内容与密码学校验**：完整区块读取校验 header hash 与适用的交易、withdrawals、uncle 承诺；`ethadapter` 通过 `Decision.Provenance` 还原 `*types.Block` 的 uncle headers。普通收据／日志查询校验其合同规定的字段和请求绑定，不证明收据 trie 包含关系。Logs／Receipts 内容流额外针对已接受的头验证适用的根；`ReceiptsRoot` 未覆盖的字段（如 `effectiveGasPrice`）仍是按方法规则裁决的来源声明。非法输入、校验失败或支持不足返回转换错误或分类核心错误，详见[方法合同](docs/design/method-contracts.md)。
- **Pending 选择**：`PendingBalanceAt`、`PendingNonceAt`、`PendingStorageAt`、`PendingCodeAt`、`PendingCallContract` 与 `PendingTransactionCount` 作为来源本地观察执行（最高 `Selected`，绝不升级 `Corroborated`）。typed `BlockByNumber(-1)`、`HeaderByNumber(-1)`、`BlockReceipts` 与 `FilterLogs` 的 pending 本地拒绝。
- **写入与同步写入**：`SendTransaction`／`SendRawTransaction` 本地校验冻结签名字节、将节点返回 hash 绑定到本地提交 hash，向固定成员广播且绝不自动重放。`SendTransactionSync`／`SendRawTransactionSync` 执行一次广播加客户端侧收据轮询（在 `CallResult.ReceiptWait` 保留有界观察），不等同服务端 `eth_sendRawTransactionSync`。
- **生命周期**：任一表面调用 `Close()` 即停止共享客户端。调用已关闭的核心会在网络前返回 `ErrPoolClosed`；本地输入校验可能先拒绝请求。在途操作随之收敛；`ethadapter` 订阅接入同一关闭屏障，即使调用方永久不读 channel 也能完整清理并关闭 `Err()`。库绝不关闭调用方持有的订阅 channel。

---

## 模块地图与文档索引

| 包 | 职责 |
| --- | --- |
| `ethguard`（根包） | 公开 `Client`、`EthClient` 视图、`Dial`/`Open`/`New`/`LoadFile`、`CallResult`、`ResultCapture`、默认订阅策略、诊断（`Sources`/`Sample`） |
| `ethadapter` | 外围 `go-ethereum` v1.17.10 适配层（47 项业务方法、严格转换、`RevertErrorData`、可选 `WithAudit`/`WithLogger`、channel 订阅桥）；核心包永不反向导入 `ethadapter` |
| `ethapi` | 离线生成的 Execution APIs 标准模型与严格 JSON 构造器 |
| `rpc` / `config` / `observe` | 公开请求／响应／裁决数据合同、YAML／端点配置规范化、被动观测与健康快照 |
| `internal/runtime` | 唯一生产装配根（Composition Root） |
| `internal/{rpcengine,aggregator,methods,projection,pool,reference}` | 执行计划、规范处理、裁决算法、固定成员池、参考选择 |
| `internal/{ethobject,ethcodec,typed,uncertain,endpoint}` | 对象完整性与绑定单树、严格标量／JSON 原语、typed 投影、有界原始材料留存、端点解析 |
| `internal/transport/{http,streamrpc,wss,ipc}` 与 `internal/rpcwire` | 原生 HTTP、共享双工 RPC 核、自有 RFC6455 WSS、Unix／Windows IPC、共用 JSON-RPC 编解码 |
| `internal/subscription` | `Head`、`Logs`、`Receipts` 三类连续流的唯一执行 owner |
| `internal/diag/*`、`cmd/*`、`rpctest`、`internal/verification/*` | 诊断工具（`livecanary`、`specsync`、`specdiff`、`soak` 等）与公开验证测试集 |

完整现行合同入口见 **[文档导航](docs/design/README.md)**：

- [项目路线图](docs/design/project-roadmap.md) — 当前进度与独立未决专题
- [接入平面、IPC 与适配器合同](docs/design/access-plane-ipc-adapter.md) — 统一构造、IPC、`ethadapter` 与最终本地验收记录
- [官方语义与 geth 兼容政策](docs/design/go-ethereum-compatibility-policy.md) 与 [方法覆盖清单](docs/design/method-transport-coverage.md)
- [代码库拓扑](docs/design/codebase-topology.md)、[返回值合同](docs/design/return-value-design.md)、[Reference 合同](docs/design/reference-freshness-policy.md)、[订阅合同](docs/design/subscription-contract.md)、[Transport 合同](docs/design/transport-contract.md)、[WSS Wire 合同](docs/design/wss-wire-contract.md)、[生成合同](docs/design/wire-codegen.md)、[发布合同](docs/design/release-contract.md)

默认测试（`go test ./...`）全程离线；Anvil（`-tags anvil`）、公网 Canary 与发布门禁为显式命令，详见 [发布合同](docs/design/release-contract.md)。
