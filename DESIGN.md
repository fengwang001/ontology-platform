# 跨对象类型聚合视图子系统 — 设计说明

## 1. 问题与语义

一个聚合视图由 `ViewSpec{Name, Path, Attr}` 声明：

- `Path.Types`：长度 `n+1` 的对象类型集合序列，`Types[0]` 是起点层、`Types[n]` 是终点层；
- `Path.Links`：长度 `n` 的链接（边类型）名序列，第 `i` 跳使用 `Links[i]`，其源/目类型必须被 `Types[i]`/`Types[i+1]` 允许；
- 对每个起点实例，沿恰好 `n` 跳可达的**终点实例集合**，取其 `Attr` 数值属性的最大值；
- 终点集合为空（或无一带该属性）时，结果是明确的“不存在”状态（`Aggregate.Present=false`），不使用 0 等默认值冒充。

关键语义：

1. **去重**：同一终点经任意多条中间路径到达，只贡献一次。内部仍保留路径条数计数（`reach` 的值），但聚合按集合键 `(start,end)` 计算。
2. **环安全**：路径发现使用“逐层 DP”，层数恰好等于声明长度 `n`。环只会让某实例在同一层的路径计数累加，展开深度有界，绝不递归发散；环上实例若落在终点层则自然计入一次。
3. **精确受影响集**：一跳链接增删后，只有“可达终点集合发生增/减”的起点进入受影响集（`diffAffected`），路径条数变化但集合不变（重复路径增删）不产生聚合维护。
4. **属性变更定向传播**：维护反向索引 `endRefs[end] -> {start}`，只有当前真实可达该终点的起点才可能受影响；属性变大单次比较；变小仅当该终点正是当前 `maxEnd` 时才重定源。

## 2. 核心数据结构

每个视图一份 `snapshot`（`ontology/internal.go`）：

- `reach map[reachKey]int64`：`(起点,终点)` 路径条数，键集合即可达关系（去重语义）；
- `agg map[start]*aggInfo`：`{present,value,maxEnd}`，其中 `maxEnd` 记录当前最大值来源，是“属性变小是否需要重定源”的判定依据；
- `endRefs map[end]set<start>`：终点→可达它的起点集合，支撑属性写入的定向传播；
- `rescanCost`：重定源时考察过的终点数，用于开销证明与测试断言。

图存储：对象表 + 属性表；链接按 `out[link][src][dst]=重数`、`in` 反向邻接存储，同一对端点允许多重边。

## 3. 关键算法

### 3.1 全量构建（朴素但环安全）

`buildSnapshot` 对每个第 0 层起点做一次长度 `n` 的逐层计数 DP（`view.go`）：

```
layer_0 = {start:1}
layer_{k+1}[v] = Σ_{u, 边 u->v∈Links[k]} layer_k[u] * 边重数   （v 的类型必须在 Types[k+1]）
```

末层所有计数 >0 的实例构成去重后的终点集合，随后线性扫描一次得到最大值与 `maxEnd`，并同时建立 `endRefs`。
复杂度 `O(|S0| * 路径工作量)`，深度恒为 `n`，与环无关。

### 3.2 链接增删的增量维护

采用 **copy-on-write 快照 + 集合差分 + 原子指针提交**（`maintain.go`）：

1. 边在图上先完成增/删；
2. 对每个“该边与某跳链接名且两端类型匹配”的视图（`hopMatches`），在旧快照的副本上重算 `buildSnapshot`；
3. 用 `diffAffected(old.reach, new.reach)` 求**可达集合**发生变化的起点集合；
4. 所有视图均成功后一次性把 `vs.snap` 指针指向新快照；任何失败则一个指针都不改（加上调用方撤销边变更），等价事务回滚。

### 3.3 属性变更与“重定源开销上界”

`SetAttr`（`attr.go`）对每个以该属性为汇总属性、且 `endRefs[id]` 非空的视图：

- 变大/新增：仅与当前最大值比较一次，不做扫描；
- 变小：仅对 `agg[start].maxEnd == id` 的起点调用 `redetermine`；
- `redetermine` 只遍历 `reach` 中 `start` 等于该起点的键，即**它当前真实可达的终点**，逐个取当前属性求最大值。

因此单次重定源考察的终点数严格等于该起点可达终点集合的大小，`RescanCost` 精确累计该数字，测试据此断言“不超过真实可达终点数”。不相关起点在 `endRefs` 中根本不出现，绝不被扫描。

### 3.4 环的声明期静态排除与运行时拒绝

- `staticallyCycleFree`：当路径各位置类型集合**两两不相交**时，同一实例不可能占据两个位置，环可在声明阶段静态排除，`RejectCycle` 对这类视图不产生拒绝。
- 否则环是合法且被正确聚合的；但当一次 `AddLink` 显式携带 `RejectCycle` 时，`detectCycle` 精确判断**新边是否参与**某条完整路径上的实例重复（`src` 可在第 k 层被某前缀到达，且跨过新边后沿后续跳能再次回到 `src`），命中则以 `KindCycleRejected` 拒绝整次变更并回滚。检测本身也是深度有界的逐层集合展开。

### 3.5 结构性变更：跳类型集合新增成员

一个链接名可以声明多组允许的 `(源类型,目类型)` 对（`AddLinkType` 可多次调用）。`AddTypeToHop(view,pos,typ)` 校验新类型与相邻链接已声明的类型对兼容后，把类型追加进 `Types[pos]` 并重算。放宽只可能新增连通，绝不可能让既有连通失效，随后与朴素模型对拍确认。

### 3.6 并发与串行等价

`Graph` 用单一 `sync.RWMutex` 串行化所有变更；每个变更是“图边/属性 + 全部视图快照指针”在同一把写锁内完成的临界区，查询持读锁返回一致快照。因此查询观察到的状态恒等于“按某一串行顺序应用已提交变更后、依据当前真实连通关系与属性值重算”的结果，绝不会读到部分更新的中间态。`Query` 只读，不修改任何状态。

### 3.7 错误模型与固定优先级

`OntoError.Kind` 取值，从高到低：

1. `KindTypeMismatch`：路径声明类型不匹配、链接未声明、端点类型不被签名允许、结构扩展类型不兼容；
2. `KindInstanceNotFound`：起点/终点对象实例不存在（仅在类型约束通过后才报告）；
3. `KindCycleRejected`：无法静态排除环且运行时检测到新边成环；
4. `KindMaintenanceRollback`：增量维护提交前失败（测试钩子 `injectFailure` 注入），整体回滚。

多条件同时满足时按上述顺序报告（见 `checkLinkEndpoints`：先施加类型约束，再判实例存在）。

## 4. 关键取舍与被放弃的方案

- **放弃“局部前缀/后缀计数传播”的精细增量**：它在“同一对象类型出现在多个路径位置”时必须处理跨层别名与环，正确性极易出错；且“删除边”的负向传播需要路径级见证。本实现选择在副本上做有界深度重算 + 集合差分，复杂度可预测、环天然安全、回滚天然原子；代价是最坏 `O(视图数 × 起点数 × 边工作量)`，对声明长度较短的本体视图可接受。
- **保留路径条数、但只按集合聚合**：条数为删除场景与调试提供信息，去重完全由 map 键保证，不会因到达路径数量影响最大值。
- **放弃惰性堆/删除式优先队列维护最大值**：属性变小时仍需处理堆中过期项，且难以给出“只考察真实可达终点”的严格上界。改为 `maxEnd` + 按需 `redetermine`，使开销上界可精确证明（`rescanCost == 可达终点数`）。
- **单把读写锁而非细粒度锁**：换取清晰的可串行化证明；锁内只做有界 DP，粒度优化留待有性能数据后再做。
- **环的默认态度是“正确支持”而非“禁止”**：需求要求含环正确聚合；`RejectCycle` 仅作为显式策略在运行时拒绝特定变更。

## 5. 本地验证方法

```bash
export PATH=$PATH:/usr/local/go/bin
go test -race -v ./...          # 含竞态检测的全量用例
go test -run TestDifferentialRandom -v ./...  # 与朴素全路径模型随机对拍（含日志）
go test -cover ./...
go vet ./... && gofmt -l .
```

覆盖点（`ontology/*_test.go`）：

- `TestSingleAndMultiHop`：单/多跳增删下的维护与“不存在”状态；
- `TestDuplicateArrivalCountsOnce`：重复到达只计一次；
- `TestCycles`：含环（自环、互环、环上终点）正确且有界；
- `TestAttrDecreaseRedetermine`：变小后重定源 + `rescanCost==4` 的开销上界 + 非来源变小零开销；
- `TestAffectedPrecision`：受影响集不多不少、不可达终点写入零影响；
- `TestErrorPriorities`：四类错误与固定优先级；
- `TestCycleRejectedAtRuntime` / `TestStaticCycleExclusion`：环拒绝/静态排除；
- `TestMaintenanceRollback`：维护失败后图与聚合整体回滚；
- `TestAddTypeToHop`：结构变更不破坏既有连通；
- `TestQueryIsPure`：查询不修改状态；
- `TestConcurrentSerialEquivalence`：并发增删链与属性写交织下，读者在同一读锁内与朴素重算逐起点比对；
- `TestDifferentialRandom`：40 个随机世界 × 300 步链接增删/属性写，与独立实现的朴素模型（`naive_test.go`，每次全量逐层枚举、零增量状态）对拍终点集合与最大值，并通过 `SetLogger` 打印每次输入、受影响起点集合与判定依据。
