# ethapi：标准数据模型与设计约束

`ethapi` 是 EthGuard 的标准数据模型包，提供 Ethereum Execution APIs 的 Request／Response 类型、标量、严格 JSON 构造器与编解码。

- **职责边界**：只承载标准模型与严格校验，不承载网络 RPC 调用（归 `ethguard.Client`）、不依赖执行引擎或传输层、不引入 `go-ethereum` 类型（未来 geth 类型转换归独立 `ethadapter`）。
- **使用方式**：普通调用方直接使用已提交的 `types.gen.go`，无需运行生成器或联网。

```go
quantity, err := ethapi.NewQuantity(json.RawMessage(`"0x1"`))
```

## 设计原则与核心约束

1. **生成物只读**：`types.gen.go` 与 `types.audit.json` 由 `internal/wiregen` 离线生成，**严禁手工修改生成文件**，也不得在调用方手写平行 DTO 绕开本包。
2. **两层定义、权威清晰**：
   - **官方原件只读**（固定 commit 的 Execution APIs 定义）作为标准锚点；
   - **审定修正（Overlay）** 显式记录依据与退出条件，只在官方定义存在缺陷或滞后于已定案协议／主网事实时提供**临时覆盖**，不把项目修正冒称为原始官方要求；上游追平后须审阅退休。
3. **标准校验 vs. 表示政策 vs. 运行时兼容三者分离**：
   - **接受域（标准校验）**：公开 `New…` 构造器严格执行有效 schema（字段存在性、required／nullable、`oneOf`/`anyOf`、数组与精确数值约束）；例如 `"0x01"` 不是合法 QUANTITY、奇数位不是合法 DATA。
   - **表示政策**：仅作用于已通过严格校验的合法输入（例如地址小写化），绝不借表示转换放宽非法输入。
   - **有限兼容**：Provider 的大小写或历史拼写兼容属于调用端具名准备步骤，兼容产出仍须进入严格构造器；**前置兼容层永远不是规范**。
4. **不变量边界**：
   - 直接构造 Go 结构体须由调用者自行维持相同不变量；`MarshalJSON` 编码既有值，不承诺重新执行完整 schema 校验（保留 union 分支选择与开放请求 extras 校验）。
   - 模型构造通过只证明数据形态符合有效 schema，不替代请求绑定、区块／交易／收据内容承诺或多源裁决。
   - 下游消费保证按入口区分：typed Block／Header 要求相应完整模型；区块的 reference 分支只提供 number／hash 身份合同，通用 `Call(..., false)` 也能触达它，其裁决成功不意味着返回了完整官方 Block（见[消费测试](../internal/verification/facade/standard_block_contract_test.go)）。
5. **依赖与构建红线**：
   - 允许依赖方向固定为 `methods/ethobject → ethapi → ethcodec`；`ethcodec` 不得反向导入 `ethapi`，`internal/wiregen` 不得导入运行时执行包；
   - 生产运行时 `executionapi.Read()`／`ReadOriginal()` 零 Manifest 加载、零 `types.audit.json` 反读。

## 权威材料索引

| 材料 | 唯一职责 |
| --- | --- |
| [固定来源](../internal/spec/README.md) | 官方版本、原件与向量身份 |
| [生成契约 Manifest](../internal/executionapi/manifest.json) | 唯一人写的提取／生成范围、命名、别名与字段序契约 |
| [审定修正 Overlay](../internal/spec/errata/README.md) | 显式结构补丁、DATA 规则与退出条件 |
| [审计总账](types.audit.json) | 实际类型来源、逐节点约束、Overlay 应用与覆盖（不推断运行时消费） |
| [完整工程合同](../docs/design/wire-codegen.md) | 提取、Overlay、编译、离线／在线同步升级与门禁规范 |
| [消费者契约测试](../internal/methods/standard_consumption_test.go) | 已注册读方法的实际执行与严格输出验证 |

## 维护与验证

修改 Manifest、Overlay 或编译器后，在仓库根目录离线重生成并验证：

```sh
go generate ./ethapi
go test ./internal/executionapi ./internal/wiregen ./ethapi
```

普通生成全程离线且确定；上游同步（`cmd/specsync`）、双向差异检查（`cmd/specdiff`）与发布门禁见[完整工程合同](../docs/design/wire-codegen.md)。
