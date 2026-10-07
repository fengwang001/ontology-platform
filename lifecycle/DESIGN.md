# 逻辑删除对象的溯源审计与复活协调器 —— 设计说明

包路径：`ontology/lifecycle`（Go，仅依赖标准库）。

## 1. 核心模型

- **对象生命周期 = 一串互不重叠的存活区间（`Interval`）**
  - 每次 `CreateObject` 开启第 1 段；每次 `DeleteObject` 以删除时点关闭当前段；
    每次 `ReviveObject` 开启一个**新**区间，而不是重开旧区间。
  - 区间标识为 `<objectID>#I<seq>`，`seq` 在对象内从 1 单调递增，天然唯一。
- **审计事件（`Event`）归属到区间**
  - 每条事件携带 `IntervalID`、对象内单调的 `Seq` 与全局逻辑时钟时点 `At`。
  - 不变量：事件的 `At` 必落在其所归属区间的 `[StartedAt, EndedAt]` 内；
    跨区间的事件不可能共享区间标识，从结构上杜绝溯源混淆。
- **全局逻辑时钟**：每个被接受的操作（birth/read/write/delete/revive）取一个
  严格递增时点；被拒绝的操作不前进时钟。区间边界因此确定、互不重叠。

## 2. 删除与权限

- 删除记录执行主体（`Actor`）、时点（`At`）与权限快照引用（`PermissionSnapshot`）。
- 权限以**引用 + 时点快照**落审计：快照保存 `PermissionID / Version / Grants /
  WasRevoked / CapturedAt`。权限条目之后被吊销或改写，不影响历史审计的可读性
  （见 `TestRevokedPermissionKeepsAuditReadable`）。
- 删除时按链接类型声明处理 incident 链接：
  - `LinkInvalidatesWithEndpoint`（第一类）：可用链接带上**本次删除时点**进入
    `LinkInvalidated`；已经失效的链接（如对端先前删除导致）**保留原失效时点**，
    绝不覆盖——这是后续“恰好等于”判定能区分不同删除轮次的关键。
  - `LinkKeepsIndependent`（第二类）：完全不触碰，独立于对象存活继续存在。

## 3. 复活与链接恢复

- 复活必须携带 `targetDeleteAt`，且必须**精确等于该对象最近一次删除时点**。
  指向更早的删除事件（跳过中间循环）返回 `ErrStaleDeleteTarget`，与
  `ErrStateMismatch`、`ErrPermissionDenied`、`ErrLinkCondition` 均可区分
  （`errors.Is` + sentinel）。
- 复活事件记录主体、时点、权限快照、`RestoreLinks` 声明以及实际恢复的链接列表。
- 链接恢复条件（四条同时成立，缺一即整体失败）：
  1. 链接以被复活对象为端点；
  2. 类型声明为第一类；
  3. 当前处于失效态；
  4. `InvalidatedAt == 本次删除时点`。
  第 4 条精确排除了更早就因其他原因失效的链接（例如对端先删）。
- **原子性**：复活在持锁状态下先完成全部链接的纯校验，确定候选集合后才开启
  新区间并恢复链接；任一链接不满足条件则整次操作失败，不发生部分恢复，
  区间划分与任何既有链接记录均不变。
- `restoreLinks=false` 时即使存在满足条件的链接也不恢复；`linkIDs` 为空且
  `restoreLinks=true` 时自动选取全部满足条件的链接。

## 4. 拒绝优先级

实现中按以下固定顺序短路判定，并用测试 `TestRejectionPriority` 锁定：

1. 操作本身权限不足（`ErrPermissionDenied`）；
2. 对象状态与请求不符（`ErrStateMismatch` / 常规读写对删除对象报
   `ErrObjectDeleted`）；
3. 复活指向的删除事件不是最近一次（`ErrStaleDeleteTarget`）；
4. 链接恢复条件不满足（`ErrLinkCondition`）。

## 5. 审计查询

- `Audit(id)` 不受删除状态限制，始终返回全部区间与区间内全部事件的**深拷贝**。
- 所有操作与审计在同一把 `sync.Mutex` 内完成“校验 + 变更”或一致性快照读取：
  并发调用可线性化，等价于某个全局串行顺序；审计结果对应该顺序的一个完整
  前缀，不会遗漏或重复任何区间（`TestConcurrentSerializability` 持续在
  `-race` 下校验该不变量）。

## 6. 与规模无关的性能要求及证明

- 判定单条链接是否可恢复，只读取该记录自身的 `TypeID / Status /
  InvalidatedAt` 三个字段，与对象经历的删除复活循环次数 N 完全无关，为 O(1)。
- 协调器内置计数器 `Stats.LinkChecks` 与
  `Stats.HistoryScansDuringLinkChecks`：后者在任何路径上都不递增（恒为 0），
  作为“不扫描历史”的可验证证据；`TestLinkCheckIsConstantTimeRegardlessOfCycles`
  在 8 轮删除复活循环中断言每轮对固定 2 条链接的判定次数恒为 2，且扫描计数恒 0。
- 自动选取模式需要枚举 incident 链接集合（O(K)，K 为链接数），但每条链接的
  合格性判定本身仍是 O(1) 字段比较，不随 N 增长。

## 7. 关键取舍

- **选择“复活即新区间”而不是“复活重开旧区间”**：旧区间的关闭时点是历史事实，
  重开会让已关闭区间的边界发生回移，破坏审计前缀的不可变性，也让跨区间事件
  归属变得模糊。新区间 + `TargetDeleteAt` 指针既保留了“延续哪一次删除”的
  溯源链，又保证历史只增不改。
- **选择逻辑时钟而不是物理时间**：物理时钟在并发下可能回退/同点，无法保证
  区间边界严格有序与可串行化；逻辑时钟在锁内分配，简单且可验证。
- **选择快照内嵌权限关键字段而非仅存外键**：纯外键在权限条目被吊销/删除后
  无法解释“当时凭什么允许”；快照保留授权事实，同时保留 `PermissionID` 维持
  引用语义。
- **选择全局单锁而不是分对象锁 + 链接锁表**：链接跨对象，多锁会引入锁序与
  死锁复杂度；本方案单锁即满足正确性与可线性化，是刻意以吞吐换取可审计的
  强一致。若未来需要扩展，可在同一接口下替换为带严格锁序的分域锁或
  serializable 事务存储。

## 8. 被放弃的方案

- **按失效区间集合恢复链接**（恢复时扫描链接的全部历史失效记录）：判定成本
  随循环次数线性增长，直接违反性能要求，且需要为链接额外保存历史，放弃。
- **复活自动“重放”上一段存活期的全部链接**：会把对端已删除、类型已变更等
  不应恢复的链接一并带回，放弃；改为严格的“失效时点恰好等于本次删除时点”。
- **异步日志 / 先变更后校验**：会让拒绝操作产生可观察副作用或日志与状态错位，
  放弃；所有判定日志在锁内、变更决策点同步写出。

## 9. 本地验证方法

```bash
# 需要 Go 1.26+；如 go 不在 PATH：export PATH=$PATH:/usr/local/go/bin
# 若 HOME 下构建缓存只读：export GOCACHE=/tmp/go-cache

go test ./... -race -v          # 全量测试（含竞态检测）
go test ./lifecycle -run TestRandomDifferential -v   # 随机差分（对照朴素模型）
go vet ./... && gofmt -l .      # 静态检查
go test -cover ./lifecycle      # 覆盖率
```

测试覆盖面（按需求逐项）：

1. 多次删除复活循环下溯源链的唯一归属：`TestIntervalProvenanceUniqueAcrossCycles`；
2. 两类链接失效行为差异：`TestTwoLinkBehaviorsAndRestore`；
3. 更早失效链接不得恢复 + 失败原子性：`TestEarlierInvalidatedLinkMustNotRestore`；
4. 跳过循环指向错误历史删除事件：`TestStaleDeleteTargetRejected`；
5. 审计在对象删除态下仍可用：`TestDeletedObjectBlocksIOButAuditAlwaysAvailable`；
6. 拒绝优先级四类错误可区分：`TestRejectionPriority`；
7. 权限吊销后历史审计可读：`TestRevokedPermissionKeepsAuditReadable`；
8. 与独立朴素模型的随机差分：`TestRandomDifferential`（错误类别、存活状态、
   区间数、事件顺序与归属、复活指向、链接状态逐步比对）；
9. 并发可线性化：`TestConcurrentSerializability`；
10. O(1) 判定的可验证证据：`TestLinkCheckIsConstantTimeRegardlessOfCycles`；
11. 每次判定打印输入/输出/依据：`TestDecisionLogging`（JSON 行日志）。
