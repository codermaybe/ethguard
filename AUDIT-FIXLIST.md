# 审计修复清单（临时文档）

> **状态**：进行中。全部完成后删除本文件。
> **用途**：单 agent 多轮迭代的交接载体——每项自包含，无过往上下文亦可独立执行。
> **来源**：2026-10 全库代码质量审计 + 对抗性复核（复核推翻/降级项已从清单剔除）。
> **仓库**：分支 master。基线 commit：853baee 之后。

## 迭代协议（每轮必须遵守）

1. 按编号顺序执行**恰好一项**；跳过任何项需用户明确同意。
2. 完成后：运行该项"验证"小节全部命令 + 下方全局基线 → 全绿后把该项状态改为 `✅ 完成` 并附一句话改动摘要 → **停下等待用户检查**。
3. 每项完成并验证后，在临时分支 `fix/audit-findings` 上**单独 commit 一次**（conventional 风格，scope 用被修的模块；本清单文档自身单独一个 chore commit，合并前删除）。
4. 若某项验证失败且 30 分钟内无法收敛，把状态改为 `⚠️ 受阻` 并写明原因，停下。

**全局基线**（每轮都要跑）：

```bash
gofmt -l .                 # 必须无输出
go build ./... && go vet ./...
go test ./...              # 涉及 pool/ 的项加 -race ./pool/
~/go/bin/golangci-lint run ./...   # 若本机未装可跳过（CI 会跑）
```

## 明确不做（复核已推翻，任何一轮都不要"顺手修"）

- **pool/source.go verifyChainID 的"重复调用窗口"**：成功路径 `identityChecked=true` 与解锁同临界区，窗口不存在；失败路径的重试是期望行为。
- **rpc/quantity.go vs internal/transport/http/quantity.go "宽严不一"**：前者解析请求参数与自身 canonical 输出（doc 明示 case-insensitive 是设计），后者解码响应；且前者不参与 chainId 解析。非缺陷。
- **client.go 对 FinalizedHeadAnchor 未导出字段的修补**：工厂内配置自己刚构造、尚未发布的对象，正常模式；自定义 resolver 已有 `anchorFactory` 入口。
- **collectionFaults / collectionFaultList 的"collect 错误双写"**：`Faults`（人读并集）与 `CollectionFaults`（结构化通道）是 rpc/types.go 注释声明的双通道设计。
- **rpc/internal/objects/feehistory 的 Normalize 不能删**：它是 strict normalizer（rpc/methods/eth_feahistory.go:34 传入、:211 实现），每次响应都执行。只能消它与 Encode 重复的校验。

---

## 修复项

### [1] P0 · cmd/soak 移除退役的 -anchor-ttl flag — 状态：✅ 完成

> 改动摘要：删除 `-anchor-ttl` flag 声明与 `WithAnchorCaching` 选项块（cmd/soak/main.go），其余未动。验证：gofmt/build/vet/全量测试/golangci-lint 全绿。

- **问题**：`cmd/soak/main.go:88` 的 `-anchor-ttl` 默认 `time.Minute`，:103-105 据此调用 `ethguard.WithAnchorCaching(ttl)`；而 `client.go:187-188` 对任何非零 anchorCache 报错（决策 0026 已退役缓存）。默认参数下 `go run ./cmd/soak` 必然构造失败。该包被 `coverage_interlock_test.go:25` 的零测试 allowlist 豁免，无护栏。
- **改动**：删除 `anchorTTL` flag 声明与 `if *anchorTTL > 0 {...}` 选项块。缓存退役后每次读取本就重建引用，flag 无存在理由。不动其它内容。
- **验收**：`grep -n "anchor" cmd/soak/main.go` 不再出现 anchorTTL/WithAnchorCaching；`go build ./cmd/soak` 通过；全库测试不受影响。

### [2] P0 · pool Forward 的 release 防 panic + ErrSourceGated 保留底层原因 — 状态：⬜ 未开始

- **问题 a**：`pool/collect.go` Forward 循环内 `collectOne(...)` 之后才 `release()`，未 defer。`collectOne` panic 会永久泄漏该源调度 slot——`internal/livecanary/run.go:60` 有 `recover()`，泄漏场景真实可达（工具路径/任何带 recover 中间件的调用方）。同文件 `dispatchOne`（collect.go:234 附近）对同一模式用 `defer release()` 并有注释，属防护不一致。
- **问题 b**：Forward 与 dispatchOne 各有一处把 `context.DeadlineExceeded` 直接替换为 `ErrSourceGated`，丢弃底层原因。代码库既有习惯是 `fmt.Errorf("%w: %w", ...)`（见 pool/source.go:84）。
- **改动**：
  - a：**不能**简单加 defer（在 for 循环内会堆积到函数返回）。把循环体的 acquire→collectOne→record 段提取为私有 helper（如 `forwardOne(callCtx, parent, src, method, params) (Emission, forwardVerdict)`），helper 内 `defer release()`；循环按返回的 verdict 决定 append/return。保持现有早退语义（ErrSourceGated 即 return；usable answer 即 return）逐字不变。
  - b：两处替换改为 `fmt.Errorf("%w: %w", ErrSourceGated, err)`。先 grep 测试中对 `ErrSourceGated` 的断言方式（`==` 还是 `errors.Is`），用 `==` 断言的测试需同步改为 `errors.Is`。
- **验收**：`go test -race ./pool/` 全绿；新增或调整的断言覆盖"gated emission 同时满足 errors.Is(ErrSourceGated) 与 errors.Is(context.DeadlineExceeded)"。
- **风险**：helper 提取不得改变 Forward 的 failover 顺序与 emission 追加顺序。

### [3] P1 · 补 CollectVoting 超时优先级的回归测试 — 状态：⬜ 未开始

- **问题**：pool 的三条 verdict 路径中，CollectVoting 是**故意的 callCtx-first**（超时错误优先于可用答案返回，见 collect.go:172-187 附近注释），与 collect 的 usable-first 相反。collect 侧有 `TestCollectUsableAnswerOutranksFanoutTimeout` 钉住，CollectVoting 侧**零测试**（voting_test.go grep Timeout/DeadlineExceeded 零命中）。这是全库最脆的隐式契约——未来有人"统一"两处会无声破坏。
- **改动**：在 `pool/voting_test.go` 新增镜像测试（参照 collect_test.go 的 `TestCollectUsableAnswerOutranksFanoutTimeout` 写法）：一个 fast 成员（rpctest.After 30ms 应答）+ 一个 laggard（Hang），CollectOptions{Timeout: 150ms}。断言：**返回 err 为超时**（callCtx-first）且 emissions 中 fast 成员带可用答案（答案随行不丢弃）。测试应立即通过（行为已存在，本项只是钉住）。
- **验收**：新测试通过；且在 collect.go 的 CollectVoting verdict 顺序被人为反转时能失败（可临时反转自验后还原，不必留在 diff）。

### [4] P1 · 统一三份 verdict 扫描 — 状态：⬜ 未开始（依赖 [3] 先落地）

- **问题**：三份几乎相同的 verdict 扫描：`pool/collect.go` CollectVoting（:172-187 附近）、`pool/collect.go` collect（:318-331 附近）、`pool/broadcast.go`（:148-161 附近），优先级故意不同（CollectVoting=callCtx-first；collect 与 Broadcast=usable-first）。
- **改动**：提取 `fanoutVerdict(emissions []Emission, callCtx context.Context, timeoutFirst bool) error`（名字可斟酌），三处调用；语义逐字保持：usable-first 路径 = usable answer → nil、callCtx.Err → ctx err、chainID sentinel → sentinel、否则 ErrAllSourcesDown；timeoutFirst 路径 = callCtx.Err 优先。**[3] 的测试是本项的安全网**。
- **验收**：`go test -race ./pool/` 全绿，含 [3] 新测试；三处调用点的注释保留"优先级差异"的说明。

### [5] P1 · 收敛装饰器委托样板（~500 行） — 状态：⬜ 未开始（最大单项，建议独立一轮）

- **问题**：rpc 包约 82 个纯转发方法：anchor.go 40 个（4 个装饰器类型）、methods/pending.go 12、eip1898.go 10、internal/policy/admission.go 10、其余散在 logs/block/eth_feahistory；另有 8 个 corroborating wrapper（corroboratingAnchoredPolicy 等）。新增可选能力接口需改 ~10 处。
- **改动**（两步，可分开提交）：
  1. 共享转发基座：`type policyDecorator struct{ rpc.Policy }`（嵌入实现默认转发），各装饰器嵌入它并只覆写有实际逻辑的方法（Prepare/Admit/MethodRule 合并等）。注意：嵌入后接口断言（`policy.(rpc.CorroboratingPolicy)`）对**提升方法**同样生效，这正是消除 corroborating wrapper 的机制——先写一个失败测试确认断言路径，再动手。
  2. MethodRule 合并模板（`WithInner` 里重复 ~10 次的 `if inner, ok := ...` 段）提取为共享 helper。
- **验收**：`go test ./rpc/... ./internal/verification/...` 全绿；重点跑 `rpc/methods` 的 `moving_tag_matrix_test.go`、`representative_test.go`、`admission_suite_test.go`（装饰器组合矩阵）；`grep -c "^func (" rpc/anchor.go` 显著下降。
- **风险**：能力保留矩阵是行为契约，矩阵测试必须全绿才算完成；若某 wrapper 有非转发逻辑（如 anchor 的 moving-tag 检测），保留原样只转发纯样板。

### [6] P1 · 合并双 log 编码器 — 状态：⬜ 未开始

- **问题**：`rpc/internal/objects/receipt/encode.go` 的 encodeReceiptLog/canonicalReceiptLog 与 `rpc/internal/objects/log/encode.go` 的 encodeLog/canonicalLog 重复：receipt 侧是 log 侧的严格超集（共享 Address/Topics/Data/BlockNumber/TransactionHash/TransactionIndex/BlockHash/LogIndex/BlockTimestamp 9 字段校验，receipt 多 Removed 必填 + binding/bloom 检查）。
- **改动**：把共享字段校验与 canonical 结构下沉到 `objects/log`（导出如 `EncodeCanonicalLog(value, opts)` 或带 `RequireRemoved bool` 选项）；receipt 侧改为调用 + 保留自己的 binding（zeroHash 容差）与 logsBloom 一致性检查。**两个 canonical struct 的 JSON 字段序不得变化**（对外字节级承诺，有 encoder_coverage_test 把关）。
- **验收**：`go test ./rpc/...` 全绿，特别是 `objects/receipt`、`objects/log` 的 encoder_coverage_test 与 `rpc/methods` 的 admission_suite。

### [7] P2 · resolver fault 就地携带 source 身份 — 状态：⬜ 未开始

- **问题**：strict.go:334/346、median.go:86/93、call_revert.go:187-206、finalized.go:61/66、local.go:80/87、methods/pending.go:92/98 循环内 `item.Source` 在作用域内，却输出裸 `"encode: %w"`/`"normalize: %w"`；这些 faults 直接进 `Decision.Faults`（strict.go:212/228）。aggregator 的 structuredSourceFaults 只 mirror pool 层 emission 错误，救不了 resolver 内部错误。
- **改动**：逐处把 `fmt.Errorf("encode: %w", err)` 改为 `fmt.Errorf("source %q: encode: %w", item.Source.ID, err)`（格式统一用 ID；ID 为空时回退 Name）。先 grep 测试中断言这些错误文本的用例并同步。
- **验收**：`go test ./rpc/... ./aggregator/...` 全绿；faults 文本含 source 标识。

### [8] P2 · 分解 internal/reference 的 Finalized — 状态：⬜ 未开始

- **问题**：`internal/reference/finalized.go:28-224` 单函数 ~197 行：收集→中位数参考→lag 过滤→quorum→候选选取→hash 冲突检测→确认轮 reorg 检测→errors.Join 组合，难单测。
- **改动**：拆为 `selectCandidate(views, maxLag, quorum)`（现 :78-119 段）与 `confirmCandidate(...)`（现 :122-190 段）两个纯函数 + 薄编排层。行为逐字不变，现有测试（reference 包）不得修改即通过。
- **验收**：`go test ./internal/reference/...` 全绿；新函数有直接单测（至少覆盖 reorg 与 hash 冲突分支）。

### [9] P2 · AggregateFields 单源畸形改为记 fault 后继续 — 状态：⬜ 未开始（含设计决策，动手前先向用户确认方案）

- **问题**：`rpc/observe.go:79-81` 单源 JSON 解码失败直接整体返回 ErrMalformedResponse，与 strict/median 的"记 fault 后 continue"模型不一致。爆炸半径有限（编码器方法在 admission 已置 Val=nil 跳过；仅非编码器方法可触发），但行为不一致是真的。
- **设计决策（需用户拍板）**：畸形源按"弃权"处理后，quorum 分母是原 observations 数（保守，畸形源拖高门槛）还是有效 observations 数（激进）？建议：分母不变（原 n），畸形记入返回的 Evaluation 或随错误通道上报。动手前把方案给用户确认。
- **验收**：新增单测：N 源中 1 个畸形、其余一致 → 有 corroboration 结论且畸形源可归因；全畸形 → 仍返回 ErrMalformedResponse 类错误。

### [10] P3 · 机械小项打包 — 状态：⬜ 未开始

逐小项独立可验证，一轮内可全部完成：
- a：32MiB body 上限收口为单一常量（`jsonsafe.MaxBodyBytes` 为源，`internal/transport/rawbody.go:35`、`internal/transport/http/http.go:36` 引用它或新建 `internal/limits`；注意 transport 不得反向 import jsonsafe 之外引入新依赖方向——先看包依赖图再定位置），并注释"三处必须同值的原因"。
- b：`pool/monitor.go:208` 注释修正（"nominal five percent, floored at minRequestPace so small paces drop faster"）。
- c：`pool/broadcast_test.go` 的 slow nodes 改用 `t.Cleanup`，消除 t.Fatalf 中途退出泄漏。
- d：`pool/tip.go` RefreshTips 的 probe 超时与 poll interval 解耦（引入独立 probe timeout，如 min(interval, 30s) 或新常量——实现时看现有常量风格定）。
- e：aggregator.go 两条早退路径（:282-290 与 :306-313 附近）的 Decision 字面量提取 helper；emission→rpc.SourceRef 三处重复提取 `sourceRefOf`。
- 验收：全量基线 + `go test -race ./pool/`。

### [11] P3 · 功能型小项 — 状态：⬜ 未开始

- a：`pool` 增加 `WithBroadcastMaxConcurrent`（默认 ≤0 保持现有无界语义；与 CollectOptions.MaxConcurrent 注释风格对齐），broadcast.go:128 使用之。配一个行为测试。
- b：给 `rpc/internal/objects` 六个 Encode 补 fuzz（不变量：Encode 输出喂对应 Normalize/Parse 得同一 key 或稳定 reject；以 `rpc/methods/admission_suite_test.go` 的 case 表为种子 corpus）。放各包 `fuzz_test.go`，短时间本地跑通即可（`go test -fuzz=Fuzz -fuzztime=10s`）。
- 验收：全量基线；fuzz 本地 10s 无失败。

### [12] 收尾 — 状态：⬜ 未开始（全部 ✅ 后执行）

- 删除本文件；如用户要求，把定稿的修复摘要整理进 docs/decisions.md 或 commit message。

---

## 附：本次审计中被复核确认为真问题但**未列入**清单的理由

- `rpc/observe.go` 的 `mustMarshal` panic 路径、`call_revert.go` 的静默降级回退：有上游 jsonsafe.Validate 兜底，属防御纵深讨论项，改动收益低于扰动成本。
- `rpc/strict.go:315` ErrConflict 分类措辞：轻微，且改动会波及消费方错误分类测试，暂缓。
- 注释语言统一（wire/encode.go、feehistory/encode.go 中文注释）： Cosmetic，可在触碰相邻代码时顺带处理，不单列。
