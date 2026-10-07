# 分代孤儿回收子系统设计说明

包路径：`orphanreclaim`（module `ontology`）。

## 1. 模型与术语

- **链接类型贡献方式**（`Config.Rules`）只有两种：
  - `IndependentRetention`（独立保留）：目标对象存在任意一条该类型入边即非孤儿，短路于第一层。
  - `JointRetention`（联合保留）：必须与组内**全部**类型的入边同时存在才共同保留，单独一条不保留。
    联合组在配置侧以「自身 + `JointGroup` 伙伴」声明；相互重叠的组在启动时用并查集合并为一个完整组，
    避免同一类型在多条规则里给出互相矛盾的分组。
- **两代宽限期**：`GraceGen1` 与 `GraceGen2` 均为正整数时钟刻度，允许第二代更短（测试配置 10 / 4）。
  时刻语义统一为 `deadline <= now` 到期，因此「恰好到期」推进、「差一刻」不推进。
- 对象状态只有三种：`GenNone`（存活非孤儿）、`Gen1`、`Gen2`，由 `objectState.gen` 与两个到期堆共同表达。

## 2. 孤儿判定（进入与脱离共用同一套）

判定函数 `evaluate` 固定两层、次序不可调换：

1. 按字典序逐个核对独立保留类型的**计数桶**，命中任一即返回 `independent` 证据；
2. 第一层全未命中时，按组代表键排序后逐组核对：组内每个类型桶都非零才命中，返回 `joint` 证据（完整组成员）；
3. 两层都未命中才是孤儿（证据层 `none`）。

关键取舍：**不为「脱离待回收」另写判定**。链接增删后的 `reassess` 与到期扫描的锁内重评调用的是同一个
`evaluate`，从机制上杜绝两套标准漂移。救回（`rescueLocked`）对第一代/第二代走同一路径——出堆、清空
`gen/since/deadline`，因此第二代救回直接变非孤儿，不回第一代、不留任何代际记忆；再次变孤儿一律重新
从第一代以**当前时刻**起算（`TestGen2Rescue_NoGenerationalMemory`、`TestGen1RescueThenReorphan_RestartsGen1`）。

## 3. 分代状态机与宽限期

- 写操作（`AddLink/RemoveLink`）在同一临界区内完成索引变更与一次 `reassess`：
  - 非孤儿 -> 孤儿：进入 Gen1，记录 `since=now, deadline=now+GraceGen1`；
  - 孤儿（已排队）-> 非孤儿：从当前代堆移除并清空记录；
  - 仍孤儿且已在队列：**不重置计时**（计时只取决于入队时刻，避免抖动续命）。
- `Advance` 在同一临界区内：**先处理 Gen2（清理优先）再处理 Gen1（晋升）**。
  每个对象出堆时都重新跑一次 `evaluate`：到期时已不孤儿则救回；Gen1 仍孤儿则改记 Gen2 并以当前时刻
  重计 `GraceGen2`；Gen2 仍孤儿才 `purgeLocked`。同一对象在一次扫描中不可能「先晋升又清理」，
  `TestNoSimultaneousGenerations` 用两代等长的压力配置专门钉住这一点。
- 清理原子性：`purgeLocked` 在持锁状态下依次摘除全部出边对端的入边索引、删除对象与堆记录，
  然后对受影响目标逐一调用同一个 `reassess`（清理本身就是一批链接删除）。外部级联规则通过
  `CascadePolicy.OnPurge(obj, outLinksSnapshot, at)` 得到**删除已不可分割完成后**的快照通知；
  通知不允许回调引擎，规则本体不在本题重定。

## 4. 错误的四类与固定次序

四类错误互斥，代码中按固定次序只报第一类（`errors.Is` 可判定哨兵）：

1. `ErrObjectNotFound`：目标（及源）对象实例不存在——`validateMutation` 最先核对；
2. `ErrLinkTypeUnconfigured`：链接类型未配置任何贡献方式；
3. `ErrJointRefUndefined`：联合组引用未定义类型（单元素「联合」组无伙伴，也归入此类）——`New` 时核对；
4. `ErrInvalidGracePeriod`：宽限期非正数——在 (3) 之后核对。

因此「目标不存在 + 类型也未配置」只会报 (1)；「联合引用坏 + 宽限期也非法」只会报 (3)。
对应次序钉死在 `TestMutationErrors_FixedOrder` 与 `TestConfigErrors_FixedOrder`。

## 5. 并发等价串行

- 引擎只有**一把 `sync.Mutex`**，所有变更、查询、快照、推进都在锁内完成；临界区内不做 IO、不回调用户代码
  （级联通知收到的是快照且约定不得回调），因此并发历史等价于按某一全局顺序串行——锁获取顺序就是该全序，
  `seq` 单调序号在日志中给出该次序的凭据。
- 队列用带索引的到期最小堆（`container/heap` + `indexOf`），对象在任一时刻只可能出现在一个堆；
  `TestConcurrentMutationAndAdvance` 在 `-race` 下让 6 个增删 worker、推进/时间线程、只读一致性核对线程
  长时间交织，并断言不存在跨代重复登记与悬挂堆索引。
- 「判定已过期但队列记录尚未更新」不可能被外部观察到：到期对象出堆后、状态变更前没有任何解锁点；
  救回、晋升、清理均在同一临界区完成。

## 6. 复杂度不随入边总数增长

入边维护为**按类型计数桶** `inCounts map[string]int64`：增/删一条边只更新一个桶（摊还 O(1)）。
一次判定只读取：独立类型桶（至多 |I| 个，命中即停）+ 联合组内成员桶。核对次数的上界完全由配置决定：

`BucketsChecked ≤ 独立类型数 + Σ(各联合组成员数)`，与该对象入边总数无关。

可验证/可复现的证明方式（测试中直接复现）：

- `TestDecisionChecksBoundedByConfigNotEdgeCount`：对同一对象堆 10、100、1 000、10 000、100 000 条入边，
  每次 `IsOrphan` 后读取引擎探针 `LastBucketsChecked()`，断言其始终不超过配置上界（本配置为 5），
  并在结尾快照核对入边总数确实为 100 000，排除「偷偷没记边」的假通过；
- `TestBucketsCheckedExactValue`：在 1001 条入边下断言核对次数恰为 4（1 个独立桶 + 组 {J1,J2} 的
  首次失败核对 1 次 + 命中组 {X,Y} 的 2 次），给出精确值而非仅给上界。
- 每次判定的 `DecisionRecord.BucketsChecked` 也写入日志，可离线审计。

## 7. 日志

- `DecisionRecord`：序号、时刻、对象、触发原因（`add_link/remove_link/query/advance_recheck`）、
  入参（源、类型）、输入快照（`InCounts` 类型桶）、证据（层 + 命中类型/组成员）、输出（orphan、前代、
  结果动作）与 `BucketsChecked`。
- `AdvanceRecord`：序号、时刻、`Purged`、`Promoted`、到期重评救回表。空推进不记录。

## 8. 被放弃 / 未采用的方案

- **逐条扫描全部入边判定**：实现直白但 O(入边数)，直接违反复杂度要求；该方案只保留为测试神谕
  `NaiveReclaimer`，用于随机差分，不进入生产路径。
- **每代独立定时器 / 后台清扫 goroutine**：会引入定时器与写操作之间的额外并发面，且「恰好到期」语义
  难以复现测试。改为由显式 `Advance()`（可由外部调度器周期调用）配合可注入 `Clock` 推进，时间完全确定。
- **第二代救回退回第一代**：与题目明确冲突（会保留代际记忆）；统一走 `rescueLocked` 清空状态。
- **救回后保留剩余宽限、再次孤儿续期**：与「重新从第一代起算、不得延续已过去时长」冲突，明确放弃。
- **级联回调允许引擎重入**：重入加锁极易死锁且破坏原子边界；改为引擎自己在锁内摘除出边并重评，
  回调只接收快照做外部副作用（审计、通知下游等）。
- 联合组重叠合并最初手写「最小组代表 + map 合并」，边界多且易错，已替换为并查集实现。

## 9. 本地验证方法

```bash
# 本环境 Go 位于 /usr/local/go/bin；构建缓存重定向到可写目录
export PATH=$PATH:/usr/local/go/bin
export GOCACHE=/tmp/gocache

go test ./...                       # 全量
go test -race -count=3 ./...        # 竞态 + 重复（CI 建议）
go test -v ./orphanreclaim          # 逐条用例
go test -run TestRandomDifferentialAgainstNaive ./orphanreclaim
go vet ./... && gofmt -l .
```

用例到需求的覆盖矩阵：

| 需求 | 测试 |
| --- | --- |
| 两层核对次序、独立层短路 | `TestTwoLayerOrdering` |
| 联合组不交叉、重叠组合并 | `TestJointGroupsDoNotCross`、`TestOverlappingJointGroups_Merge` |
| 四类错误固定次序 | `TestConfigErrors_FixedOrder`、`TestMutationErrors_FixedOrder` |
| 宽限恰好到期 / 刚差一刻 | `TestGen1Boundary_ExactlyAndJustBefore`、`TestGen2Boundary_PurgeExactly` |
| Gen1 救回后重新起算 | `TestGen1RescueThenReorphan_RestartsGen1` |
| Gen2 救回无代际记忆 | `TestGen2Rescue_NoGenerationalMemory` |
| 到期瞬间重评防过期清理 | `TestAdvanceRecheckAtDeadline` |
| 原子级联与受影响目标重评 | `TestPurgeCascadeIsAtomic` |
| 并发交织、跨代不重复 | `TestConcurrentMutationAndAdvance`（`-race`） |
| 朴素模型大量随机对照 | `TestRandomDifferentialAgainstNaive`（24 seeds × 1500 ops，每 25 步快照比对） |
| 复杂度不随入边增长 | `TestDecisionChecksBoundedByConfigNotEdgeCount`、`TestBucketsCheckedExactValue` |
| 判定/推进日志含输入输出与证据组合 | `TestDecisionAndAdvanceLogging` |
