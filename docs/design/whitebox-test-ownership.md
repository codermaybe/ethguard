# 白盒测试归属清单

状态：现行（2026-11-04）。本文登记 `rpc`、`rpc/methods`、`pool`、`aggregator`、
`internal/runtime` 五个包全部白盒（同包）测试文件对未导出符号的依赖，为
「删除 legacy 执行模型」（阶段 B1，对应计划切片 8/9）与后续测试外移提供依据。

## 方法与复现

`scripts/whitebox_inventory.sh`（可重复执行）：收集包内生产文件的顶层未导出
声明，再逐个测试文件做词边界匹配。结果**高估**依赖（注释里出现符号也计入），
对归属审计是安全方向：报告为 0 依赖的文件是可靠的「可外移」候选；有依赖的
文件需人工确认依赖性质。

本文按依赖性质分四类：

- **A 保留白盒**：测试对象本身是未导出行为（状态机、内部不变量、生成器），
  黑盒化会丢失断言能力。
- **B 随 legacy 删除**：因 legacy constructor/decorator 链而存在（等价对照、
  oracle 矩阵），B1 删除 chain 时一并退役或重写为 `CompilePlan` 直测。
- **C 可外移**：0 未导出依赖，可改为 `_test` 包（黑盒）而不损失断言。
- **D 共享 fixture 耦合**：依赖的是测试间共享的未导出 fixture（如 `obs`、
  `src`、`fakeDialer`），单文件外移不可行，整组外移可行。

## rpc（22 个白盒文件）

| 文件 | 类 | 未导出依赖 |
| --- | --- | --- |
| acceptance_thresholds_test.go | D | cluster |
| adjudicate_test.go | D | cluster, corrObs, errObs, src |
| anchor_test.go | C | 0 |
| basis_immutability_test.go | C | 0 |
| canonical_key_source_test.go | D | cluster, src |
| corroborated_negative_test.go | D | decimal, obs, src |
| corroboration_composition_test.go | D | errObs, obs |
| decision_test.go | C | 0 |
| evidence_test.go | C | 0 |
| invocation_reference_test.go | A/D | countingCapture（capture 钩子行为） |
| local_test.go | D | obs, quantity, src |
| median_test.go | D | cluster, decimal, obs, quantity, src |
| observe_test.go | A | errObserved, fieldValue, newAcceptanceCalculator |
| outcome_verdicts_test.go | C | 0 |
| policy_test.go | C | 0 |
| quantity_test.go | D | decimal |
| read_plan_test.go | A/D | obs, planEvidence, taggedPlanEvidence（plan 内部证据） |
| status_set_test.go | C | 0 |
| strict_encoding_digest_test.go | A | assertEncodingDigests, cluster, rejectedEncoding*（digest 合同） |
| strict_rejected_encoding_test.go | A | 同上（rejected encoding 分母） |
| strict_test.go | D | cluster, obs, representative, sourceCounter, src, strictFunc |
| write_test.go | D | writeHash, writeReject, writeSource（fixture） |

rpc 的未导出依赖集中在**决策语义内部**（cluster 代表簇、acceptance
calculator、digest 断言）。这些是 0012/0029 的证据层，保留白盒（A/D）；
7 个 0 依赖文件（C）可随时转 `_test` 包。

## rpc/methods（31 个白盒文件）

| 文件 | 类 | 未导出依赖（性质） |
| --- | --- | --- |
| admission_suite_test.go | A | normalize*/admit*（未导出准入与归一化行为） |
| block_admission_test.go | A | admissionBlock, blockPolicy, blockRaw |
| block_encoding_test.go | A | blockRaw, injectField, mustMarshal |
| call_revert_outcome_test.go | A | normalizeCallOutcome, encodeCallSuccess 等 |
| canonical_key_domain_test.go | A | bloomOf, mustMarshal |
| catalog_interlock_test.go | A | methodCatalog（interlock） |
| catalog_test.go | C | 0 |
| content_compat_test.go | A | normalize*/admit* 全家（内容兼容矩阵） |
| eip1898_test.go | B | newTagAnchor, slicePolicies, tagAnchor, withRef, rebuildSlicePolicy（legacy chain 装配） |
| eth_call_revert_test.go | A | normalizeCallOutcome, resolvePair, rpcErrorObs |
| eth_createaccesslist_test.go | A | accessListBody, normalizeAccessList |
| eth_feehistory_test.go | A | feeHistory, normalizeFeeHistory, validateFeeHistoryRequest |
| eth_getblockbynumber_test.go | A | observation（fixture） |
| eth_getblockreceipts_test.go | A | admit/normalizeReceipt* 全家 |
| eth_getlogs_test.go | A | canonicalLogValue, logEntry, normalizeLogs |
| eth_gettransactionreceipt_key_test.go | A | foldReceiptHex, sampleReceipt 等 |
| eth_gettransactionreceipt_test.go | A | 同上 |
| exec_object_test.go | A/B | newTagAnchor（legacy 装配）+ observation |
| fee_history_plan_equivalence_test.go | B | feeHistoryPlan 等（plan↔constructor 等价） |
| local_shape_test.go | C | 0 |
| method_rules_test.go | A | methodRule 行生成与 interlock |
| methods_test.go | A | normalizeCallOutcome |
| moving_tag_matrix_test.go | A | 矩阵扫描器（自查工具，本身即 interlock） |
| new_methods_test.go | C | 0 |
| pending_test.go | B/A | slicePolicies 等 legacy 装配 + pending 语义 |
| plan_equivalence_test.go | B | constructorFor 等（plan↔chain oracle） |
| representative_test.go | A | identityKey, payloadOf, resolvePair |
| resolver_capability_interlock_test.go | A | validatePlanBindings（编译期 interlock） |
| spec_tables_test.go | A | specAbsentAllow（spec 语义） |
| strict_resolve_bench_test.go | A | benchmarkReceipt, normalizeReceiptObject |
| transaction_encoding_test.go | A | injectField, sampleResults |

methods 的白盒性主要来自**未导出的归一化/准入函数**（它们不进公开 API 是
正确封装），这类保留；eip1898/pending/plan_equivalence/fee_history 四处
是 **B 类**：依赖 legacy chain 装配 helper，属切片 8 重写、切片 9 删除范围。

## pool（18 个白盒文件）

| 文件 | 类 | 未导出依赖（性质） |
| --- | --- | --- |
| adapter_selection_test.go | D | dialer（fixture） |
| broadcast_test.go | A/D | eligibleFailureThreshold, monitor |
| collect_test.go | D | fakeNode/newFakeDialer/newPoolWithDialerAndChainID 等 |
| construction_test.go | C | 0 |
| freshness_scope_test.go | A/D | votingGate, tipReference, contractDialer 等 |
| membership_test.go | C | 0 |
| monitor_test.go | A/D | latencyWindow, monitorHTTPStatusError |
| native_http_differential_test.go | A/D | poolEchoServer 全家（真实适配器差分） |
| normalize_test.go | A | normalizeEndpointID |
| rate_limit_health_test.go | A/D | eligibleFailure*（阈值常量）+ fixture |
| sampler_lifecycle_test.go | A/D | samplerPeer, warmSamplerPool |
| scheduling_budget_test.go | A/D | budgetPeer, contractDialer |
| scheduling_capacity_test.go | A/D | quotaExceededProbe 等 |
| scheduling_contention_test.go | A/D | sharedBudgetPeer, waitForBudgetCall |
| tip_test.go | A/D | newTipPool, eligibleFailureThreshold 等 |
| transport_contract_test.go | A | contractDialer, contractTransport（合同注入点） |
| voting_test.go | A/D | eligibleFailureThreshold + fixture |
| write_outcome_health_test.go | A | checkReadLimitRedial |

pool 的白盒性来自 fixture 与阈值常量（`eligibleFailureThreshold` 等）：
阈值是 0010 的行为参数，断言「窗口百分比/冷却」必须读它——保留白盒或把
阈值提升为导出常量后再外移；两个 0 依赖文件（C）可外移。

## aggregator（13 个白盒文件，全部有依赖）

| 文件 | 类 | 未导出依赖（性质） |
| --- | --- | --- |
| aggregator_test.go | D | fixedPool, captureRecorder 等 fixture |
| composition_test.go | D | captureRecorder, fixedPoolWith |
| decision_event_status_test.go | A | scriptedAggregate, oneEvent（事件合同） |
| decision_fault_attribution_test.go | A | faultTables, invocationState（故障归因内部） |
| freshness_scope_test.go | A | invocationState, preparedTipFreshness |
| invocation_state_test.go | A | beginInvocation, invocationState（状态机本身） |
| outcome_projection_test.go | A | finalizeVerdicts, mustCorroboration |
| plan_projection_test.go | A | probeProjector 全家（projection 绑定） |
| read_plan_test.go | A | scriptedStage, probe*（计划执行管线） |
| rejected_encoding_test.go | D | fixedPoolWith |
| resolver_capability_interlock_test.go | A | resolvePlannedInvocation（interlock） |
| rpc_error_data_presence_test.go | A | presence 协议错误 fixture |
| testpool_test.go | D | fixedPool（纯 fixture） |

aggregator 全部保留白盒：invocationState 状态机与故障归因是 0025/0028 的
核心内部行为，无导出面可替代。

## internal/runtime（16 个白盒文件）

| 文件 | 类 | 未导出依赖（性质） |
| --- | --- | --- |
| captured_head_reader_test.go | A | capturedHeadReader, recordingCapture |
| finalized_head_raw_capture_test.go | A | anchorMethodName, tracePool |
| latest_reference_trace_test.go | A | recordingStage, traceAggregator |
| legacy_quarantine_interlock_test.go | B | legacyConstructorSeamAllowlist（B1 退役） |
| plan_block_equivalence_test.go | B | comparePlanAndChain（plan↔chain oracle） |
| plan_call_equivalence_test.go | B | 同上 |
| plan_chain_fleet_test.go | B | runPlanAndChain, runEquivalenceRoute |
| plan_general_equivalence_test.go | B | 同上 |
| plan_logs_equivalence_test.go | B | 同上 |
| plan_reference_trace_test.go | B | planTraceAggregator（plan 追踪） |
| pool_head_reader_test.go | C | 0 |
| projector_registry_test.go | A | buildMethodRegistry/buildProjectorRegistry |
| reference_tip_scope_test.go | A | tipScopedFleet |
| runtime_test.go | C | 0 |
| strict_head_equivalence_test.go | B/A | establishService vs establishStage（facade↔stage，B2 对象） |
| strict_head_raw_capture_test.go | A | tracePool |

runtime 的 6 个 `plan_*_equivalence/trace` 文件是切片 8 的重写对象（改为
直测 `CompilePlan` + 业务 fixture）；`legacy_quarantine_interlock_test` 在
切片 9 退役；`strict_head_equivalence_test` 比较 facade 与 stage 两条路径，
归切片 11/12 处理。

## 汇总与后续动作

- **C 类（可外移，13 个）**：rpc 7、rpc/methods 3、pool 2、runtime 2（计数
  见各表）。外移是机械改动，不改变断言；不与删除切片绑定，可按包分批做。
- **B 类（随 legacy 退役/重写）**：rpc/methods 4、runtime 7 —— 即切片 8
  （重写 plan 行为矩阵）与切片 9（删除双执行模型）的测试工作面。
- **A/D 类（保留）**：其余全部。D 类的 fixture 耦合意味着外移必须整组进行，
  单文件移动会破坏编译。
