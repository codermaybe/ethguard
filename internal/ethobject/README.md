# internal/ethobject：以太坊对象准备、完整性与规范编码契约

本目录是 EthGuard 唯一的以太坊业务对象树，承担 `header`、`block`、`transaction`、`receipt`、`log`、`accesslist`、`feehistory`、`syncing` 的输入兼容准备、完整性／内容承诺检查、请求绑定与规范字节编码，并由 `wiremask`、`hexcompat` 提供受控前置投影。

## 职责边界与不变量

1. **三层分工（不得越界）**：
   - **本目录（准备与对象完整性）**：负责原始 provider 输入的具名有限兼容、字段存在性与别名语义、密码学承诺（header 自哈希、交易／收据 trie、logsBloom、签名与 sender 声明）及请求绑定；
   - **`ethapi`（标准模型与严格校验）**：独占有效 schema 的结构与标量规则（`New*` 验 JSON、`Validate*` 验 Go 值），不得在本目录维护第二套平行标准校验；
   - **`internal/methods` / `internal/rpcengine` / `internal/typed`**：分别承担方法注册装配、多源裁决与公开 Go 值交付。本目录**严禁反向导入**上述执行或门面层。
2. **输入态与值态分离**：
   - 准备层负责原始 JSON 输入语义（例如 `log` 的精确 key 准入 vs. 大小写别名编码顺序、`accesslist` 的 provider 扩展投影与逐项错误顺序）；
   - 已试点接入方法根值验证的对象（当前为 `accesslist` 的 `ethapi.ValidateCreateAccessListResult`）须按“兼容准备 → 组装强类型值 → 方法根值验证 → 单次 `MarshalJSON`”执行，不再通过 `Marshal → New*` 往返做根复检；值验证不替代原始输入语义检查或业务绑定。
3. **JSON 安全门与字节所有权**：
   - **公开入口永远自保**：`Validate`、`Encode`、`EncodeBlock`、`DecodeWithCanonical` 等公开函数入口必须执行 `jsonsafe.Validate`；
   - **受控复用仅限同一份未改写字节**：父集合入口（如 `log.Encode`、`receipt.EncodeBlock`、`receipt.VerifyBlockRows`）在整包通过 `jsonsafe.Validate` 后，子元素可调用 `*JSONChecked` 内部入口复用该安全证明；凡经过重新编码／转义生成的中间或最终字节，不得继承原输入预算证明，必须另行检查。
4. **复用产物而不破坏错误阶段**：
   - 内部可复用同一轮已验证产物（例如 `header.DecodeWithCanonical` 供 `block.AdmitBlock` 复用、`transaction.validatePrepared` 返回局部 `validatedContext` 供同次绑定复用、`receipt.canonicalBloom` 使用栈上 `types.Bloom` 增量计算），但不得为减少解析而合并冻结的不同语义阶段（如 `log` 精确 key 准入与别名编码／`blockTimestamp` 编码错误、`block` 准入与 `fullTx=false` 编码阶段）。
5. **不引入跨调用缓存或隐藏修复**：
   - 所有 `Candidate` / `FullBlock` / `validatedContext` 均为单次调用局部不可变载体；对外返回切片须复制或隔离所有权，防止调用方改写已验证字节。

## 关联权威文档

- 架构依赖与执行路线：[代码库拓扑](../../docs/design/codebase-topology.md)
- 标量与生成校验契约：[ethapi/README.md](../../ethapi/README.md)、[类型生成合同](../../docs/design/wire-codegen.md)
- 本阶段性能验证与保留边界：[性能优化终审交接](../../docs/design/performance-final-review.md)、[分阶段验证](../../docs/design/performance-verification.md)
