# 验证测试 helper 与 seam 库存

状态：**文档切片，只建立分类和依赖清单**（2026-10-25）。不创建 testkit，不拆
package，不导出任何测试专用生产 API。

本文承接 [T09 验证代码迁移前置契约](t09-verification-migration.md) 的切片 C，
记录 `internal/verification/integration` 内共享 helper 的消费者与职责，并为
未来按 seam 拆分做归属标注。所有调用者数据来自对当前代码的符号级扫描。

## 1. 当前拓扑事实

- `internal/verification` 下当前只有一个包：`integration`（单个
  `ethguard_test` package，20 个测试文件，其中 7 个是 T09 文件）。
- T09 的 7 个文件与包内其余 13 个文件**零符号耦合**（双向验证：T09 未使用
  非 T09 文件定义的任何包级符号，反之亦然）。两类文件今天共享的只有
  package 名。
- 仓库级共享测试设施是根目录 `rpctest`：被 aggregator、pool、ethadapter、
  observe/prometheus、internal/livecanary、internal/rpcsample/replay 和本包
  共 7 处消费。它已经承担跨模块 fixture kit 的角色；`t09Fleet` 是对
  `rpctest.Node` 的薄封装。
- replay seam 今天由 `internal/rpcsample`（含 `replay/` 与 testdata）承担；
  T09 代码与其无依赖（T09 不写 capture、golden 或 testdata）。

## 2. 共享 helper 清单

调用者缩写：MS=method_scenarios，SH=scenario_harness，VF=verification_fixtures，
FL=field_lifecycle，PM=protected_mutations，EC=evidence_conservation，
TP=typed_projection。

### 2.1 fleet 与链 fixture（VF 定义）

| helper | 职责 | 调用者 | 未来归属 |
| --- | --- | --- | --- |
| `t09Fleet` / `t09NewFleet` / 其方法（returnRaw、returnOne、failHTTP、failProvider、calls、totalCalls、context） | 真实 Client/adapter over 进程内节点池 | MS、SH、FL、PM、EC、TP、VF 全部 7 文件 | integration（facade+pool+aggregator 联合验证的本体 fixture） |
| `t09Chain` / `t09NewChain` / block、receipt*、txObject、t09TxObject、t09TypedLog | 真实签名交易与真实 header 的链素材 | VF、MS、PM、EC、FL | integration（构造真实可验证素材，被各 seam 复用） |
| `t09Log` / `t09Logs` / `t09FeeHistory` / `t09AccessList` / 锚常量（t09AnchorHeight、t09AnchorHash、t09Address、t09StorageKey） | 标准响应素材与固定锚 | VF、MS、PM、FL | integration |

### 2.2 表示变体与 JSON 编辑（VF 定义）

| helper | 职责 | 调用者 | 未来归属 |
| --- | --- | --- | --- |
| `t09Variants` / `t09VariantsNoEscape` / `t09Indent` / `t09UpperHex` / `t09EscapeFirstHex` | 同一语义值的多种 wire 拼写 | VF、FL | scenarios（字段生命周期矩阵的表示稳定性） |
| `t09SemanticJSON` | 解析后重编码，剥掉拼写差异 | VF、SH、TP、EC | scenarios（与 contracts 的 typed 比较共用） |
| `t09SetField` / `t09DeleteField` / `t09SetNestedField` / `t09DeleteNestedField` / `t09JSON` / `t09Panic` / `fieldOf`（EC 定义） | 构造性修改响应 payload | VF、MS、PM、FL、EC | scenarios（mutation/守恒用），`fieldOf` 亦被 PM 用 |

### 2.3 场景 harness 与共享断言（SH 定义）

| helper | 职责 | 调用者 | 未来归属 |
| --- | --- | --- | --- |
| `t09Want` / `t09Row` / `t09MethodCase` / `t09DimensionWant` / `t09ScenarioNames` / `t09Matrix*` | 场景行的期望结构与固定基组 N=5/H=2/Q=3 | SH、MS | scenarios |
| `t09CheckScenario` / `t09RunScenarioRow` / `t09AssertWant` | 单场景执行与 Decision 维度断言 | SH（MS 经 t09CheckScenario） | scenarios |
| `t09AssertSourceRefs` / `t09SourceNames`（FL 定义） | 来源成员与 fleet 索引精确匹配 | SH、MS、FL、PM、EC | scenarios |
| `t09AssertDecodedValue` / `t09AssertFaultNames` / `t09AssertSourceAbsent` / `t09AssertWholeCanonical` | 跨文件收敛出的四个共享断言 | SH、FL、PM、EC | scenarios（守恒类断言与 EC 共用） |
| `t09AssertNoPendingForm` / `t09Repeat` / `t09HasPendingParam` | 场景行辅助 | SH、MS | scenarios |

### 2.4 字段生命周期表（FL 定义）

| helper | 职责 | 调用者 | 未来归属 |
| --- | --- | --- | --- |
| `t09FieldRow` / `t09FieldRows` / `t09FieldRowFor` / `containsString` | 每方法字段生命周期单一事实源 | FL、EC、MS、SH、PM | scenarios |
| `t09*SchemaPaths` 七组 / `assertSchemaFieldsDeclared` / `stripSchemaArrayPrefix` / `topLevelField` | 官方 result-path 集与声明互锁 | FL | contracts（方法契约的 schema 面） |
| `t09FirstObject` / `t09AssertConservation` / `t09AssertTypedRoundTrip` | FL 专用断言 | FL | scenarios / contracts（typed round-trip 面） |

### 2.5 typed 摘要与无损解码（TP、VF 定义）

| helper | 职责 | 调用者 | 未来归属 |
| --- | --- | --- | --- |
| `t09Typed*` 23 个 probe | 生产 Client/ethadapter typed API 探针 | TP 定义；MS、FL 经 `t09MethodCase`/`t09FieldRow` 引用 | contracts（typed 一致性） |
| `t09Summary*` / `t09BigStrings` / `t09BigRows` / `t09FeeWireSummary` | typed 结果的字符串摘要 | TP | contracts |
| `t09TypedWant*` 14 个 | 对脚本答案应用生产 decoder 得期望摘要 | TP 定义；MS 引用（如 t09TypedWantBig） | contracts |
| `t09Param*` 4 个 | 参数形态读取 | TP | contracts |
| `t09Decode*` 14 个 / `t09BlockRecord` 等记录类型 / `t09BlockSummary` / `t09Receipt*` / `t09TransactionRecord` / `t09FeeWire` / `t09AccessWire` | 无损语义解码器（唯一测试侧解码点，仅做值比较，不复刻 typed 路径） | VF 定义；FL、MS、SH 经 row.decode 引用 | contracts |

### 2.6 mutation 与守恒（PM、EC 定义）

| helper | 职责 | 调用者 | 未来归属 |
| --- | --- | --- | --- |
| `t09Mutation` / `t09MutationCase` / `t09RunMutation` | 受保护字段 mutation 分类执行器 | PM | scenarios |
| `t09TraceConservation` / `t09OutsideField` / `t09OmittedField` / `t09ObservedField` / `t09AssertOutsideDestination` / `extensionPaths` / `residualPaths` | 逐 observation 守恒追踪 | EC | scenarios |
| `decodeJSONTree` / `semanticJSONEqual` / `semanticStringEqual` / `parseHexQuantity` / `isDecimal` / `compareNumbers` / `joinPath` / `bytesContain` / `trimValue` | 守恒比较的纯函数 | EC（`trimValue` 亦被 FL、TP 用） | scenarios |

## 3. testkit 候选结论

契约的资格条件是"同时被多个验证 Module 使用"。当前 `internal/verification`
只有 integration 一个 Module，**没有任何 helper 今天满足外提资格**；文件大
小不是外提理由。

拆分时点（contracts / scenarios 真正成为独立包时）将天然被两个 Module 共
用、届时需要先于拆分决策的候选，按证据强度排序：

1. `t09Fleet` 与 `t09NewFleet`（7 文件全用，是唯一同时服务场景矩阵、typed
   round-trip 与守恒的 fixture）；
2. `t09NewChain` 及链素材构造（MS/PM/EC/FL/VF 五处）；
3. `t09SemanticJSON` 与 JSON 编辑组（跨 5 文件）；
4. 四个共享断言 + `t09SourceNames`（跨 5 文件）。

注意：上述候选届时也应优先考虑落到已有的 `rpctest`（仓库级共享设施）而非
新建 `internal/testkit`，除非出现 rpctest 无法承载的验证语义。

## 4. 停止条件回顾

- 未创建任何新 package 或 testkit；未导出任何生产符号。
- 拆分若迫使生产代码导出测试专用 API（例如把 `t09Decode*` 需要的能力暴露
  成公共 API），停止并单列待审，而不是扩大 Interface。
- 本清单在每次 seam 拆分落地后需要复核调用者数据（当前数据对应提交
  4cacb51 的代码状态）。
