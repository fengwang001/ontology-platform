# 属性级并发写入判定：设计说明

本文档说明对象实例存储中属性级（而非整实例级）并发写入判定的设计，
包括关键取舍、被放弃的方案与本地验证方法。实现位于 `ontology/`，
对照模型位于 `ontology/naive/`。

## 1. 冲突判定规则

一次写入请求携带：目标实例、调用方声明的基线版本号、显式写集合 `W`。
系统为其计算相关读集合 `R`：该对象类型上注册的所有校验钩子**为本次写入**
声明的读取字段集合（本实例属性 + 经由链接声明的跨实例属性）。

两次写入冲突，当且仅当：

```
(W1 ∪ R1) ∩ (W2 ∪ R2) ≠ ∅
```

显式写集合完全不相交但读集合相交时仍判定冲突；写集合与相关读集合
均不相交时，两次并发写入都成功，并在同一基线之上合并出新版本。

判定在**提交时刻**、于实例锁内、基于当时的最新已提交版本重新计算。
调用方声明的基线仅用于确定预期起点（决定与哪一份足迹比较），
不参与"是否冲突"的事实认定。

## 2. 判定顺序与三类互斥错误

提交路径按固定顺序检查，任意一次写入最多命中一类：

1. **已删除**（`ErrKindDeleted`）：目标实例已被逻辑删除。
   该检查优先于属性级冲突检查（规范强制要求）。
2. **基线落后**（`ErrKindStaleBaseline`）：`baseline + 1 < current`，
   即基线落后超过一个版本，超出 O(1) 判定预算（见 §4）。
3. **属性级冲突**（`ErrKindPropertyConflict`）：写集合 ∪ 相关读集合
   与最近一次已提交写入的同名足迹存在交集（含跨实例部分）。
4. （正交的第四类）**校验拒绝**（`ErrKindValidation`）：钩子对合并后的
   预期状态校验失败。与三类冲突错误互不重叠。

三类错误通过 `*WriteError.Kind` 字段由返回值本身区分（`IsKind` 辅助函数）。

## 3. 版本与快照

- 版本号是每个实例上的 `uint64` 计数器，每次成功提交加一，严格单调、
  不重复。递增方向与时钟推进方向一致（先提交者获得较小版本号），
  但取值不依赖时钟：实现中不读取任何时钟来产生版本号。
- 每次提交生成不可变快照（属性深拷贝 + 规范化序列化字节缓存）。
  `ReadAt` 对同一版本永远返回相同字节，与后续写入次数无关。
- 被拒绝的写入不触碰版本号、快照、当前属性、足迹与挂起的远程失效
  集合；拒绝路径与成功路径可通过外部可观察状态（版本号是否前进、
  判定日志中记录的裁定类别）区分。

## 4. O(1) 判定：只保留最近一次提交的足迹

每个实例只保存**最近一次已提交写入**的足迹（`W ∪ R` 的本实例部分），
因此单次判定的开销与历史版本总数无关。

代价是合并深度被限制为 1：基线等于当前版本（无并发写入）或其直接
父版本（恰有一次并发写入，可做交集判定）时才可提交；更早的基线
需要查阅多份历史足迹，直接以 `ErrKindStaleBaseline` 拒绝，由调用方
读取新版本后重试。规范要求的三类错误之一（基线落后导致的版本冲突）
正是这一预算的表达方式。

**可验证证据**：每条判定记录（`DecisionRecord`）都带有
`FootprintsConsulted`（恒为 1）与 `HistoryLen`（判定时历史长度）两个字段。
证据由判定路径本身写入判定日志，不依赖额外对外暴露的状态；
`TestConflictCheckCostIndependentOfHistory` 在 200 个版本的历史上验证
该值恒定。

## 5. 跨实例相关读取（链接与钩子）

- 被指向实例的版本变化本身不构成对指向实例的写入。
- 归属裁定**唯一依据**是校验钩子声明的读取范围（`RemoteReads`），
  而非链接本身的存在：注册链接类型、建立链接实例都不是必要条件
  （`TestRemoteAttributionByHookDeclarationOnly` 在完全没有链接的
  情况下验证冲突仍然成立）。
- 实现为类型级反向订阅索引 `(目标类型, 属性) → 订阅实例集合`。
  被指向实例提交时，其写集合中每个被订阅的属性被记入各订阅实例的
  **挂起远程失效集合**；指向实例下一次提交判定时，其相关读集合与该
  挂起集合求交。成功提交会清空挂起集合（该版本已"知悉"这些变化）；
  被拒绝的写入不得消费它。

## 6. 可串行化

- 同一实例的全部判定在实例锁内串行完成；跨实例的远程失效检查、
  消费、传播与判定日志追加在同一 `propMu` 临界区内完成。
- 因此判定日志的全局 ID 顺序就是所有写入的一个合法串行解释：
  不存在只在真正并发下才能产生的中间状态。
- 等价性测试（`TestConcurrentWritesEquivalentToNaiveSerial`）按日志
  顺序把每次判定重放到独立实现的朴素全锁定模型，逐步比对裁定类别、
  版本号与最终状态，必须完全一致。

## 7. 被放弃的方案

| 方案 | 放弃原因 |
| --- | --- |
| 整实例级版本冲突（任何并发写即冲突） | 不满足属性级合并的核心需求 |
| 保留全部历史足迹以支持任意深度合并 | 判定开销随历史版本总数增长，违反 O(1) 约束 |
| 用墙钟/混合逻辑时钟产生版本号 | 规范禁止依赖时钟取值；计数器已满足"方向一致" |
| 以链接实例的存在作为远程归属依据 | 规范明确：唯一裁定依据是钩子声明的读取范围 |
| 实例级（沿实际链接）远程订阅 | 与"链接本身不是裁定依据"冲突；且链接实例的增删会引入额外的订阅一致性维护成本 |
| 读-写非对称冲突（SSI 风格） | 规范明确要求在"写集合 ∪ 相关读集合"层面做对称交集判定 |
| 拒绝路径上消费挂起的远程失效 | 违反"被拒绝的写入不得推进任何实例状态" |

## 8. 已知取舍

- 合并深度为 1（见 §4）：三方及以上同基线并发写入时，第三者收到
  `ErrKindStaleBaseline`，需重读重试。这是 O(1) 判定预算的直接推论。
- 远程订阅是类型级的：只要类型上注册了声明某远程属性的钩子，
  该类型的所有实例都会订阅。可能产生保守的（误报方向上的）失效，
  但绝不会漏报，且符合"声明即依据"的规范。
- 校验钩子的 `Validate` 在 `propMu` 临界区内执行，其实现不得重入
  `Store`（在 `ontology/store.go` 的提交路径注释中声明）。

## 9. 本地验证方法

```bash
# 全部单元测试与等价性测试
go test ./...

# 竞态检测 + 多次重复（并发等价性）
go test -race -count=20 ./...

# 关键用例
go test -run TestDisjointWritesBothCommitAndMerge ./ontology
go test -run TestHookReadSetIndirectConflict ./ontology
go test -run TestRemoteReadSetConflictAcrossLinkedInstances ./ontology
go test -run TestConcurrentWritesEquivalentToNaiveSerial ./ontology
go test -run TestConflictCheckCostIndependentOfHistory ./ontology

# 演示
go run ./cmd/server
```

测试覆盖矩阵：

- 属性子集相交：`TestIntersectingWriteSetsConflict`
- 属性子集不相交：`TestDisjointWritesBothCommitAndMerge`、
  `TestHookReadSetDisjointBothCommit`、`TestRemoteUnrelatedPropNoConflict`
- 仅经钩子读集合间接相关：`TestHookReadSetIndirectConflict`（静态声明）、
  `TestHookDynamicReadSetIndirectConflict`（按写入动态声明）、
  `TestRemoteReadSetConflictAcrossLinkedInstances`（跨实例）
- 判定顺序与互斥：`TestDeletedTakesPriorityOverPropertyConflict`、
  `TestStaleBaselineConflict`
- 拒绝无副作用：`TestRejectedWriteChangesNothing`、
  `TestRejectedWriteKeepsPendingRemote`
- 版本与快照：`TestVersionsStrictlyMonotonic`、`TestSnapshotByteImmutability`
- 随机对照：`TestSerialRandomizedMatchesNaive`（串行操作流逐步比对）、
  `TestConcurrentWritesEquivalentToNaiveSerial`（并发执行 + 日志重放）
- 判定日志可重放：`TestDecisionLogCompleteness`
