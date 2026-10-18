# T00 公共契约复核

> [ARCHIVED — 2026-09-07 历史快照，冻结不再维护] 验收结论是 T00 时点判断；当前状态以 `docs/decisions.md` 与后续验收记录为准。

日期：2026-09-07。结论：T00 已提交，整体验收待补正；以下是源码核对及规范审阅，不是提供者实测通过记录。不修改已接受 ADR，不批准第 6 节迁移提案。T01 的固定成员／多数规则可继续；T03 相关未决切片维持局部阻塞。

## 1. 公共契约与现状差距

区分 Ethereum RPC 协议、固定版本 ethclient 的 Go 行为、EthGuard 额外保证。方法同名同形、构建通过，不代表三个层面均兼容。

| 问题 | 可核对的实现事实 | 裁决与验收归属 |
| --- | --- | --- |
| 负数标签改写 | `ethadapter/client.go` 的 BlockByNumber 将所有负数交给 AnchorBlockHash；默认 resolver 为 latest，但配置其他 resolver 时并非 latest。toBlockNumArg 则直接编码负数 | 已确认兼容缺陷。T03 覆盖所有已知负数标签、未知负数、超范围负数；不得将非法参数变成成功读取其他块 |
| 标签等级丢失 | `rpc/anchor.go` 的 WithAnchor 将 latest/safe/finalized 交给同一个不接收标签的函数；多个位置也共用结果 | 已确认语义缺陷。同级候选与覆盖证据必须绑定原标签，不能以普通历史块存在证明 safe/finalized。T03 同时覆盖混合标签范围 |
| 调用参数被丢弃 | adapter 的 toCallArg 缺少 Geth v1.17.5 的 BlobGasFeeCap、BlobHashes、AuthorizationList 编码 | 已确认请求兼容缺陷。T08 覆盖 CallContract 与 EstimateGas；不能成功执行被删减参数的另一项请求 |
| canonical 约束丢失 | adapter 的 blockRefArg 返回裸 hash，丢弃 BlockNumberOrHash.RequireCanonical，且不识别 earliest 常量 | 已确认 Geth 入口兼容缺陷。T07 BlockReceipts 覆盖 requireCanonical=true/false、非 canonical 块及 earliest；共同参数设施归 T03 |
| hash 线格式依赖扩展 | WithAnchor 将状态读取块参数替换为裸 hash 字符串 | EIP-1898 对其适用方法规定对象形式。T03/T06 使用逐方法合法参数；厂商接受裸 hash 不能证明标准可移植性；日志过滤及仅高度方法另行处理 |
| revert 错误接口不兼容 | adapter.RevertError 仅实现 Error；decodeCallResult 丢弃 code，部分 data 不能保留。Geth RevertErrorData 依赖 rpc.Error/rpc.DataError | 已确认 Go 行为差距。T08 在不杜撰错误码／不擅自合并 data 的前提下保留可提取信息；对标准 Geth code=3＋hex data 测试原有提取方式 |

既有完整区块、null、固定分母等差距仍按原任务执行，不因本表未重复而关闭。

源码基准：本地 `github.com/ethereum/go-ethereum@v1.17.5` 的 `ethclient/ethclient.go`（toBlockNumArg、toCallArg、RevertErrorData、BlockReceipts）、`rpc/types.go`、`eth/api_backend.go`（BlockByNumberOrHash）。Geth 具体实现与协议要求分别记录，尤其不能把 EIP-1898 的方法范围直接扩展为所有方法。

协议参考：[EIP-1898](https://eips.ethereum.org/EIPS/eip-1898)。其对象引用保留 blockHash 和 requireCanonical；拒绝非 canonical 的约束不能在转换中丢失。

## 2. 迁移提案复评（逐项更新）

当前状态：第 6 节四项迁移方向均已按 0035–0037 接受。负数参数映射及缓存退出以 0037 为准，以下保留初审历史；当前目录和实现边界见 [architecture-record.md](architecture-record.md)。

2026-09-07 后续裁决：pending 方向已记入决策 0035 和 spec-baseline §6.1；resolver 按 0036 完整迁移，库统一建立标签感知参考，自定义扩展后期另议（§6.3）。下列建议保留初审历史，resolver 的保留旧接口候选已被 0036 替代；其余未决项不因本次批准而关闭。本文开头“不批准”描述初审动作，不覆盖此后逐项裁决。

- 6.1 pending：不能改成已出块读取；一律本地拒绝属于主动收窄支持面，并非修复成 ethclient 同等行为。建议先共同决定是否允许方法明确支持的 pending 以本地观测语义返回，永不因同值晋升 Corroborated。该路由必须由请求语义决定，不能由 quorum 失败触发；具体支持方法、来源选择及可见保证另行定案。
- 6.2 负数映射：方向合理，是恢复兼容。需同时修复 raw 路径、BlockNumberOrHash 和未知负数，不仅修改一个辅助函数。
- 6.3 自定义 resolver：保留旧接口可讨论，但旧接口不接收标签，不能单凭“用户负责”认为等级已保持。需标签感知能力或明确限定旧接口适用范围，并验证对应覆盖证据；不支持时不能悄悄替代。
- 6.4 缓存：属于 EthGuard 配置迁移，不是 Ethereum RPC 规范冲突。建议对实际启用旧跨调用缓存的配置显式报错；区分零值／禁用值与正 TTL，不应将所有出现过配置键的情况一律拒绝。最终规则待批准。

共同参考会牺牲新鲜度，这是已接受的语义增强，但也意味着不能宣传与单节点 ethclient 完全行为等价。应同时公开支持面、标签等级、参考选取方式及失败条件。

## 3. 轻客户端初审与证据补正

“不实现轻客户端”不等于违反轻客户端协议，但也不能据此将初审写成安全通过。

1. 补充分叉对应规则与精确 commit／文件锚点。不能以 living spec 为由用浮动内容替代固定版本；T12 可更新基线，但必须保留 T00 证据。本次未独立取得交付所列两个规范 tag 的固定内容，版本存在性与内容对应仍待交付者补证。
2. 执行负载的 execution_branch 绑定 BeaconBlockHeader.body_root；不要误写为 beacon state_root。EL 区块头的交易／withdrawals trie root 与 CL ExecutionPayloadHeader 的 SSZ roots 也不能直接等同。参考 [Capella light-client sync protocol](https://github.com/ethereum/consensus-specs/blob/master/specs/capella/light-client/sync-protocol.md)；此处浮动链接只用于定位纠错，交付基线须换为固定 commit。
3. UPDATE_TIMEOUT 的 force-update 是有已验证更新等前提的可选恢复路径，不能简化为“超时就证明最终性”。EthGuard 拒绝不足票与轻客户端验证签名的信任前提不同，删除“更保守所以一致”的安全比较。参考 [Altair light-client sync protocol](https://github.com/ethereum/consensus-specs/blob/master/specs/altair/light-client/sync-protocol.md)，固定版本要求同上。
4. 迁移示例只证明编译覆盖的调用。不支持的 ethclient 方法不能笼统称为可经 Unwrap 使用；根客户端能力、原始调用、订阅能力应分别说明。

验收补正应落回 spec-baseline 差距总账及对应任务，并提供受控请求捕获／错误兼容测试；真实提供者样本仍单独保持未验证。此次仅修改审阅文档和验收清单，未改代码、未重跑交付者所报 build/vet。
