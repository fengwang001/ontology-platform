# 实例存储与跨类型聚合可见性子系统 — 设计说明

## 1. 目标与范围

在对象实例存储之上提供：

1. **乐观并发写入（OCC）**：写入/删除必须声明前序版本号 `Prev`；凭证不匹配则拒绝，
   且拒绝不占用版本号。
2. **跨类型聚合视图的增量维护**：视图定义在源对象类型之上（本实现支持单源类型、
   按字符串属性 `GROUP BY`、对数值属性 `SUM`；一个类型可挂多个视图），视图是源
   实例的**纯粹派生物**，不持久化成员列表快照。
3. **提交的原子可见性**：一次提交对其所属全部聚合视图（含分组迁移的旧组扣除/新组
   计入）在同一生效时刻整体可见。
4. **可区分的拒绝原因与严格的拒绝次序**，且拒绝不改变任何聚合取值。
5. **并发操作的全局串行等价性**与**确定性重放**。
6. **查询成本与类型下实例总数无关**，并以可执行、可断言的方式证明。

三个明确分工的模块：

| 模块 | 文件 | 职责 |
| --- | --- | --- |
| 实例版本仲裁 | `store.go` | 实例当前可见版本（含墓碑）的点查与落盘、schema/视图登记 |
| 聚合视图增量维护 | `aggregate.go` | `(视图, 分组键) -> {SUM, 成员数}` 的增量单元、原子 apply、全量 rebuild |
| 一致性校验与错误归一化 | `errors.go` + `kernel.go` 内校验次序 | 参数校验、凭证仲裁、四类规范错误 |
| 编排内核 | `kernel.go` | 唯一提交互斥锁、把三个模块组成一个事务边界 |

## 2. 关键数据结构

- `Record{Key, Version, Deleted, Attrs, CommitSN}`：实例的当前可见版本。
  - `Version` 从 1 开始单调递增；只有成功提交才 +1（拒绝不占用版本号）。
  - `Deleted=true` 是墓碑：删除也推进版本号，使「已删除后再次删除」可与
    「从未提交过」区分（后者根本没有 `Record`）。
  - `CommitSN` 是全局提交序号，即该版本的**生效时刻**（逻辑时钟）。
- `groupCell{sum, members}`：一个 `(视图, 分组键)` 的增量汇总。视图不保存成员
  列表，只保存派生出的标量；成员数仅用于空组回收、互斥归属断言与成本可读性。
- `CostMeter{InstanceRecordReads, GroupCellsTouched}`：操作触碰数据规模的
  计数，是查询成本性质的**可验证载体**（见 §7）。

## 3. 关键不变量

- **I1 视图纯派生**：任意时刻，对任意视图 V 与分组 g，
  `incremental(V,g) == recompute_from_live_records(V,g)`。
  `Kernel.RecomputeView` 用源实例全量快照重算，测试在每条随机操作后都断言该等式。
- **I2 单提交原子可见**：一次提交的「实例落盘 + 所有受影响视图 delta 生效」在
  同一临界区内完成；查询不可能看到其中一部分。
- **I3 迁移互斥**：分组键变更对同一视图产生「旧组 −x / 新组 +x」一个
  `delta`，在同一次 `apply` 内连续修改；不存在同时属于两组或两组都消失的状态。
- **I4 版本仲裁**：`Prev != 当前 Version` 即拒绝；版本号只在仲裁通过后 +1。
- **I5 拒绝次序**：参数非法 → 凭证不匹配 → 删除不存在/重复删除，只报第一个命中。
- **I6 删除守恒**：删除提交对所有视图产生与旧贡献相反的 delta，空组立即回收；
  从未提交过（无 Record）→ `not_found`；已墓碑再删（凭证正确）→ `already_deleted`。

## 4. 并发控制：为什么是一把全局提交锁

写入/删除路径持有**单一全局提交栅栏 `commitMu`（RWMutex）**：提交取写锁，
所有跨「实例存储 ↔ 聚合索引」的一致性观测（`Query`、`GetInstance`、
`RecomputeView`、`VerifyViewConsistency`）取读锁。提交在写锁临界区内完成：

1. 按主键点查当前版本（持锁，故「检查凭证」与「落盘」之间无窗口）；
2. 参数与凭证仲裁（参数校验在锁外提前完成，失败零副作用）；
3. 计算对所有视图的 delta；
4. `agg.apply(deltas)` 与 `store.put(rec)` 顺序执行；
5. `CommitSN++` 作为本次提交的唯一生效时刻。

聚合索引与实例存储各自另有内层 `RWMutex`，但在提交路径上它们总在 `commitMu`
写锁内被嵌套访问，因此整个提交是一个不可分割的线性化点。读路径持有 `commitMu`
读锁时，任何写提交都无法插入——这一点至关重要：若读路径不共享栅栏，
「`agg.apply` 之后、`store.put` 之前」的瞬间，用源实例重算就会与增量索引
互相矛盾（实现时确实用压力测试逼出并修复了该窗口）。`VerifyViewConsistency`
在同一个读临界区内完成「索引读取 + 源实例重算」比对，是对该性质的直接检验。

由此直接得到：

- 并发提交等价于按 `commitMu` 获取顺序的某个**全局串行序**；
- 多视图更新、分组迁移的旧/新组更新同处一个线性化点，**整体可见且无额外时延**；
- 同一操作序列重放到全新实例，得到相同的版本号、`CommitSN` 与全部聚合取值
  （确定性由「无时间/随机参与、共享零可变输入」保证，测试双次重放断言）。

### 被放弃的方案

- **每视图/每分组独立锁 + 两阶段提交**：能提高吞吐，但一次提交跨多个视图与旧/新
  分组时需要 2PC 才能保证原子可见，引入 prepare 窗口——查询者可能读到部分视图
  处于「提交前」、部分处于「提交后」，违背规格；且「判定整体可见以提交生效时刻为
  界、不得引入额外生效时延」排除了异步/延迟生效的方案。单机内存子系统上，一把
  提交锁以极小的实现复杂度换来了最强的一致性保证（strict serializability），故采用。
- **视图保存成员列表快照**：规格明确禁止；且快照与源实例之间需要额外同步，反而
  引入不一致面。本实现只保存可增量合并的标量（SUM 可逆，用加减维护）。
- **删除不推进版本号**：会让「正确凭证下的重复删除」无法与状态推进协调，也使墓碑
- **逻辑删除不保留版本**：无法区分两类删除错误。墓碑推进版本解决了该问题。
- **异步增量队列/CDC**：带来可见性时延，被「不得引入额外生效时延」否决。

### 取舍：复活（undelete）语义

对已删除实例，凭证正确的 `Write` 允许重新写入（清掉墓碑、版本继续 +1）。规格只
规定了删除侧的两类错误，复活语义使 OCC 版本链保持单一、自洽，且其聚合效果（重新
计入）与普通写入完全同构。若业务要求「删除即终态」，只需在仲裁后加一条
`old.Deleted -> ErrAlreadyDeleted` 的分支，不影响其余设计。

## 5. 校验与错误归一化

规范错误四类（`errors.go`），均可通过 `ErrorKind` 程序化区分：

1. `invalid_argument`：类型为空/未注册、主键为空、属性缺失、属性值类型不符、
   被视图用作分组键的字符串为空、视图/分组查询参数非法；
2. `version_conflict`：`Prev != 当前 Version`；
3. `not_found`：删除一个**从未成功提交过任何版本**的主键；
4. `already_deleted`：删除一个已处于墓碑状态的实例。

次序实现于 `Kernel.Write` / `Kernel.Delete`：先做全部参数校验（锁外），再在
提交锁内做凭证仲裁，最后才判定 not_found / already_deleted。典型例子：删除一个
不存在的键，`Prev=5` 报 `version_conflict`，`Prev=0` 才报 `not_found`。

`naive` 子包产生的错误同样归一化为 `ontology.Error`，因此差分测试按 `ErrorKind`
逐条对照，而不依赖错误文案。

## 6. 增量维护与分组迁移

`buildDeltas` 为实例所属的每个视图生成一个：

```
delta{ oldGroup, newGroup, oldVal, newVal, hadOld, hasNew }
```

- 首次插入：`hadOld=false, hasNew=true` → 只计入新组；
- 普通更新（分组键不变）：旧组 −oldVal、新组（同组）+newVal；
- 分组迁移：旧组 −oldVal、新组 +newVal，同一 `apply` 内完成；
- 删除：`hasNew=false` → 只扣除旧贡献，成员归零的单元立即 `delete`；
- 复活：等同首次插入。

`SUM` 是可逆聚合，因此不需要成员快照即可增量维护；成员计数使空组回收与互斥归属
校验成为 O(1)。

## 7. 查询成本：性质与可验证证明

查询 `Kernel.Query(view, group)` 只做一次 map 查找：

- **不读任何源实例记录**（`CostMeter.InstanceRecordReads == 0`），与该类型实例
  总数 N 无关；
- **只触碰一个分组单元**（`GroupCellsTouched == 1`）。

证明方式（均可执行、可复现）：

1. **计数器断言** `TestQueryCostIndependentOfTotalInstances`：N ∈
   {100, 1000, 5000} 下断言源实例读取恒为 0、触碰单元恒为 1；对照组朴素模型
   `Scanned` 恒等于 N。
2. **墙钟基准** `BenchmarkQueryCost`：本机结果

   ```
   incremental/N=1000    ~49 ns/op
   incremental/N=10000   ~68 ns/op   （N ×10，耗时基本不变）
   naivescan/N=1000      ~24.7 µs/op
   naivescan/N=10000     ~216 µs/op  （严格随 N 线性 ×~8.8）
   ```

规格中「只应与该分组当前成员规模相关」：取值查询本身是 O(1)；分组单元内维护了
`Members`，若上层需要枚举成员，则对成员主键做源实例点查（O(成员数) 次点查，
仍与组外实例无关）。本实现刻意不持久化成员列表快照（规格要求），因此成员枚举
走源实例，聚合取值走增量单元，两者口径都在 I1 下一致。

## 8. 本地验证方法

```bash
export PATH=/usr/local/go/bin:$PATH
export GOCACHE=/tmp/gocache        # 若默认缓存目录只读
go test -race -v ./...             # 全量测试（逐条打印输入/输出/判定依据）
go vet ./...
gofmt -l .
go test -run=^$ -bench=BenchmarkQueryCost -benchtime=2000x .
```

测试清单：

| 测试 | 覆盖的规格点 |
| --- | --- |
| `TestAtomicMultiViewVisibility` | 一次提交影响多个视图时的整体可见性；双视图同时迁移 |
| `TestGroupMigrationExclusivity` | 迁移的旧组扣除/新组计入互斥归属；删除即刻扣除无残留 |
| `TestDeleteErrorDistinction` | 从未提交删除（not_found）vs 已删除再删（already_deleted） |
| `TestOCCInterleaving` | 交错 OCC：唯一赢家、输家不占版本号、持新凭证重试成功 |
| `TestRejectionOrderingAndIsolation` | 三类拒绝次序；拒绝后全部聚合快照不变 |
| `TestRandomDifferential` | 400 条随机写/删/迁移逐条对照朴素模型；整序列双次重放完全一致 |
| `TestConcurrentEquivalentToSerialOrder` | 16 worker 并发后的增量/重算守恒（等价某串行序） |
| `TestGroupMigrationConcurrent` | 12 worker 并发迁移同一实例：最终恰好归属于一个分组 |
| `TestReaderWriterAtomicInvariant` | 8 写者迁移/删除/复活 × 4 读者在同一观测内断言 索引==重算（-count=10） |
| `TestQueryCostIndependentOfTotalInstances` | 查询源实例读取=0、触碰单元=1，与 N 无关 |
| `BenchmarkQueryCost` | 增量查询 vs 朴素全扫随 N 的墙钟对比 |

每条操作的打印格式：

```
[op 007] IN:  <操作输入>
         OUT: <实际输出（双侧：kernel 与 naive）>
         判定依据: <据以判定的不变量/期望>
```

## 9. 局限与扩展

- 聚合函数目前为可逆的 `SUM(int/double)`；`COUNT` 同构；`AVG` 可用
  `{sum,count}` 单元支持；`MIN/MAX` 不可逆，需在单元内维护成员贡献的有序结构
  （仍与组外实例无关），属于局部扩展。
- 视图目前为单源类型；跨类型 join 语义的视图需要定义多类型提交的原子边界，
  本设计的 `delta` 结构与提交锁可直接扩展为多源 delta 列表。
- 存储为内存实现；持久化时把「实例新版本 + 全部 delta」写为一条 commit record
  （WAL），恢复时顺序回放即可同时重建实例与聚合，I1/I2 天然保持。
