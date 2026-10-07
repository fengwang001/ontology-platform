# 聚合视图子系统设计说明

## 1. 问题模型

一个聚合视图 `ViewDef` 声明：

- 分组对象类型 `GroupType`（如 `Dept`）；
- 一到多个被聚合来源 `Source`，每个来源给出对象类型（如 `Emp`）、
  归属链接类型（如 `belong`，方向为 `被聚合实例 -> 分组实例`）、
  被求和的数值属性类型，以及**多分组贡献策略 `Policy`**；
- 聚合结果 = 对当前真实归属某分组、且属性处于存在状态的全部实例，
  按策略计算贡献并求和，同时给出参与实例计数。

多分组贡献必须显式声明，系统不提供“默认只算一个”：

- `PolicyFull`：实例归属 `k` 个分组时，对每个分组贡献完整值 `v`（合计 `k*v`）；
- `PolicyEvenShare`：实例归属 `k` 个分组时，对每个分组贡献 `v/k`（合计 `v`）。

数值使用 `math/big.Rat` 精确表示，`v/k` 与份额重摊不产生浮点误差。

代码位置：`ontology/aggview/`。

## 2. 组件与数据结构

- `Store`（`store.go`）：最小本体平台。实例持有类型、属性表、
  出链接集合；另维护按 `链接类型 × 目标` 的反向索引，
  使“分组删除时找到全部归属成员”无需全库扫描。
- `Engine`（`engine.go`、`engine_ops.go`、`engine_ops2.go`）：
  持有全部已注册视图、一把全局互斥锁、失败钩子与审计日志出口。
  每个分组的聚合按来源对象类型分桶保存（`view.agg[group][objType]`），
  多来源互不串扰；`Query` 合并各桶。
- `txn` / `mutator`（`txn.go`）：处理单元与 undo 日志。
  Store 的属性写入、链接增删、实例删除，以及引擎侧的聚合桶修改、
  归属版本递增、桶删除，全部注册逆操作；失败时按逆序回放。
- 归属版本 `view.versions[member][objType]`：每次链接集合改变单调加一，
  是 `MoveToGroup` 乐观并发控制的版本令牌。

## 3. 增量维护规则

设实例当前归属集合为 `G`，`k = |G|`，旧值/新值为 `vold/vnew`，
单组贡献函数为 `c(v,k)`（Full: `v`；EvenShare: `v/k`）。

| 操作 | 聚合更新（同一处理单元） |
| --- | --- |
| 属性写入 `vold -> vnew` | 对每个 `g ∈ G` 应用 `c(vnew,k)-c(vold,k)`；计数随存在状态增减。不触碰其他分组。 |
| 新增归属链接到 `gnew`（新 `k`=旧`k`+1） | EvenShare：对旧归属各组应用 `c(v,k)-c(v,k-1)`；再对 `gnew` 加 `c(v,k)` 并计数。Full：仅对 `gnew` 加 `v`。 |
| 删除归属链接 `gold`（新 `k`=旧`k`-1） | 先按旧 `k` 的份额从 `gold` 扣除并减计数；EvenShare 再对剩余分组应用 `c(v,k)-c(v,k+1)`。扣减与重摊同单元。 |
| `MoveToGroup`（CAS） | 校验版本后：旧分组按旧 `k` 份额扣除、新分组按新份额加入、替换链接集合，全部在同一处理单元；版本不符则整体拒绝。 |
| 被聚合实例删除 | 按其**最后已知属性值**对全部当前归属分组扣除最后贡献；清理版本。 |
| 分组实例删除 | 经反向索引取全部归属成员：逐人从该分组扣除最后份额，EvenShare 对该成员的其他归属做重摊，删除归属链接（成员实例与其他归属不受影响），移除该分组聚合桶。 |

“不存在”（`Value{Present:false}`）在任何策略下贡献都是零，且不计数；
首次写入具体值、清除回不存在，分别体现为计数与和的增、减。
取值恰好为零与不存在的区别仅体现在计数上。

## 4. 原子性（处理单元与回滚）

每次 `SetProperty / AddLink / RemoveLink / MoveToGroup / DeleteObject`
都在 `Engine.txn` 内执行：

1. 取全局锁；
2. 业务校验（错误在修改任何状态前返回）；
3. 通过 `mutator` 写 Store 与聚合桶，每步注册 undo；
4. 可注入的提交钩子（`SetFailHook`）模拟“处理单元内更新失败”；
5. 任一步失败 → 逆序执行全部 undo，返回 `KindUpdateFailed`，外部观察不到中间状态；
6. 成功 → 输出审计事件（输入、判定依据、每个受影响分组的增量与计数变化）。

因此“原分组只扣未加”“新分组只加未扣”“属性已改但聚合未动”这类
中间状态在失败路径下也不可能对外可见（见 `TestMoveToGroupRollback`）。

## 5. 并发控制与串行等价

- 全部写操作串行经过同一把互斥锁。两个操作即便同时到达，
  引擎内部也存在确定的全序，天然满足“等价于某个串行顺序”。
  属性写入与归属迁移无论谁先进入临界区，最终都是
  “新值只计入最终归属、旧值只留在最终非归属”的合法串行结果
  （`TestConcurrentPropertyAndMove` 验证 200 轮）。
- 归属改变额外要求调用方先读 `MembershipVersion`，
  再以 `MoveToGroup(..., expectedVersion)` 提交。链接集合一变版本即加一；
  两个并发迁移只有一个版本匹配并生效，另一个得到 `KindConflict`，
  在触碰任何状态前返回（`TestConcurrentMoveConflict` 验证恰好一成一拒、总量守恒）。
- 不同实例的独立写操作在锁下逐一线性化，随机交织后与朴素重算逐分组一致
  （`TestConcurrentIndependentOps`）。

## 6. 常数份更新证明

结论：一次单链接归属改变触及的聚合条目份数恒为常数，与分组内实例总数无关。

结构论证（可直接对照代码）：

- `AddLink` 的 `apply` 只发生在新分组（1 份）；`reshare` 的遍历对象是
  **该成员自己**的其他当前归属，数量上界为该成员的链接数，与目标分组规模无关。
- `RemoveLink` 对称：只对原分组扣 1 份，重摊只遍历该成员的剩余归属。
- `SetProperty` 只遍历该成员自己的当前归属分组。
- `MoveToGroup` 的 `apply` 只出现在旧分组集合与新分组两处，
  单分组归属语义下被去重计数为恰好 2 份（`Change.TouchedGroups()`）。
- 任何代码路径都不读取目标分组的成员列表；唯一遍历成员列表的入口
  `LinksTo` 只出现在分组删除（属于级联全量解除，本就需要触及每个成员）
  与朴素重算模型中。

经验佐证：`TestConstantTouchesIndependentOfGroupSize` 令分组规模取
1/10/100/500，`MoveToGroup` 的 `TouchedGroups()` 恒为 2。

## 7. 正确性对拍

`diff_test.go` 内的 `naiveModel` 是一份独立的朴素实现：
不维护任何增量状态，每次校验都重新扫描 `LinksTo(linkType, group)`，
按当前属性存在性与当前归属数重算和与计数。

- `TestRandomDifferentialFullPolicy`：20 个随机世界 × 400 步，
  混合属性写入（不存在/零/正负值）、链接增删、分组与成员删除（删除后重建），
  每步后逐分组交叉验证；
- `TestRandomDifferentialEvenShare`：600 步分数运算交织，验证 `big.Rat` 精确性；
- 所有变更通过 `memLogger` 打印输入、受影响分组、判定依据（`go test -v` 可见）。

## 8. 错误分类与固定优先级

| Kind | 含义 |
| --- | --- |
| `KindGroupNotFound` | 分组实例不存在（或类型不是视图的分组类型） |
| `KindTypeNotDeclared` | 被聚合对象类型未声明参与该视图 |
| `KindConflict` | 归属改变版本冲突，操作被拒绝且无副作用 |
| `KindUpdateFailed` | 处理单元内更新失败，整体回滚 |

多个条件同时成立时按上表从高到低报告；`MoveToGroup` 内的校验顺序
即“分组 → 类型 → 版本”，事务失败在所有校验通过之后才可能发生
（`TestErrorPriority` 固定该顺序）。

## 9. 关键取舍与被放弃的方案

1. **单把全局互斥锁 vs 行级锁/MVCC**：选单锁。需求强调任意时刻查询与
   底层数据完全对应、失败整体回滚、可串行化；单锁使这些性质可直接论证，
   代码量小且无死锁面。放弃 MVCC/2PL 是因为其版本回收、读写冲突判定与
   聚合结构的多键原子更新会显著放大复杂度。吞吐瓶颈出现时可沿
   “按视图/分组分片加锁 + 成员归属路由表”演进，增量算法本身不变。
2. **同步同单元更新 vs 异步事件队列**：选同步。异步队列天然存在
   “链接已变、聚合未更新”的窗口，无法满足任意时刻查询一致与
   “扣减/增加同一处理单元”；事件日志仅用于审计，不参与状态传播。
3. **强制显式策略 vs 默认去重**：选强制。重复链接多归属时默认只算一个
   正是需求明确禁止的静默行为；缺少 `Policy` 的视图声明直接注册失败。
4. **EvenShare 当场重摊 vs 惰性重算**：选当场。归属数变化时立即对
   该成员的其他归属做 `c(v,k±1)` 差额调整，任何时刻 `Query` 都精确；
   惰性重算会引入读时收敛与并发计数问题。
5. **`big.Rat` vs float64**：选 `big.Rat`。`v/k` 链式调整在 float64 下
   会积累误差，无法与朴素重算逐位相等。
6. **视图先注册后写数据 vs 注册时回溯存量**：选前者。回溯需要定义
   “注册时刻”与并发写入之间的一致性快照，属未被要求的复杂能力；
   `RegisterView` 在全局锁下挂载视图，之后的所有变更统一走增量路径。
7. **分组删除解除链接并保留成员 vs 连带删除成员**：选解除。
   被聚合实例是独立对象，分组消失只意味着“不计入任何分组”。

## 10. 本地验证方法

```bash
export GOCACHE=/tmp/gocache   # 若默认缓存目录只读
go test ./...
go test -race -count=1 ./ontology/aggview/
go test -v -run 'TestRandomDifferential|TestMultiGroup|TestDelete|TestConcurrent' ./ontology/aggview/
go vet ./...
gofmt -l .
```

测试覆盖与需求条目的对应：

- 多分组贡献：`TestMultiGroupFullPolicy`、`TestMultiGroupEvenSharePolicy`；
- 属性/归属并发交织串行等价：`TestConcurrentPropertyAndMove`；
- 不存在与零：`TestAbsentVsZero`；
- 两类删除级联：`TestDeleteCascade`；
- 常数份更新：`TestConstantTouchesIndependentOfGroupSize`、`TestConstantTouchesEvenShare`；
- 并发归属冲突拒绝：`TestConcurrentMoveConflict`；
- 处理单元失败回滚：`TestMoveToGroupRollback`、`TestErrorPriority`；
- 随机对拍：`TestRandomDifferential*`；
- 错误优先级：`TestErrorPriority`。

## 11. 已知边界

- 单机内存实现，无持久化；持久化介质只需保证处理单元的 undo/redo 语义。
- 注册视图要求先于数据写入（见取舍 6）。
- 同一来源类型的实例可多次链接到同一分组时，`Store` 将链接视为集合
  （重复链接不产生重复贡献）；多份贡献请使用多条不同链接类型并分别声明来源。
