# internal/ethcodec：共享标量原语、JSON 快路径与有限兼容契约

本包提供由 `ethapi` 生成代码、`internal/ethobject`、`internal/methods` 共用的底层标量严格校验、确定性 JSON 编解码与具名有限兼容原语。

## 职责边界与不变量

1. **零反向依赖**：本包不得导入 `ethapi`、执行引擎、传输层或 `go-ethereum`，依赖方向固定为 `methods/ethobject → ethapi → ethcodec`。
2. **严格原语与有限兼容物理隔离**：
   - `strict.go`（`IsCanonicalQuantity`、`IsCanonicalQuantity64`、`IsCanonicalQuantity256`、`IsWholeByteData`、`IsAddress`、`IsHexByte`、`IsStorageKey`、`IsCanonicalDecimal`）与 `fixed_hex.go`（`IsLowerFixedHex`）只执行与官方有效 schema 逐字符等价的 ASCII 严格检查，**绝不在其内部混入任何兼容转换**；
   - `compatibility.go` 中的 `Fold*` 函数均为具名、有限、显式调用的输入准备步骤，处理后的候选值必须继续进入严格原语或 `ethapi` 生成构造器；
   - `CanonicalQuantity` 保持“解码 → 具名大小写兼容 → 严格 `IsCanonicalQuantity` → 返回规范小写 hex”的单步语义，不再做无意义的 `big.Int` 十进制往返。
3. **JSON 字符串快路径边界（`jsonstring.go`）**：
   - `DecodeJSONString` 仅对首尾双引号且内部全部为 `0x20 <= c < 0x7f`（无 `"` 与 `\`）的完整 ASCII 字符串走拷贝快路径（返回独立拥有字节的 `string`）；
   - 任何转义、非 ASCII、控制字符、空白包裹或畸形输入一律回退 `encoding/json.Unmarshal`，确保 Unicode 替换与错误语义完全一致。
4. **精确 pattern 绑定原则**：
   - 编译器只对精确匹配的官方 pattern 绑定本包 ASCII 原语（例如 `Address` 允许混合大小写、`StorageKey` 允许 0–64 位、`HexByte` 的折叠受组合上下文控制）；任何新出现或近似 pattern 自动回退正则，不按类型名静默套用。

## 关联权威文档

- 标准模型与生成约束：[ethapi/README.md](../../ethapi/README.md)、[类型生成合同](../../docs/design/wire-codegen.md)
- 本阶段性能验证与差分基准：[性能优化终审交接](../../docs/design/performance-final-review.md)
