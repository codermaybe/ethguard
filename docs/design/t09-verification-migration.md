# T09 验证代码迁移前置契约

状态：**前置库存完成，等待语义化重命名与局部收敛**（2026-10-25）

本文是交给后续 agent 的工作边界。它不改变 T09 的方法矩阵、字段生命周期、
Decision、quorum、typed projection 或 reference 语义。

## 当前库存

T09 代码全部位于 `internal/verification/integration`，保持同一个
`ethguard_test` package。当前有 7 个文件、约 4,770 行和 45 个测试入口：

| 当前文件 | 责任 | 建议语义文件名 |
| --- | --- | --- |
| `t09_scenarios_test.go` | T09.1 方法场景、请求形态和矩阵覆盖 | `method_scenarios_test.go` |
| `t09_matrix_helpers_test.go` | 场景行执行器、Decision 维度断言 | `scenario_harness_test.go` |
| `t09_fixtures_test.go` | 链、交易、区块、fleet 和响应变体 fixture | `verification_fixtures_test.go` |
| `t09_fields_test.go` | 字段生命周期、表示稳定性和 typed round-trip | `field_lifecycle_test.go` |
| `t09_mutations_test.go` | 受保护字段 mutation/admission 边界 | `protected_mutations_test.go` |
| `t09_conservation_test.go` | Value、Diagnostics 和每个 observation 的守恒 | `evidence_conservation_test.go` |
| `t09_typed_test.go` | 生产 Client/ethadapter typed probes 和摘要 | `typed_projection_test.go` |

这些文件共同依赖同一组测试 fixture。第一轮只在当前 package 内移动/重命名，
不要把某个 helper 单独移到生产包，也不要为了目录整齐立即创建测试专用公共 API。

## 适合 Flash agent 的四个切片

### A. 语义化文件重命名

按上表使用 `git mv`，只改变文件名和文件头注释。先不改变 package、生产 import
或测试行为。完成后用 `git diff --find-renames --stat` 确认 Git 识别为移动。

### B. 测试辅助代码局部收敛

在同一个测试 package 内减少重复的场景断言、typed 摘要和 fixture 调用样式。
优化必须保持每个方法场景显式写出 status、participants、support、faults、
representative、Value 和 typed 结果；不能把场景矩阵重新隐藏到通用循环里。

允许改进命名、提取小型纯测试 helper、合并明显重复的断言；不允许引入第二套
typed decoder、从源码推导 expected value、缩小固定 fan-out 分母或改变错误断言。

### C. 按验证 seam 准备后续拆分

先记录每个 helper 的消费者，再决定是否进入未来的 `internal/testkit`。只有同时被
多个验证 Module 使用的 fixture 才有资格外提。当前 T09 fixture 主要服务 integration
package，不能仅因为文件较大就外提。

后续目录的意图是：

```text
internal/verification/contracts/   # 方法契约、编码、typed 一致性
internal/verification/scenarios/   # 决策场景和字段生命周期矩阵
internal/verification/integration/ # facade + pool + aggregator 联合验证
internal/verification/replay/      # capture/golden 离线回放
```

本切片只建立分类和依赖清单；如果拆包会迫使生产代码导出测试专用符号，应暂停并
把该项列为待审，而不是继续扩大 Interface。

### D. 验收门和文档同步

重命名或局部优化后同步测试入口说明和运行命令。保留 T09 专项门，直到所有测试
函数名也完成语义化迁移并相应更新命令；不要让现有验证命令静默失效。

每个切片至少运行：

```text
gofmt -w <changed-go-files>
go test ./internal/verification/integration -run 'TestT09' -count=1
go test ./... -count=1
go vet ./...
git diff --check
git diff --find-renames --stat
```

阶段收口仍需运行：

```text
go test ./... -race -count=1
go test ./internal/verification/integration -run 'TestT09' -race -count=2 -shuffle=on
```

## 不变量和停止条件

后续 agent 可以重命名测试文件、局部 helper 和测试入口，但必须保持：

1. `Corroborated`、`Selected`、`Forwarded`、`Unknown` 的分类不变；
2. 固定 T09 基础 `N=5`、`H=2`、`Q=3` 和完整参与分母不变；
3. 失败、admission fault、normalization fault 和 diagnostics 的归属不变；
4. `Decision.Value` 只来自生产 projection，测试不能复制 typed 解码路径；
5. 普通测试不联网，不写入 capture、golden 或 testdata；
6. 任何需要改生产 Interface、quorum、方法 policy 或 reference 算法的发现都停止在
   当前切片，单独提交设计和实现。

## 已验证基线

前置库存完成时执行了：

```text
go test ./internal/verification/integration -run 'TestT09' -count=1
go test ./... -count=1
```

两条命令均通过。本文之后的变更应把这两条命令的结果作为重命名和局部优化的比较
基线。
