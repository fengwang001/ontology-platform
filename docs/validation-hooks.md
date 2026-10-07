# 校验钩子机制设计说明

对象类型与链接类型可挂载多个校验钩子。本文档说明钩子的注册、排序与短路
执行机制的设计取舍、被放弃的方案与本地验证方法。实现位于 `ontology/`
包（`types.go`、`registry.go`、`validate.go`）。

## 核心概念

- **分组（GroupSpec）**：钩子的优先级分组，需先通过 `DeclareGroup` 声明。
  `Priority` 数值越小优先级越高；相同优先级按分组名字典序排列，保证组间
 顺序完全确定，与声明先后无关。`ShortCircuit` 声明组内是否允许短路。
- **钩子（Hook）**：`Validate(ctx, target) (Decision, error)`，注册时归入
 一个已声明的分组，组内标识唯一。
- **快照（Snapshot）**：某次校验调用开始时刻的钩子集合，提取后不可变。
- **报告（Report）**：一次校验调用的完整审计记录，含快照版本、各分组结果、
 各钩子状态与最终判定。

## 关键设计取舍

### 1. 单互斥锁串行化全部变更与快照提取

`Registry` 用一把 `sync.Mutex` 保护全部状态。注册、注销、批量注册、快照
提取都在临界区内完成；每次成功变更把 `version` 加一并分配给该次变更，
快照记录提取时刻的版本号。

因为版本号由同一把锁分配，全部变更天然构成一个全序；任何快照的版本号都
对应该全序下的某个确定时刻。因此并发交织时，每次校验调用观察到的快照与
结论必然等价于"全部操作按某全序串行执行"的结果——这就是线性一致性的
定义，且由构造保证而非事后检验。

钩子**执行**发生在临界区之外（基于已提取的不可变快照），长耗时钩子不会
阻塞注册/注销。

### 2. 快照 = 临界区内深拷贝

快照在锁内把当前存活钩子深拷贝为独立切片。代价是每次校验调用有一次
O（当前存活钩子数） 的拷贝，换来：

- 调用过程中发生的注册/注销绝不影响已开始的调用；
- 同一次调用内任意多次读取快照必然一致（快照是值，不是视图）；
- 无需读写锁升级、无需 MVCC 多版本保留，注销即真正释放。

### 3. 注册顺序号（seq）单调递增，注销即作废

每个钩子注册时分配全局单调递增的 `seq`，组内按 `seq` 升序执行。注销会
删除钩子的 `seq`；以相同分组与标识重新注册时分配全新 `seq`，自然排到组
内当前末尾，不可能"复活"旧位置。正在使用旧快照的调用持有旧 `Hook` 引用，
不受注销与重新注册影响。

### 4. 批量注册 = 单次临界区 + 顺序分配 seq

`RegisterBatch` 在一次锁持有内先校验整批合法性（任一非法则整批不生效），
再按批次内声明顺序依次分配 `seq`。因此：

- 批次对外是单次动作：其他线程的注册不可能插入批次内部（测试
  `TestConcurrentBatchAtomicity` 验证批次在组内顺序中连续）；
- 组内顺序按批次内声明的相对顺序，而非按提交时刻重新打乱；
- 整批只推进一次版本号，在全序中占据唯一位置。

### 5. 组间天然短路，组内策略由分组声明决定

执行引擎按优先级升序逐组执行：

- 某组判定拒绝（无论哪种方式）后，更低优先级分组**不执行**，报告中仅以
  `OutcomeNotExecuted` / `HookNotExecuted` 明确标注为未执行——这是允许
  保留的审计记录；除此之外不产生任何"看似执行过"的痕迹（执行计数、日志
  钩子均不会被触碰，测试用执行记录器断言低优先级钩子零调用）。
- 允许短路的组：首个拒绝后立即停止组内后续钩子（标注 `HookSkipped`），
  组结果为 `OutcomeRejectedShortCircuit`。
- 不允许短路的组：执行完组内全部钩子再汇总，任一拒绝则组结果为
  `OutcomeRejectedAggregate`。

### 6. 钩子异常 = 不可判定，单独上抛

钩子返回 error 时：

- 所在组结果标记为 `OutcomeIndeterminate`，该钩子标注 `HookErrored`；
- 错误包装为 `*HookError`（含分组与钩子标识，支持 `errors.As`/`Is`）
  从 `Validate` 单独返回，**不归并**为普通拒绝；
- 组内剩余钩子跳过、更低优先级分组一律不执行——**不论**该组是否声明
  允许短路（异常不是拒绝，不适用短路语义，直接全局中止）；
- `context` 取消按同一路径处理。

三类组结果（短路的拒绝 / 汇总的拒绝 / 异常的不可判定）在 `Outcome` 上
两两可区分，调用方可分别统计。

### 7. 跳过计数与历史无关

`Report.SkippedCount()` 只遍历本次报告中的钩子记录，而报告只由本次快照
构造；注册表不保留任何已注销钩子的残骸（注销即从 map 与顺序切片中删除）。
因此跳过计数、快照提取、报告构造的开销全部只与**本次快照中的钩子数量**
相关，与系统历史上注册/注销的总次数无关。

可验证方式：`TestSnapshotCostIndependentOfHistory` 与
`TestSkippedCountIndependentOfHistory` 先制造 5000/2000 次注册+注销历史，
再断言快照钩子数与报告记录数等于当前存活钩子数（3），而非历史总量。

## 被放弃的方案

- **读写锁 + 每次执行时现查**：`RWMutex.RLock` 保护下直接遍历注册表执行，
  读锁持有期间注册/注销会阻塞，且长耗时钩子会无限期阻塞变更；改为快照
  后执行，锁只保护 O（存活数） 的拷贝。
- **MVCC / 不可变持久化数据结构（每次变更复制全量）**：变更代价 O（存活数），
  且需要垃圾回收旧版本；当前变更频率远低于校验频率，方向反了。快照方案
  让变更 O(1)、读付出一次拷贝，更贴合校验密集的工作负载。
- **按优先级数值内联在钩子上（无分组概念）**：每个钩子自带优先级，执行前
  全局排序。被放弃是因为组内短路策略是**分组级**声明，且"同组按注册时间
  排序"需要分组边界明确；显式分组让执行语义可声明、可审计。
- **异常归并为拒绝（fail-closed）**：实现更简单，但会把"钩子自身故障"与
  "业务判定拒绝"混为一谈，运维上无法区分告警；规范明确要求单独上抛。
- **跳过低优先级分组时不留任何记录**：完全静默会让审计无法区分"该组不
  存在"与"该组被跳过"；保留 `NotExecuted` 标注的记录可同时满足"无执行
  痕迹"与"可审计"两个要求。

## 并发正确性论证与测试对照

线性一致性由"单锁 + 版本号"构造保证，测试用独立实现反向验证：

`TestConcurrentLinearizability` 让 8 个 worker 并发交织执行单钩子注册、
批量注册、注销与校验调用（钩子行为是标识与目标的确定性函数，覆盖放行/
拒绝/异常三种路径）。每次成功变更记录其版本号，每次校验记录完整报告。
事后把全部变更按版本号串行回放到一个**独立的朴素串行模型**（约百行、
不共享任何实现代码），对每次校验调用，用朴素模型在其快照版本对应的状态
上独立重放执行语义，逐组逐钩子比对：分组顺序、组结果类别、每个钩子的
状态、最终判定、异常是否上抛。任何"观察不到对应全序时刻的快照"都会表现
为比对失败。

## 本地验证方法

```bash
# 全量测试
go test ./ontology

# 竞态检测 + 重复运行（并发测试稳定性）
go test -race -count=3 ./ontology

# 查看线性一致性对照规模
go test -race -v -run TestConcurrentLinearizability ./ontology

# 代码检查
gofmt -l .
go vet ./...
```

## 测试覆盖清单

| 需求 | 测试 |
| --- | --- |
| 组内多钩子全部通过（短路/非短路） | `TestShortCircuitGroupAllPass` / `TestNonShortCircuitGroupAllPass` |
| 组内至少一个拒绝（短路/非短路） | `TestShortCircuitGroupRejectStopsGroupAndLowerGroups` / `TestNonShortCircuitGroupRejectAggregates` |
| 组间短路与低优先级不可观察 | 同上（执行记录器断言零调用 + `NotExecuted` 审计记录） |
| 异常单独上抛、低优先级一律不执行 | `TestHookErrorIsIndeterminateAndPropagated`（两种组配置） |
| 注销后同名重注册排到末尾 | `TestUnregisterReregisterMovesToEnd` |
| 批次内相对顺序 + 批次原子性 | `TestRegisterBatchPreservesIntraBatchOrder` / `TestRegisterBatchAtomic` / `TestConcurrentBatchAtomicity` |
| 调用开始时刻快照、调用内一致 | `TestValidateUsesSnapshotTakenAtCallStart` / `TestSnapshotIsolationFromLaterMutations` |
| 跳过计数与历史无关 | `TestSnapshotCostIndependentOfHistory` / `TestSkippedCountIndependentOfHistory` |
| 并发交织等价于某全序串行执行 | `TestConcurrentLinearizability`（对照朴素串行模型） |
| 快照版本单调、报告可事后核对 | `TestValidateReportsSnapshotVersion` |
