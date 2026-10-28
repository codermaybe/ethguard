# MethodSpec Reference 字段冻结（R5）

> 状态：已冻结（轨道 R5，2026-10-29）。前置：五类纸上原型通过（`prototypes/overview.md`）、
> R4 的声明式 `ReferenceRequirement` 与 ReferenceStage 门禁通过（trace fixture 全绿）。
> 本文件冻结 `MethodSpec` 的 reference 维度；其余字段集在 M0 编译器落地时按原型结论冻结。

## 1. 冻结的字段语义

`MethodSpec` 携带**一个** reference 字段（不设第二个来源）：

```go
// Reference declares where a request can ride a moving tag and therefore
// which reference level the invocation must establish before the request is
// rewritten. Declaring is all a method does: the aggregator runs the
// ReferenceStage on the invocation's frozen basis, and the wire rewrite reads
// the established evidence. A nil Reference means the method never needs a
// reference (stateless, pinned, or local reads).
Reference *ReferenceSpec
```

```go
type ReferenceSpec struct {
    // Kind selects the request shape that can carry a moving tag.
    //   ReferenceNone         — the method has no moving-tag position
    //   ReferenceHashAt       — string positions rewritten to the anchor hash
    //   ReferenceHeightAt     — string positions rewritten to the anchor height
    //   ReferenceHeightFields — named string fields of one object parameter,
    //                           defaulted and rewritten to the anchor height
    //   ReferenceEIP1898      — the object form {blockNumber: tag} at one
    //                           position, rewritten to {blockHash}
    Kind      ReferenceShape
    Positions []int    // ReferenceHashAt / ReferenceHeightAt / ReferenceEIP1898
    ParamPos  int      // ReferenceHeightFields
    Fields    []string // ReferenceHeightFields
}
```

决定性约束（承自 R4 的实现与五类原型）：

1. **声明与锚定同源**：`ReferenceFor(Request)` 的判定与 `Prepare` 的改写条件由
   同一 spec 派生 —— 一个位置要么同时声明并改写，要么都不。编译器不得让二者分叉。
2. **kind 来自请求**：spec 声明的是「哪里能骑 moving tag」；具体 level
   （latest/safe/finalized）来自请求中的 tag（`ReferenceKindForTag`），不是 spec 常量。
3. **无网络回调**：spec 不携带任何 `AnchorFunc`；改写数据来自 invocation 上已建立的
   reference，建立只由 aggregator 通过 ReferenceStage 执行。
4. **字段集只减不增**：新的 reference 形态先走 Custom 逃生口评审，不为单方法扩列。
5. `WithAnchor`/`WithAnchorHeight`/`WithAnchorHeightFields`/`EIP1898BlockRef` 装饰器
   在 M0 之后由 `Compile` 从 spec 发射；直接构造（测试、迁移期）继续可用。

## 2. Registered 方法 reference inventory

从现装配（`internal/runtime.Build` + 各方法 policy 构造器）逐方法盘点：

| 方法 | Reference | 说明 |
| --- | --- | --- |
| eth_chainId | none | 无锚定 |
| eth_getBlockByHash | none | hash 即 pin |
| eth_getBlockByNumber | none | 业务形态无锚定；header-only 形态兼任 reference 读取（HeadCodec 复用，见 P4） |
| eth_getBalance | hash@[1] | + EIP-1898 对象形态同位置 |
| eth_getTransactionReceipt | none | hash 即 pin |
| eth_getTransactionByHash | none | hash 即 pin |
| eth_getBlockReceipts | hash@[0] | pending 局部拒绝 |
| eth_getTransactionCount | hash@[1] | + EIP-1898 |
| eth_getStorageAt | hash@[2] | + EIP-1898 |
| eth_getCode | hash@[1] | + EIP-1898 |
| eth_call | hash@[1] | + EIP-1898 |
| eth_estimateGas | hash@[1] | + EIP-1898 |
| eth_getLogs | height-fields@[0].fromBlock,toBlock | 缺省字段视为 moving tag |
| eth_feeHistory | height@[1] | newestBlock 不能带 hash |
| eth_gasPrice | none | |
| eth_maxPriorityFeePerGas | none | |
| eth_blobBaseFee | none | |
| eth_blockNumber | none | |
| eth_createAccessList | hash@[1] | + EIP-1898 |
| eth_peerCount | none | local |
| eth_syncing | none | local |
| net_version | none | |
| net_listening | none | |
| eth_sendRawTransaction | —（Write 族） | 不在 Read spec scope |
| eth_getProof | —（Unsupported） | Verification 族，场景数据待定（§8） |

每个 Registered 方法的 reference 字段都有显式值或明确 none；无「隐式默认」。

## 3. 门禁确认

- 五类原型通过：P1（hash@+EIP-1898+pending）、P2（validate-before-reference +
  height-fields）、P3（call-revert 逃生口不受影响）、P4（none + HeadCodec 复用）、
  P5（none）。
- trace 门禁通过：一次移动标签读取 = 一个 Invocation、一个 Basis、一次声明式建立、
  两次 Prepare 观察共享同一 ID、业务 fan-out 在建立失败时为零。
- `moving_tag_matrix_test.go` 不改即通过（声明与改写同源的回归网）。
