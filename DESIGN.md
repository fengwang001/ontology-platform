# 属性索引一致性子系统 — 设计说明

## 目标

为对象实例的被索引属性维护辅助索引，使索引内容在任何时刻（含崩溃、并发）
都与实例的真实属性值一致；索引查询只随命中结果数量增长。

## 核心抽象

- `Value`：属性值。`Present=false` 是"属性当前不存在"的特殊标记（`Absent`），
  与显式写入的默认值（`Present=true, Data=""`）严格区分。
- `KeyFunc`：声明被索引属性时指定的索引键生成规则。`Absent` 统一映射到保留键
  `AbsentKey`，因此"不存在"与"显式默认值"落在不同的索引桶，查询不会混淆。
- `Index`：一种索引结构（哈希桶 `map[Key]set[instanceID]`）。同一属性可挂多个
  索引结构（精确、前缀、长度桶……），键规则各异。
- `WAL`：先写日志。每个处理单元（单写 / 批量写 / 删除）一条记录，内含全部
  受影响实例的 undo/redo 信息（旧值、旧键、新值、新键）。
- `Store`：核心。写入路径 = 校验 → WAL begin → 应用（改值 + 改全部索引）→
  时钟 +1 → WAL commit → WAL end。

## 关键取舍

### 1. 处理单元的原子性：WAL + undo/redo，而非影子拷贝

每个处理单元先把"写入前的完整状态"（属性值 + 每个索引结构的键）记入 WAL，
再就地应用。崩溃恢复时：

- WAL 中有 commit 记录 → **前滚**（按 redo 幂等重放，set 语义天然幂等）；
- 无 commit 记录 → **回滚**（按 undo 同时恢复属性值与全部索引条目，
  undo 记录同时包含两边状态，从结构上杜绝"只回退一边"）。

恢复只扫描 WAL 中**未完成的事务记录**，每条记录只触碰自己涉及的几个条目，
开销 O(未完成事务数 × 每事务条目数)，与索引中已有条目总数无关
（`TestRecoveryCostIndependentOfIndexSize` 验证）。

**放弃的方案**：

- *全量校验和/启动时全索引重建*：恢复开销随索引规模增长，违反约束，放弃。
- *影子写（copy-on-write）+ 原子切换*：每个索引结构都要维护双份桶，
  多索引结构下切换点无法原子化，放弃。
- *两阶段加锁 + 查询读未提交*：查询会观察到中间态，违反一致性要求，放弃。

### 2. 查询一致性：串行点 + 读校验双保险

- 写入/删除的"应用段"持 `applyMu` **读锁**（彼此不阻塞），查询持 `applyMu`
  **写锁**。因此查询不会与任何处理单元的应用段重叠，查询结果等价于发生在
  某个与写入次序一致的时间点（可线性化）。
- 查询命中索引桶后，再回读实例当前属性值做校验：即使崩溃后尚未恢复
  （索引残留半成品），也不会返回陈旧或幻影结果。

查询开销 = 1 次哈希桶定位 + 命中条目数次访问。`Index.visits` 计数器记录
实际访问的条目数，`TestQueryCostIndependentOfInstanceCount` 用 5000 个实例、
3 条命中证明访问数等于命中数、与实例总数无关——无需遍历全部实例。

### 3. 并发控制：细分锁，同属性串行、异属性并行

- `(实例, 属性)` 粒度的互斥锁：同一属性的并发写入被串行化，锁获取顺序即
  串行顺序，值与索引在同一临界区内一起更新，最后一次写入的值与索引条目
  必然对应（`TestConcurrentSamePropertySerialEquivalence`）。
- 不同属性用不同的锁、不同的值单元（`sync.Map`）、不同的索引结构，
  互不阻塞（`TestConcurrentDifferentPropertiesUnblocked`）。
- 实例级 `RWMutex`：写入持读锁、删除持写锁，删除与写入互斥。
- 锁顺序统一为 `applyMu → 实例锁 → 属性锁 / 全局 map 锁`，避免 ABBA 死锁。

### 4. 批量写入：单事务、校验先行、失败整体回滚

批量写入是一个 WAL 事务（undo 含全部实例）。先校验所有实例存在性与属性
可索引性，校验失败则**什么都不改**（属性值、索引、时钟戳都不变）；应用阶段
任一实例的索引维护失败 → 已应用条目按 undo 全部回退，逐实例返回错误：
失败实例报 `ErrIndexMaintenance`，其余报 `ErrBatchRolledBack`。

错误优先级（高 → 低）：`ErrInstanceNotFound` > `ErrPropertyNotIndexable` >
`ErrIndexMaintenance` > `ErrBatchRolledBack`。校验先行天然实现了前两级的
优先级；`HighestPriorityError` 在需要汇总时按同一顺序挑选
（`TestErrorPriority`）。

### 5. 崩溃模拟：切分点注入

`FaultInjector` 在处理单元的 5 类切分点提供钩子：WAL begin 后、属性值改后、
每个索引结构更新后、全部索引更新后、commit 记录写后。注入即 panic
（模拟进程死亡），随后 `Recover()` 判定该事务已提交（前滚）或未提交（回滚）。
`crash_test.go` 对单写 / 批量写 / 删除逐切分点验证。

### 6. "不存在"作为一等索引状态

实例创建 / 属性注册时即以 `AbsentKey` 回填索引，因此 `QueryAbsent` 也是
O(命中数) 的索引查询，且与显式默认值（普通键）天然分桶。

## 本地验证方法

```bash
export PATH=$PATH:/usr/local/go/bin   # 如 go 不在 PATH

go build ./...          # 编译
go vet ./...            # 静态检查
gofmt -l .              # 格式（无输出即通过）
go test ./...           # 全量测试
go test -race ./...     # 竞态检测
go test -v -run TestDifferentialRandomOps ./ontology   # 对拍测试（打印日志样本）
go run ./cmd/server     # 演示：写入/查询/批量回滚/崩溃恢复
```

测试覆盖：

| 需求 | 测试 |
| --- | --- |
| 单实例单属性写入一致性 | `TestSingleWriteIndexConsistency` |
| 批量部分失败整体回滚 | `TestBatchPartialFailureRollsBackAll` |
| 各切分点中断恢复判定 | `TestCrashRecoveryAtEveryCutPoint` / `TestCrashRecoveryDelete` / `TestCrashRecoveryBatch` / `TestCrashAtEveryIndexBoundary` |
| 不存在 vs 显式默认值 | `TestAbsentVsExplicitDefault` |
| 删除与索引清除一致性 | `TestDeleteRemovesAllIndexEntries` / `TestConcurrentWriteDelete` |
| 多索引结构同步维护 | `TestMultipleIndexesStayInSync` |
| 并发同属性串行等价 | `TestConcurrentSamePropertySerialEquivalence` |
| 并发异属性互不阻塞 | `TestConcurrentDifferentPropertiesUnblocked` |
| 查询开销 O(命中数) | `TestQueryCostIndependentOfInstanceCount` |
| 恢复开销与索引规模无关 | `TestRecoveryCostIndependentOfIndexSize` |
| 错误类别与优先级 | `TestErrorPriority` |
| 与朴素全表扫描模型对拍 | `TestDifferentialRandomOps`（800 步随机序列，含随机崩溃恢复） |

日志：每次写入/查询都会记录输入、索引条目增删（`INDEX+`/`INDEX-`）、
提交/回滚及判定依据（`COMMIT`/`ROLLBACK`/`RECOVER ... reason=...`），
见 `BufferLogger` 与各测试的 `t.Log` 输出。
