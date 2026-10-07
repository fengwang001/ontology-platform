# 批量导入校验钩子可见性子系统 — 设计说明

## 1. 三个协作模块

代码位于 `ontology/` 包，按职责拆成三个协作角色：

| 角色 | 源文件 | 职责 |
| --- | --- | --- |
| 批量导入分批执行模块 | `importer.go` | 取快照、按列表顺序编排、两语义分支、后置钩子时机、提交/撤销/冲突重放 |
| 钩子触发与批内可见性解析模块 | `overlay.go`、`store.go` | 快照、批内前缀效果、增量派生聚合、`ScopedView` 查询、逆序撤销日志 |
| 错误归一化模块 | `errors.go`、`FirstError` | 三类错误可区分，并按固定次序只报首个命中原因 |

另有 `registry.go`（对象类型、字段 schema、钩子注册表）与 `naive.go`
（独立维护全量状态、严格逐条应用并同步触发钩子的朴素参照模型，仅供差分测试）。

## 2. 批内可见性：为什么是「前缀」而不是快照或最终态

每条记录在应用前拿到一个 `ScopedView`，查询顺序固定为：

1. 先查本批 `overlay.pending`（截至当前、已通过前置钩子并应用的前缀）；
2. 未命中再查批次开始时的一致性只读快照 `Snapshot`。

因此第 `i` 条记录看到的恰好是「已提交状态 ∪ 本批 `[0,i)` 中已通过者的效果」：

- 不是批次开始前静态快照——能看到前面已通过记录；
- 不是整批完成后的最终态——排在后面、尚未处理的记录不在 `pending` 中；
- 失败的记录从不调用 `apply`，因而不进入 `pending`，
  尽力而为语义下对后续记录天然不可见。

派生值（聚合）采用**增量维护**：基线在批次构建时基于快照一次性算出，
每次 `apply` 做 O(1) 增量更新。钩子读取 `Aggregate` 恒为当前前缀的确定值，
与逐条朴素模型「每步全量重算」的结果一致。

## 3. 两种整体语义

### 全有或全无 `ALL_OR_NOTHING`

- 阶段 0：主键重复（结构性参数非法）→ 整批拒绝，不触发任何钩子。
- 阶段 1：并行做全部字段类型校验；只要有一条类型不符，取列表中首个，
  整批拒绝（保证参数非法优先于前置钩子）。
- 阶段 2：严格按列表顺序逐条「前置钩子 → apply」；任一前置失败立即停止，
  其后记录标记为 `SKIPPED_UNPROCESSED`（根本未处理），
  并对已应用前缀执行 `UndoAll`，不提交。
- 阶段 3：全部前置通过后、提交前，批次级后置钩子触发一次，
  看到全部记录已应用但尚未提交的最终状态；后置失败 → 全部记录
  标记 `batch_rolled_back` 并整批撤销。
- 通过后置钩子后一次性原子提交。

### 尽力而为 `BEST_EFFORT`

- 每条记录独立判定：类型错误或前置失败即跳过（不 apply、不进可见范围），
  不影响后续记录。
- 处理结束后把所有已应用记录提交；**不触发**批次级后置钩子。
- 返回与输入同序的每条成功/失败及原因清单。

### 撤销顺序与状态一致性

`overlay` 为每次 apply 记录逆序撤销日志 `(pk, existed, previous)`，
`UndoAll` 逆序回放：新建键删除、更新键恢复原实例（深拷贝），
并把增量聚合重置到快照基线。快照与存储之间通过深拷贝彻底切断引用别名，
`Store.Commit` 同样存入独立拷贝。因此撤销后状态与批次开始前逐字节一致。

## 4. 错误归一化与优先级

`HookError.Kind` 三类可区分：`param_invalid` / `pre_hook` / `post_hook`。
`FirstError` 不按「首次出现」而按**类别优先级**返回：

1. 参数非法（主键重复、字段类型不符）；
2. 单条前置钩子失败（取列表中首个）；
3. 批次级后置钩子失败（仅全有全无）。

这也是为什么全有全无时字段类型校验必须先于任一前置钩子整体完成——
否则靠后位置的类型错误会被更早的前置失败掩盖，违反优先级。

## 5. 确定性与并发

### 内部并发度不改变结果

顺序可见性意味着第 `i` 条的可见集合依赖第 `i-1` 条的成败，
因此「前置钩子 → apply」这一语义链在一般钩子下**本质串行**
（无法在不违反「只看到已通过前缀」的前提下真正并行执行前置判定）。
可安全并行的只有无共享可变状态的字段类型校验阶段，它以可配置 worker 池执行；
并通过 `WithJitter` 在每步注入随机调度延迟。无论并发度或调度如何，
结果只取决于输入列表顺序。测试以 workers ∈ {1,2,4,8,16} + 随机抖动验证结果向量完全一致。

### 跨批次可串行化

采用「快照读 + 每类型版本号 + 提交时版本检测 + 冲突整批重放」：
`Store.Commit` 在互斥下比对基线版本，若已被其他批次推进则返回冲突，
执行器基于新快照重放整个批次。钩子必须是状态的纯函数（不依赖外部随机源），
因此重放得到同样的逐条清单。最终每个批次一次原子提交、版本号单调，
等价于某个全局串行顺序。

## 6. 单次可见性解析的开销（可验证证明）

需求要求：一次解析开销不随批次总长度增长，只与实际访问记录数相关。
实现层面：

- `ScopedView.Get/Exists/Field` 至多做两次哈希探测（pending + snapshot），O(1)；
- `ScopedView.Aggregate` 读增量值，一次哈希探测，O(1)；
- 代码中不存在任何对批内已应用列表的遍历（`PrefixScans` 恒为 0）。

`AccessCounters{Probes, PrefixScans}` 记录每个钩子的底层探测次数，
作为可验证证据。`TestVisibilityResolutionIsConstantPerAccess`
在 N = 128/1024/4096 下断言每个只做固定访问的钩子探测数恒为常数、
`PrefixScans` 恒为 0；`TestProbesTrackAccessedOnly` 断言探测数
只被实际访问条数（2·k）约束，与位置之前的总条数无关。

## 7. 被放弃的方案

- **每条记录一个不可变状态版本（版本链表/持久化 map）**：
  试图借此真正并行跑前置钩子。但一般钩子对「已通过前缀」的依赖
  使得第 i 个版本必须等第 i-1 条成败确定后才能确定，并行性并不成立，
  且带来结构复杂度与内存开销，放弃。
- **查询时用「快照 + 全量 delta 列表」线性叠加**：
  单次解析最坏 O(N)，直接违反复杂度要求，放弃。
- **每步全量重算聚合（朴素模型做法）**：
  语义最直白但每步 O(存量+前缀)，只保留在测试基准中，生产路径改用增量维护。
- **整段持全局写锁跑钩子**：实现简单但跨批次完全失去并发；
  改为快照 + 乐观版本检测 + 冲突重放。

## 8. 本地验证方法

需要 Go 1.26+（仓库 `go.mod` 已固定）。

```bash
# 若 go 不在 PATH
export PATH=$PATH:/usr/local/go/bin
export GOCACHE=/tmp/gocache   # 仅当默认缓存目录只读时需要

go test ./...
go test -race -v ./...          # 竞态检测 + 打印输入/输出/判定依据
go test -run TestRandomDifferential -count=5 ./...  # 2000 组随机差分
go vet ./...
gofmt -l .
```

测试用例与需求的对应：

| 需求点 | 测试 |
| --- | --- |
| 可见性严格按前缀顺序 | `TestVisibilityIsPrefixOrdered`、`TestSemanticsDiffer` |
| 两语义在同一输入下差异 | `TestSemanticsDiffer` |
| 后置失败整批撤销 / 尽力而为不触发后置 | `TestPostHookFailureRollsBackWholeBatch`、`TestPostHookSkippedUnderBestEffort` |
| 错误优先级与三类可区分 | `TestErrorPriorityAndKinds` |
| 不同内部并发度结果一致 | `TestSameResultAcrossInternalConcurrency` |
| 跨批次可串行化 / 重放确定性 | `TestConcurrentBatchesAreSerializable`、`TestReplayDeterminism` |
| 解析开销不随批长增长 | `TestVisibilityResolutionIsConstantPerAccess`、`TestProbesTrackAccessedOnly` |
| 与朴素模型大量随机对照 | `TestRandomDifferentialAgainstNaiveModel`（400 组随机序列，打印输入/实际/基准/依据） |
