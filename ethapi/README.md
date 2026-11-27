# ethapi：标准数据模型

`ethapi` 提供 Ethereum Execution APIs 的 Request／Response 类型、标量、严格 JSON 构造器与编解码。它不依赖网络传输、执行引擎或 geth；RPC 调用入口属于 `ethguard.Client`。使用已提交的模型不需要运行生成器。

```go
quantity, err := ethapi.NewQuantity(json.RawMessage(`"0x1"`))
```

公开 `New…` 构造器验证相应的有效 schema，包括字段存在性、null、数组、数值约束和组合分支。`"0x01"` 不是严格 QUANTITY，奇数位 DATA 也不合法。直接构造 Go 值须由调用者保持同样的不变量；`MarshalJSON` 编码这些值，不重新执行完整 schema 校验。

标准定义分两层：固定 commit 的官方原件保存其原始含义；显式审定的 Overlay 修正已知定义缺陷，并记录依据、目标和退出条件。生成模型忠实于“固定官方定义 + 项目审定修正”，不把修正冒称为原始官方要求。合法输入的输出表示政策也有单独记录，例如地址转小写。

Provider 的大小写或历史拼写兼容发生在调用端的具名准备步骤，之后仍须经过严格校验。请求绑定、交易／区块内容承诺和多源裁决属于下游方法合同，不是模型构造成功自动提供的保证。

下游消费保证按入口区分：typed Block／Header 要求相应完整模型；区块的 reference 分支只提供 number／hash 身份合同，通用 `Call(..., false)` 也能触达它，其裁决成功不意味着返回了完整官方 Block。三类分支的实际行为见[消费测试](../internal/verification/facade/standard_block_contract_test.go)。

| 材料 | 用途 |
| --- | --- |
| [固定来源](../internal/spec/README.md) | 官方版本、原件与向量身份 |
| [Manifest](../internal/executionapi/manifest.json) | 唯一生成范围、命名与表示契约 |
| [审计总账](types.audit.json) | 实际类型及所有来源、逐节点约束、Overlay 应用与覆盖；不推断运行时消费 |
| [完整工程合同](../docs/design/wire-codegen.md) | 维护者的提取、Overlay、编译、同步升级及验证流程 |
| [消费者契约测试](../internal/methods/standard_consumption_test.go) | 已注册读方法的实际执行与严格输出验证 |

维护者在仓库根目录离线更新生成物：

```sh
go generate ./ethapi
go test ./internal/executionapi ./internal/wiregen ./ethapi
```

上游同步、例外复核和完整发布门禁见工程合同；普通生成不会获取网络材料。
