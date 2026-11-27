# ethapi：前置增强 + 官方兜底

`types.gen.go` 是生成产物。理解它应先读前置增强、生成原则和官方定义；修改也从这三处开始，再重新生成。

## 两层职责

**前置增强**显式处理固定官方标准中的冲突和缺失。原始定义保持不变，修复或补充须说明来源、作用对象、修改内容、依据、验证和退出条件。所有未增强的定义由固定官方 schema 决定。

**官方兜底**由通用生成器忠实实现增强后的有效 schema：类型、字段、required、nullable、值约束和组合分支均来自定义。生成器不自行补方法规则，构造器不通过隐含转换扩大或缩小输入范围。合法值的表示政策须与接受域分开说明。

```text
固定官方原件 ── 显式修复／补充 ── 有效 schema
                                  │
                              通用生成原则
                                  │
                         types.gen.go + types.audit.json
```

运行时的 provider 输入兼容由方法前置处理负责；它处理实际输入，标准增强处理定义，两者分别审计。方法绑定、内容承诺和多源保证由原有方法／对象模块负责。

## 阅读入口

| 要了解什么 | 唯一入口 |
| --- | --- |
| 官方来源、版本与原始定义 | [internal/spec](../internal/spec/README.md)：`VERSION`、`SOURCE.json`、`methods.json`、`requests.json` |
| 已有前置修复 | [errata](../internal/spec/errata/) 与 [executionapi](../internal/executionapi/bundle.go)：`ReadOriginal` 保留原件，`Read` 形成有效定义 |
| 实际输入的有限兼容 | 各入口及 [ethobject](../internal/ethobject/)：选定字段后形成候选，保留原始来源证据；不混入标准勘误 |
| 生成范围与表示政策 | [policy.go](../internal/wiregen/policy.go) |
| schema 如何成为 Go 类型与构造校验 | [schema.go](../internal/wiregen/schema.go)、[model.go](../internal/wiregen/model.go)、[walk.go](../internal/wiregen/walk.go)、[emit.go](../internal/wiregen/emit.go) |
| 详细合同与独立验证要求 | [wire-codegen](../docs/design/wire-codegen.md) |

遇到生成缺失，先区分官方无定义、已有定义错误、尚未选择生成、生成器不支持约束。各自修复对应入口；缺少依据的定义保持未覆盖，不生成宽松替代。

## 修改与验证

修改前置规则或通用生成器后，在仓库根目录执行：

```sh
go generate ./ethapi
go test ./internal/executionapi ./internal/wiregen ./ethapi
```

`types.audit.json`记录来源、增强身份、覆盖和实际生成政策。验收同时检查原始／有效 schema 的独立预期、真实消费者以及重新生成的一致性；生成新鲜度和构造往返各自只证明部分合同。

公开 `New…` 构造器检查相应定义。直接构造 Go 值仍须保持相同不变量；该调用方式和编码职责详见生成合同。
