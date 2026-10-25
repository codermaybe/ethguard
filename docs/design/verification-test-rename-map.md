# 验证测试入口语义化重命名映射（P2）

状态：**P2.2 已执行（45/45 应用）**（2026-10-25）

本文冻结 `internal/verification/integration` 中全部 45 个 `TestT09...`
测试入口到 `TestVerification...` 语义名的映射。命名约定：

- 统一前缀 `TestVerification`，专项命令使用 `-run '^TestVerification'`；
- 方法场景矩阵入口为 `TestVerificationMethodScenario<Method>`；
- typed 一致性入口带 `Typed`，守恒类入口带 `Evidence`，其余保留原语义后缀；
- `T09.1`/`T09.2` 任务编号保留在注释、提交历史和迁移说明中，不进入函数名。

职责类别：method-scenario、field-lifecycle、protected-mutation、
evidence-conservation、typed-projection、anchor-or-boundary。

## 映射表

### method-scenario（27）

| 旧名 | 新名 |
| --- | --- |
| `TestT091ChainID` | `TestVerificationMethodScenarioChainID` |
| `TestT091GetBlockByHash` | `TestVerificationMethodScenarioGetBlockByHash` |
| `TestT091GetBlockByNumber` | `TestVerificationMethodScenarioGetBlockByNumber` |
| `TestT091GetBalance` | `TestVerificationMethodScenarioGetBalance` |
| `TestT091GetTransactionReceipt` | `TestVerificationMethodScenarioGetTransactionReceipt` |
| `TestT091GetTransactionByHash` | `TestVerificationMethodScenarioGetTransactionByHash` |
| `TestT091GetBlockReceipts` | `TestVerificationMethodScenarioGetBlockReceipts` |
| `TestT091GetTransactionCount` | `TestVerificationMethodScenarioGetTransactionCount` |
| `TestT091GetStorageAt` | `TestVerificationMethodScenarioGetStorageAt` |
| `TestT091GetCode` | `TestVerificationMethodScenarioGetCode` |
| `TestT091Call` | `TestVerificationMethodScenarioCall` |
| `TestT091EstimateGas` | `TestVerificationMethodScenarioEstimateGas` |
| `TestT091GetLogs` | `TestVerificationMethodScenarioGetLogs` |
| `TestT091FeeHistory` | `TestVerificationMethodScenarioFeeHistory` |
| `TestT091GasPrice` | `TestVerificationMethodScenarioGasPrice` |
| `TestT091MaxPriorityFeePerGas` | `TestVerificationMethodScenarioMaxPriorityFeePerGas` |
| `TestT091BlobBaseFee` | `TestVerificationMethodScenarioBlobBaseFee` |
| `TestT091BlockNumber` | `TestVerificationMethodScenarioBlockNumber` |
| `TestT091CreateAccessList` | `TestVerificationMethodScenarioCreateAccessList` |
| `TestT091PeerCount` | `TestVerificationMethodScenarioPeerCount` |
| `TestT091Syncing` | `TestVerificationMethodScenarioSyncing` |
| `TestT091NetVersion` | `TestVerificationMethodScenarioNetVersion` |
| `TestT091Listening` | `TestVerificationMethodScenarioListening` |
| `TestT091MatrixCoverage` | `TestVerificationMethodScenarioCoverage` |
| `TestT091CallRevertDimensionMatrix` | `TestVerificationMethodScenarioCallRevertDimensions` |
| `TestT091LocalShapeRejection` | `TestVerificationMethodScenarioLocalShapeRejection` |
| `TestT091GetLogsNullIsMalformed` | `TestVerificationMethodScenarioGetLogsNullIsMalformed` |

### anchor-or-boundary（4）

| 旧名 | 新名 |
| --- | --- |
| `TestT091QuorumFloorInterlock` | `TestVerificationQuorumFloorInterlock` |
| `TestT091AnchorSeam` | `TestVerificationAnchorSeam` |
| `TestT092LocalWholeAnswerShapeBoundary` | `TestVerificationLocalWholeAnswerShapeBoundary` |
| `TestT092UpperPrefixSpelling` | `TestVerificationUpperPrefixSpelling` |

### field-lifecycle（4）

| 旧名 | 新名 |
| --- | --- |
| `TestT092FieldLifecycleCoverage` | `TestVerificationFieldLifecycleCoverage` |
| `TestT092RepresentationStability` | `TestVerificationFieldRepresentationStability` |
| `TestT092CallRevertValueAndContext` | `TestVerificationCallRevertValueAndContext` |
| `TestT092CallRevertNonHexDataCanonical` | `TestVerificationCallRevertNonHexDataCanonical` |

### protected-mutation（3）

| 旧名 | 新名 |
| --- | --- |
| `TestT092ProtectedMutationClasses` | `TestVerificationProtectedMutationClasses` |
| `TestT092ProtectedMutationFails` | `TestVerificationProtectedMutationFails` |
| `TestT092EscapedHexAdmissionBoundary` | `TestVerificationEscapedHexAdmissionBoundary` |

### evidence-conservation（6）

| 旧名 | 新名 |
| --- | --- |
| `TestT092ConservationPerObservation` | `TestVerificationEvidenceConservationPerObservation` |
| `TestT092OutsideDiagnosticsPerObservation` | `TestVerificationEvidenceOutsideDiagnosticsPerObservation` |
| `TestT092NestedDerivedFieldRecomputation` | `TestVerificationEvidenceNestedDerivedFieldRecomputation` |
| `TestT092DiagnosticsConservation` | `TestVerificationEvidenceDiagnosticsConservation` |
| `TestT092CallRevertWithheldDataConservation` | `TestVerificationCallRevertWithheldDataConservation` |
| `TestT092CallRevertAbstainConservation` | `TestVerificationCallRevertAbstainConservation` |

### typed-projection（1）

| 旧名 | 新名 |
| --- | --- |
| `TestT092RoundTrip` | `TestVerificationTypedRoundTrip` |

## 引用同步范围（P2.2 执行时）

- `method_scenarios_test.go`：45 处函数定义中的 29 处、`t09MatrixTestNames`
  的 23 个值、`TestVerificationMethodScenarioCoverage` 内部的
  `strings.HasPrefix(name, "TestT091")` 判定与 6 个例外名单、文件内注释；
- `field_lifecycle_test.go`、`evidence_conservation_test.go`、
  `scenario_harness_test.go`、`verification_fixtures_test.go`、
  `protected_mutations_test.go`：函数定义、函数头注释与 notes 中对
  `TestT092EscapedHexAdmissionBoundary` 的 3 处活引用；
- 当前文档中的专项命令与"保持 TestT09 前缀"旧描述：
  `project-roadmap.md`、`architecture-recovery.md`、
  `t09-verification-migration.md`；
- 保留不动：`t09-contract.md` 完成审计表与收口运行记录（提交锚定的历史
  证据）、`codebase-topology-migration.md` 的 M 阶段验收记录、
  `moving-tag-matrix.md`（Tests 列不含任何 T09 入口，已核实为零引用）、
  CI 工作流（只运行全量 `go test ./...`）、`docs/decisions.md`（零引用）。

## 停止条件核对（P2.4）

- 每个旧名恰好映射一个新语义，无多义（45/45 一一对应）；
- matrix Tests 列零 T09 引用，无需映射裁决；
- 纯函数名替换，不需要改测试体、fixture、断言或 helper；
- 不保留旧名兼容入口，不引入 `TestT09` 别名；
- 不触碰生产代码与公开 API。

## 数量核对

27（method-scenario）+ 4（anchor-or-boundary）+ 4（field-lifecycle）
+ 3（protected-mutation）+ 6（evidence-conservation）+ 1（typed-projection）
= 45，与扫描到的测试入口总数一致。
