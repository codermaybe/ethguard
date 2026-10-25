# root facade 与 `ethadapter` 对接边界审查

状态：**保持 Adapter 局部实现，不抽第二层公共转发 Module**（2026-10-25）

本文审查 root facade 和 `ethadapter` 的调用形状、返回形状、typed projection 与
错误转换。结论只决定 Adapter 的 Interface 和实现归属，不改变公开方法签名、
Decision、quorum、reference policy 或 typed 语义。

## 当前职责分工

root facade `ethguard.Client` 提供：

- 配置、Option、生命周期和 generic `Call`；
- 带 `Decision` 的 root typed methods；
- anchor/reference、pool、broadcast 和不确定数据入口；
- 对 `internal/typed` 的公共委托。

`ethadapter.Client` 是 ethclient-shaped Adapter：

- 通过 `ethguard.New`/`Wrap` 委托 root Client 的生命周期；
- 将 Geth 的 `*big.Int`、`BlockNumberOrHash`、`CallMsg`、`FilterQuery` 等输入形状
  转换为 root `Call` 能接受的 RPC 参数；
- 隐藏 `Decision`，只返回 ethclient 形状的值或错误；
- 将 root Decision/Value 投影为 Geth 类型，并保留 `ethereum.NotFound`、
  `RevertError` 和 EthGuard sentinel 的兼容语义。

这两个表面共享同一条生产 typed projection：root 的 `Decode*` 委托
`internal/typed`，adapter 也直接调用同一个 `internal/typed` 实现。直接调用共享
实现不是第二套 decoder，而是同一 Seam 的两个调用方。

## 调用形状分类

### 已有 root 形状足够的委托

这些方法直接委托 root Client，避免 adapter 重复实现业务语义：

- `ChainID`；
- `BlockByHash`；
- `BlockNumber`；
- `TransactionReceipt`；
- `SuggestGasPrice`、`SuggestGasTipCap`；
- `PeerCount`、`SyncProgress`。

它们的 adapter 逻辑只增加 context policy 和返回形状隐藏，属于薄 Adapter。

### 必须由 adapter 保留的形状转换

以下实现不能简单搬到 root facade 或抽成无语义的公共 forwarding package：

- `BalanceAt`、`NonceAt`、`StorageAt`、`CodeAt`：Geth 的参数类型需要转换为
  EIP-1898/quantity RPC 参数，并通过 `CallInto` 隐藏 Decision；
- `BlockByNumber`、`BlockReceipts`：Geth 的负 block number、hash/number/tag 和
  `requireCanonical` 需要保留调用方形状，adapter 还要绑定返回对象的 hash/height；
- `CallContract`、`EstimateGas`：`CallMsg` 的 wire 字段和 `RevertError` 属于
  ethclient 兼容层；
- `FilterLogs`：`FilterQuery` 的 address/topics/range/blockHash 形状与方法 policy
  的 filter wire 不同；
- `TransactionByHash`：adapter 需要返回 `(transaction, pending, error)`，root
  facade 的 Decision 结果不具有该调用形状。

这些函数的实现是 Adapter 的深度：输入协议转换、调用形状约束、返回对象绑定和错误
兼容性都隐藏在一个小的 ethclient-shaped Interface 后面。把它们拆成公共转发包会
让调用方同时掌握 Geth 形状、root 参数形状和 Decision 规则，降低 Depth 和 Locality。

## 不抽取的重复形状

以下 helper 保持在 `ethadapter`：

- `toCallArg`、`toFilterArg`、`blockRefArg`、`checkedBlockNumArg`；
- `bindReceiptsToHash`、`blockAt`；
- `decodeCallResult`、`decodeCallDecision` 和 `RevertError`。

它们只服务 Adapter 的外部协议形状或兼容性错误，不是 root 与 adapter 之间共享的
业务事实来源。抽到 root 会让 root facade 获得 Geth-specific 知识；抽到新 package
则会产生只有一个 Adapter 实现的浅层 Seam。两个 Adapter 出现之前，不建立新的
可替换 Interface。

## 保持的硬边界

1. `ethadapter` 只委托 root Client 或共享 `internal/typed`，不依赖
   `internal/runtime`；
2. adapter 不参与 Prepare、admission、quorum、conflict 或 reference 选择；
3. root facade 和 adapter 不创建第二套 typed decoder、policy registry 或执行链；
4. adapter 的 Geth 参数转换、返回形状和错误兼容性可以独立测试，但生产裁决仍由
   root/aggregator/runtime 拥有；
5. 新增 ethclient-shaped 方法时，先判断 root 是否已有完全匹配的 typed method；
   能委托就委托，只有形状或兼容性确实不同才在 adapter 增加局部转换；
6. 不为了减少文件行数建立 root↔adapter forwarding package。

## 重新审查条件

只有出现以下证据，才考虑抽取新的 Adapter seam：

- 第二个外部调用形状需要复用同一套参数/返回转换；
- 同一转换规则被 root facade 和至少两个 Adapter 独立实现；
- 当前 `RevertError`、BlockNumberOrHash 或 FilterQuery 转换需要被另一个外部
  Adapter 复用，并且可以定义小而稳定的 Interface；
- root facade 与 adapter 的 typed projection 出现行为漂移，而共享
  `internal/typed` 无法通过现有 seam 修复。

在满足条件之前，下一步只做 adapter 内部的命名和局部重复清理，不改变调用形状、
错误兼容性或 root/adapter 的单向依赖。
