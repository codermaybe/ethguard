# 代码库拓扑迁移计划

状态：**M0–M5 已完成；M1.1 验证目录细分待后续**（2026-10-25）

本计划把已确定的目标拓扑转成可回滚、可审计的结构迁移。第一目标是降低维护者
掌握目录和分配任务的成本；迁移期间不重新定义 RPC 语义，不调整 quorum、Decision、
reference policy 或方法契约。

## 1. 迁移原则

1. **先形态，后深层重构**：先让目录表达 Module 和验证边界，再抽取 runtime、typed
   和 transport/reference seam。
2. **一个提交一个结构意图**：每个提交只完成一个可解释的移动、重命名或私有装配边界；
   不把业务修复混入结构提交。
3. **保持公共入口稳定**：root facade、`ethadapter` 的外部调用形状和公开返回类型保持
   不变；内部包路径变化只在仓库内同步完成。
4. **生产与验证分离**：跨模块、契约和场景验证逐步进入
   `internal/verification/{contracts,scenarios,integration,replay}`；必须访问未导出实现
   细节的白盒测试暂时留在生产 package。
5. **消费者拥有 Interface**：新 seam 由使用能力的 Module 定义。迁移阶段不为“未来可能
   需要”添加宽接口。
6. **可回滚**：每个阶段结束时都能单独通过全量测试，且可以回退整个阶段，不依赖后续
   阶段的未完成代码。

## 2. 官方工具与文件操作边界

### 2.1 适合使用 `gopls` 的操作

当迁移改变 Go package 声明或 import path 时，优先使用官方 `gopls` 的 Rename 能力，
让工具同步更新 package 声明、仓库内 import 和引用。它适合“整个 package 改名或移动”
这种闭合操作。

当前环境若未安装 `gopls`，先安装与仓库 Go 版本兼容的版本；安装失败时不得用未经
检查的批量替换冒险改写 import，应退回 `go list` + 精确搜索 + 编译验证的方式。

### 2.2 适合使用 `git mv` 的操作

同一 package 内仅改变文件目录位置、文件名或测试文件归属时，使用 `git mv`，因为
package import path 没变，工具不需要重写引用。完成后用 `git diff --find-renames` 检查
Git 识别为移动而非删除/重写。

### 2.3 不能伪装成文件移动的操作

把测试从生产 package 移入 `internal/verification` 会改变 package 边界，不能只执行
文件移动。每个测试文件先标记为：

- 白盒测试：依赖未导出符号，继续留在生产 package 或改造成小型局部测试；
- seam/契约测试：通过公开入口，移入对应 verification 子目录；
- 共享 fixture：移入 `internal/testkit`，但生产代码不得导入它。

这种变更必须单独提交，避免与生产装配抽取混在一起。

## 3. 阶段与验收门

### M0：基线冻结和库存

目标是建立迁移前事实，不改 Go 代码。

产物：

- 当前 package、测试文件、公共入口和跨目录 import 清单；
- 目标 Module 与当前文件的映射表；
- 需要静态守卫的禁止依赖清单；
- 一组可重复的基线命令和输出摘要。

验收：

```text
go test ./... -count=1
go vet ./...
go test ./... -race -count=1
git diff --check
```

M0 不提交任何生成的 capture、golden 或 testdata。

### M1：验证拓扑迁移

这是第一个实际迁移阶段，因为它直接降低目录认知负担，且不触碰运行时装配。

步骤：

1. 识别 T09 及其他跨模块测试中依赖公开 seam 的文件，按 `contracts`、`scenarios`、
   `integration`、`replay` 分类；
2. 将共享 fixture 和测试 Adapter 放入 `internal/testkit`；
3. 使用 `git mv` 搬运文件，再逐个修正 package 声明和 import；
4. 对必须访问未导出实现的白盒测试保留原位，并在文件注释中说明原因；
5. 删除测试对生产内部摘要解码、隐藏 runner 或测试侧第二套 typed 逻辑的依赖；
6. 给 verification 目录建立最小 README 或 package 注释，说明它验证哪条 seam。

首个安全切片可以先把共享大量 fixture 的 root 外部测试整体放入
`internal/verification/integration`，保持同一个 `ethguard_test` package。不要为了立即
填满 `contracts`、`scenarios` 目录而先导出一批测试专用 API；等 `internal/testkit` 和
公开 seam 稳定后，再执行 M1.1，将 T09 等测试按验证目标细分到 `contracts` 与
`scenarios`。这属于验证拓扑的二次整理，不阻塞第一阶段的生产/验证分离。

验收：

- 生产 package 不导入 `internal/verification` 或 `internal/testkit`；
- T09 的九场景和字段守恒覆盖保持不变；
- 测试名称可以使用任务编号，但永久目录不使用 T09 作为拓扑名称；
- `go test ./... -count=1`、`go vet ./...`、必要的 race 和 T09 shuffle 复跑通过；
- `git diff --find-renames` 能识别主要文件为移动。

### M2：root Composition Root 形态

目标是把装配知识集中到 `internal/runtime`，但先不改变执行顺序。

步骤：

1. 从 root facade 中识别 pool、aggregator、methods、reference、typed 的构造和绑定；
2. 在 `internal/runtime` 建立唯一内部构造入口；
3. root facade 改为调用 runtime 返回的内部实现；
4. 保留 root facade 的公开方法、错误形状、Decision 和 typed 返回类型；
5. 给 runtime 增加只验证装配闭环的局部测试，不把方法语义复制到 runtime。

禁止：在 runtime 中重新实现方法 policy、字段比较、quorum 或 reference 算法；
禁止创建第二套 client 或第二套 decoder。

验收：

- `internal/runtime` 是唯一 Composition Root；
- `ethadapter` 仍只委托 root facade；
- runtime 不成为半公开 Client，不暴露业务方法；
- 现有全量、race 和场景测试通过。

### M3：Typed seam 收敛

目标是让 root facade 与 `ethadapter` 共享一个生产 typed decoder/projection。

步骤：

1. 列出现有所有 Decision.Value 到公开 Go 类型的生产解码路径；
2. 将共用解码和表示投影移动到 `internal/typed`；
3. root facade 和 `ethadapter` 改为调用这一实现；
4. 保留各方法 concrete return type，不引入公共 `TypedValue`；
5. 把表示稳定性、无损往返和 typed 一致性测试归入 `verification/contracts`，仅保留
   必要的白盒局部测试。

验收：

- 全库只有一条生产 typed projection 路径；
- ethadapter 不再拥有独立 decode/装配逻辑；
- typed seam 不依赖 source collection、quorum 或网络传输；
- 公开结果和错误语义保持不变。

### M4：Transport 与 Reference seam

目标是建立可替换的最小 Transport Port 和 Head-Reading Port，不扩展运行时能力。

步骤：

1. 在 `pool` 中由消费者定义最小 Transport Port；
2. 将现有 HTTP/geth client 接入为 Adapter，避免具体客户端类型进入 aggregator、rpc
   或 methods；
3. 在 `internal/reference` 中定义最小 Head-Reading Port；
4. 将 reference 建立算法改为消费该 Port；方法 policy 绑定仍由 runtime 完成；
5. 用 fake Transport 和 mock Head-Reading Port 做 seam 烟雾验证。

Port 明确不承载：subscription、filter、供应商私有类型、完整 block policy、业务 method
绑定、admit、quorum 或其他裁决逻辑。

验收：

- fake Adapter 可以驱动 pool → aggregator 路径；
- mock Head-Reading Port 可以独立验证 reference 建立；
- Transport Port 与 Head-Reading Port 不包含方法 policy 组装或业务裁决；
- 不改变 HTTP/WSS 的既有行为，不在此阶段新增网络能力。

### M5：守卫、文档和收口

最后才更新 README、路线图和 CI 守卫，使文档反映已经落地的结构，而不是提前声明。

至少加入以下静态检查：

- `rpc` 不依赖 `rpc/methods`；
- 生产代码不依赖 `internal/verification` 或 `internal/testkit`；
- `ethadapter` 不依赖 `internal/runtime`；
- `internal/runtime` 是唯一 Composition Root；
- 生产代码不存在第二套 typed decoder。

## 4. 每个迁移提交的固定检查

每个提交在提交前运行与变更相称的检查；阶段末运行全套检查：

```text
gofmt -w <changed-go-files>
go test ./... -count=1
go vet ./...
go test ./... -race -count=1
git diff --check
git diff --find-renames --stat
```

T09 或其他场景矩阵变更时，追加：

```text
go test . -run 'TestT09' -race -count=2 -shuffle=on
```

所有测试保持离线；不写入 `capture/`、`golden/` 或 `testdata/`。如果一个结构提交导致
行为测试失败，先回退该提交并定位边界，不在同一提交中顺手修改业务语义。

## 5. 首个执行切片

首个 agent 只执行 **M0 + M1 的库存和最小验证迁移**：

- 不触碰 root、rpc、pool、aggregator 的生产实现；
- 先输出文件分类和迁移清单，再移动第一批确认属于公开 seam 的测试；
- 保留白盒测试，遇到归属不确定的文件列为待审项，不擅自跨边界；
- 完成后提交一个独立结构提交，并附命令、失败路径、网络状态和未迁移清单；
- 通过阶段门后，才进入 M2。

这样可以先验证“目录即设计”的迁移方法，再让后续 agent 处理 runtime 和 seam，而不会
在一次大规模重排中同时引入装配、接口和语义风险。
