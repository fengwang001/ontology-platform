# 溯源审计与复活协调器 — 设计说明

本文档记录逻辑删除对象的生命周期溯源、审计与复活协调器（`ontology` 包）
的关键取舍、被放弃的方案与本地验证方法。

## 模型

- **存活区间（Interval）**：对象生命周期由若干段区间构成。创建/复活开启
  一段区间，删除关闭当前区间。区间标识为 `<object>#<序号>`，全局唯一，
  审计事件通过 `Event.Interval` 精确归属到一段区间，跨区间不会混淆。
- **逻辑时钟（LogicalTime）**：所有被接受的状态变更在同一把互斥锁内
  按序取得单调递增的逻辑时间。逻辑时间的全序即系统对外可观察的全局
  串行顺序；不同事件的时间绝不相同。
- **审计事件（Event）**：删除/复活事件记录执行主体、逻辑时点与权限条目
  **快照**（`Event.Grant` 是操作发生时刻条目内容的拷贝）。条目之后被
  吊销不影响历史记录的可读性。复活事件额外记录 `ResumesDeletion`
  （延续的删除事件）、`RestoreLinks`（是否声明恢复链接）与
  `RestoredLinks`（实际恢复集合）。
- **链接（Link）**：链接类型声明 `CascadeInvalidate`（随任一端对象删除
  失效）或 `Independent`（独立存在）。级联链接失效时记录
  `InvalidatedAt`（失效时点），且**首次失效生效**：已失效的链接不会被
  后续删除覆盖失效时点。

## 关键取舍

1. **单互斥串行化**。全部操作（含审计查询与常规读写）在同一把
   `sync.Mutex` 内完成。整体效果天然等价于某个全局串行顺序，任一时刻
   的审计查询都反映该顺序下某个前缀的完整历史。被拒绝的操作在锁内、
   任何状态变更之前返回，不改变对象状态、区间划分或链接记录。
2. **O(1) 的链接恢复条件判断**。复活只允许恢复“失效时点恰好等于本对象
   最近一次删除时点”的级联链接。由于逻辑时间全局唯一，失效时点等于
   删除时点当且仅当该链接正是被这次删除失效的，因此判断是字段比较
   `link.InvalidatedAt == obj.lastDeletion.Time`（`restorable`，
   `ontology/coordinator.go`），不需要扫描任何历史记录，与删除复活
   循环总次数无关。由 `BenchmarkRestorableCheck` 可验证地证明
   （cycles=10/100/1000/10000 各档耗时持平）。
3. **先校验、后变更的复活原子性**。复活按固定优先级依次校验：
   权限不足 → 状态不符（对象仍存活）→ 指向的删除事件非最近一次 →
   链接恢复条件。全部通过前不做任何变更；显式链接列表中任一链接不满足
   条件，整次复活失败，不存在部分恢复。
4. **首次失效生效**。链接一旦被某次删除失效，其失效时点不再被之后的
   删除覆盖。这保证“另一端对象先被删除导致链接更早失效”的场景中，
   本对象的复活不会错误地复活该链接。
5. **读写计入审计**。常规读写事件也写入当前存活区间的事件列表，
   使“每段区间内发生的全部操作”字面成立；已删除对象上的读写被拒绝
   （`ErrObjectDeleted`），因此读写事件永远不会落入错误的区间。

## 被放弃的方案

- **RWMutex + 只读审计查询**：审计查询若走读锁，需要额外机制保证
  读到的一致前缀（如版本化快照或 MVCC），复杂度显著高于收益；当前
  单锁方案以可预期的吞吐换取无条件的线性一致性。
- **按 `InvalidatedBy`（失效者）判断可恢复性**：记录“被谁的删除失效”
  看似直观，但同一对象相邻两次删除之间若链接被复活又失效，失效者相同
  而失效时点不同，仍需比较时点；直接比较全局唯一时点更简洁且充分。
- **扫描历史判断恢复条件**：朴素模型（`ontology/naive_test.go`）采用
  该方案，复杂度随循环次数线性增长，仅作对照参照，不用于生产路径。
- **复活时部分恢复、部分跳过**：会破坏审计的可解释性（无法从事件
  判定哪些链接被有意跳过），改为整次失败、由调用方修正请求后重试。
- **复活不指向删除事件、隐式延续最近一次**：隐式选择在并发重试下
  容易静默延续错误的删除（例如客户端基于过期视图重试），显式
  `ResumesDeletion` 让错误可检测、可归因（`ErrStaleDeletion`）。

## 错误类别

| 错误 | 含义 |
| --- | --- |
| `ErrPermissionDenied` | 权限条目缺失/已吊销/主体、动作、对象不匹配 |
| `ErrObjectDeleted` | 对象已删除，常规读写或重复删除被拒绝 |
| `ErrObjectNotDeleted` | 对象仍存活，复活被拒绝 |
| `ErrStaleDeletion` | 复活指向的删除事件并非最近一次 |
| `ErrLinkRestore` | 显式请求的链接不满足失效时点条件 |
| `ErrNotFound` / `ErrAlreadyExists` | 引用不存在 / 标识冲突 |

均可用 `errors.Is` 区分；拒绝优先级：权限 > 状态 > 链接恢复条件。

## 本地验证方法

```bash
# 单元测试 + 随机对照 + 并发线性一致性（含竞态检测）
go test -race -v ./ontology

# 性能要求的可验证证明：各档循环次数下耗时应持平
go test -run=NONE -bench=RestorableCheck -benchmem ./ontology

# 演示程序
go run ./cmd/server

# 代码检查
gofmt -l .
go vet ./...
```

测试覆盖面：

- `TestIntervalAttribution`：多次删除复活循环下溯源链的唯一归属
  （区间标识唯一、边界不重叠、复活精确指向前一次删除）。
- `TestLinkPolicies` / `TestReviveRestoresOnlyMatchingLinks` /
  `TestReviveExplicitLinkConditionFails`：两类链接失效行为差异、
  仅恢复失效时点匹配的链接、显式请求不满足条件时整次失败。
- `TestStaleDeletionPointer`：跳过循环指向更早删除事件报
  `ErrStaleDeletion`。
- `TestDeletedObjectAccess`：删除状态下常规读写报“对象已删除”，
  审计查询仍可用。
- `TestRejectionPriority` / `TestGrantRevocationKeepsAuditReadable`：
  拒绝优先级与吊销后历史可读性。
- `TestRandomSequenceAgainstNaiveModel`：随机操作序列与独立朴素模型
  逐步对照，日志打印每次判定的输入、输出与依据。
- `TestConcurrentLinearizable`：并发下任一时刻历史满足溯源链结构
  不变量，最终事件时间全局唯一（等价于某个串行顺序）。
