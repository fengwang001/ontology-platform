# 历史时刻一致快照遍历子系统 — 设计说明

## 1. 目标与一句话方案

给定逻辑时刻 `t`，遍历必须表现得如同在时刻 `t` 当场把整张
对象/链接图“冻住”，随后所有判定（链接是否存在、对象属性取值、
对象类型属性定义、链接基数版本）都只引用这一份不可变视图。

一句话方案：

> 所有写入只追加（append-only）、按全局提交顺序获得单调逻辑时刻；
> 每个键的历史是一条“版本时间线”，每对象的邻接关系是一棵
> **持久化（不可变）treap 的历史根序列**；遍历在开始时钉住
> （pin）时刻 `t`，之后只读这些永不原地修改的数据结构，因此
> 并发提交无法改变任何一步的判定。

代码位于 `temporal/`，可运行示例在 `cmd/demo/`。

## 2. 核心数据模型

- **逻辑时刻 `Instant`**：`int64`，从 1 起单调递增。每个成功 `Tx.Commit()`
  原子地获得一个时刻；快照在时刻 `t` 看到且仅看到提交 `1..t` 的效果。
- **对象类型时间线**：`TypeID -> timeline[[]Property]`。
  `MigrateObjectType` 追加一个新版本；时刻 `t` 的属性定义是
  “最后一个 `at <= t` 的版本”，即恰好覆盖 `t` 的那版，不做就近取整。
- **链接类型时间线**：`LinkTypeID -> timeline[Cardinality]`，
  同样按覆盖区间选版。
- **对象时间线**：`ObjectID -> timeline[objectRecord]`，记录类型、
  属性与存活位（创建/删除）。
- **链接（边）实例时间线**：`edgeKey{linkType,src,dst} -> timeline`，
  每次创建/撤销各追加一条记录。
- **邻接索引**：
  对每个对象维护出/入两个桶，桶内是 `[]adjRoot{at, root}`，
  `root` 是一棵持久 treap 的根。`treapKey = (linkType, other)`。
  时刻 `t` 的邻接根 = 桶中“最后一个 `at <= t` 的根”。

### 2.1 为什么用持久 treap

- 写入路径做一次 `insert/delete` 得到**新根**，旧根永远保留，
  因此长时间遍历持有的历史根在写入者提交新根后仍然可读。
- 成员判定 `treapContains` 为 `O(log k)`，中序遍历有序
  （按 `(linkType, other)`），保证遍历输出在不同运行与对照模型之间
  确定性一致。
- 优先级是键的纯函数（FNV-64a），结构可复现，没有随机数状态。
- 同一提交内同对象多条边会**折叠为该桶的唯一新根**（见
  `commit.go applyEdges`），避免同刻多根互相覆盖。

### 2.2 版本时间线如何无锁读

`timeline[V]` 的底层切片通过 `atomic.Pointer[[]entry[V]]` 发布；
写入者 copy-on-write 追加，读取者原子拿到一个切片头后做二分查找，
全程不需要拿全局锁。键 -> 时间线的 map 只插入不删除：指针查询用
`RWMutex.RLock`，一旦拿到 `*timeline`，其后的读取完全无锁。
所以“长遍历”不会阻塞“持续写入”。

## 3. 一次遍历的完整流程（`Store.Traverse`）

1. **钉快照** `s.Snapshot(t)`：
   - `t < horizon` → `ErrBeforeHorizon`（最高优先级）；
   - `t > head`（未来时刻）→ `ErrMissingHistory`。
2. **起始对象在 `t` 是否存在**：不存在/已删除 → `ErrStartNotFound`。
3. 锚定起始对象类型在 `t` 的属性定义版本（为结果解释属性）。
4. **BFS 展开**，全部走钉住的快照：
   - 每展开一个对象，取其 `t` 时刻邻接根，按有序键枚举；
   - 每条候选边再用**边实例自己的生命周期时间线**交叉确认
     （索引与时间线不一致视为历史损坏 → MissingHistory，绝不猜测）；
   - 是否继续、边是否存在、对端属性取值，都只以 `t` 判定；
   - 到达下一节点/下一深度会超预算时，**当场**判定
     `ErrLimitExceeded`（因此该错误优先于“更深层才会触达的缺失数据”）。
5. 对所有到达对象，按其在 `t` 的类型版本解释属性；需要的类型数据
   缺失 → `ErrMissingHistory`。
6. 返回完整结果；任何错误都返回 `nil` 结果，不返回半成品。

遍历期间系统继续接受写入、迁移、基数调整都没有影响，因为第 1 步后
所有输入都来自不可变版本。

## 4. 关键语义与取舍

### 4.1 属性迁移：解释锚定在 `t`

对象存的是“原始属性值”，属性的**解释/集合**由“该对象类型在 `t`
的定义版本”决定。迁移在 `t` 之后完成时，时刻 `t` 的遍历仍看到旧定义
（例如旧字段 `age` 保留、新字段 `score` 不出现），迁移是否已经完成
不会产生两种输出。测试：`schema_cardinality_test.go` 的
`TestPropertyMigrationBoundary`（迁移前一刻/后一刻边界、迁移后再迁移
仍重复得到相同结果）。

### 4.2 基数：按“当时真实存在的边”展开，不按新规则过滤

- 基数**只在提交创建边时**校验；收紧基数不会删除已存在的边。
  因此收紧后可能存在“现在不被允许、但历史上真实存在”的边，
  遍历必须忠实复现——这正是邻接根/边时间线记录的真实集合。
- 遍历读“恰好覆盖 `t`”的基数版本；长版本序列靠时间线二分精确定位，
  不退化为最近版本。测试 `TestCardinalityExactVersion` 用 10 次调整
  逐刻断言版本，并验证收紧后新建第 4 条边被提交期拒绝而历史 3 条边
  仍被完整展开。

### 4.3 错误分类、优先级与“零副作用”

四类互不相同的错误（`errors.go`）：

| 错误 | 触发 |
|---|---|
| `ErrBeforeHorizon` | `t` 早于可回放最早边界 |
| `ErrStartNotFound` | 起始对象在 `t` 不存在 |
| `ErrLimitExceeded` | 深度或访问规模超过预先声明上限 |
| `ErrMissingHistory` | 某一步依赖的底层历史缺失（`Gap`） |

同一次请求多条件共存时的确定性优先级：
`BeforeHorizon > StartNotFound > LimitExceeded > MissingHistory`。
实现要点：快照先钉（水位最先暴露）；起始存在性随后；预算在“决定
跨越那一跳”的瞬间判定（早于更深层缺失数据的读取）。

只读路径不修改任何时间线。写路径 `Commit` 在**任何修改前**先完成
`precheck + validate`，非法提交（含基数冲突）在追加历史之前被拒绝，
所以失败的遍历与被拒绝的提交都不会改变历史。测试
`TestNoPartialResultOnError`、`TestError*` 与失败后 `Head()` 不变断言。

### 4.4 可串行化

`Store` 维护一条**全局提交日志** `[]CommitEntry`，提交在
`commitMu` 下串行分配时刻并追加。生产实现与朴素对照模型
（`naive.go`）都以这份日志为唯一事实源。任一时刻 `t` 的快照
等价于“把所有提交按日志顺序串行执行后在 `t` 切一刀”。
多次并发遍历各自钉住不同/相同时刻，彼此不共享可变游标，互不干扰
（`concurrency_test.go` 在 `-race` 下验证）。

## 5. 单条边判定成本不随链接类型历史增长 — 可独立验证

需求要求：确认一条候选边在 `t` 是否存在的开销，不随该链接类型自创建
以来累计的创建/撤销总量线性增长。

实现上，一次 `Snapshot.LinkExists` 的代价是：

1. 取源对象邻接桶在 `t` 的根：对 `[]adjRoot` 二分，
   `O(log 该对象被触及的提交数)`；
2. treap 成员判定：`O(log 该对象在 t 的度数)`；
3. **该边实例自身**生命周期时间线二分：
   `O(log 这一条边的创建/撤销次数)`。

它从不枚举该链接类型的其它边，因此与“该链接类型的历史总量”无关。

**独立验证方式**：`Snapshot` 暴露底层探针计数 `Stats()`
（`TimelineProbes`、`AdjacencyProbes`）。
`costproof_test.go` 构造同一链接类型下 100 条 vs 4000 条
（40 倍）无关边 churn 的两个库，对**同一条**只创建过一次的探针边各做
一次 `LinkExists`，断言探针数恒定为
`{TimelineProbes:1, AdjacencyProbes:2}`；`TestProbeCountPerEnumeration`
对整源枚举给出相同常数。对照之下 `TestNaiveOracleCostGrowsForContrast`
展示朴素模型必须重放全部提交条目（线性增长），凸显该界限的含义。

## 6. 被放弃 / 未采用的方案

- **“读时持全局读锁、写时加写锁”（MVCC 之外的粗粒度锁）**：
  长遍历会阻塞持续写入，直接违背“遍历期间系统持续接受写入”。放弃，
  改为原子发布的不可变版本 + 持久索引（读路径无全局锁）。
- **在遍历中随读随检查“最新”定义/基数并据此修正决策**：
  会让遍历中途看到迁移/调基后的信息并改判，破坏单一基准。放弃，
  所有版本在快照内一次性锚定。
- **按当前基数“补算本应存在/不应存在”的边集合**：
  会错误过滤历史真实存在但现已不被允许的边。放弃，遍历只认真实
  边时间线与历史邻接根；基数仅约束新写入。
- **在邻接表上保存边的内联状态并就地更新**：
  原地更新会让历史快照被后续写污染。放弃，采用持久 treap 根序列。
- **按“最近一次”选择类型/基数版本**：
  长版本序列下会取错版本。放弃，统一使用时间线二分精确覆盖。
- **把缺失数据当作“不存在”静默处理**：
  无法区分真空缺与损坏。放弃，显式 `Gap` → `ErrMissingHistory`。

## 7. 本地验证方法

需要 Go 1.26+（仓库 `go.mod` 声明 `go 1.26.5`），纯标准库、无外部依赖。

```bash
# 如 go 不在 PATH：
export PATH=$PATH:/usr/local/go/bin
# 若 HOME 下构建缓存目录只读，可重定向缓存：
export GOCACHE=/tmp/gocache GOPATH=/tmp/gopath

# 全量测试（含随机差分）
go test ./...

# 竞态检测 + 详细输出（并发快照一致性）
go test -race -v ./...

# 覆盖率
go test -coverprofile=cov.out ./...
go tool cover -func=cov.out

# 静态检查 / 格式
go vet ./...
gofmt -l .

# 端到端示例：迁移与基数变更后重跑历史基线，输出不变
go run ./cmd/demo
```

### 测试矩阵与需求对应

| 需求点 | 测试 |
|---|---|
| 属性迁移前后边界、结果不随迁移完成与否分叉 | `TestPropertyMigrationBoundary` |
| 多轮基数调整精确定版、收紧不过滤历史边 | `TestCardinalityExactVersion` |
| 四类错误、优先级、无半成品、失败不改历史 | `TestErrorStartNotFound`、`TestErrorBeforeHorizon`、`TestErrorLimitExceeded`、`TestErrorMissingHistory`、`TestNoPartialResultOnError` |
| 长遍历与持续并发写入/迁移/调基交织一致性、可串行化 | `TestConcurrentWritesDuringTraversal`、`TestSnapshotSerializabilityAgainstNaive`（`-race`） |
| 随机操作序列与独立朴素模型逐条对照 | `TestDifferentialAgainstNaive`（40 个种子 × 60 步，多时刻/多起点） |
| 单边判定开销不随类型历史增长（可独立验证） | `TestLinkDecisionCostBoundedByEdgeHistory`、`TestProbeCountPerEnumeration`、`TestNaiveOracleCostGrowsForContrast` |
| 每次判定的输入/基准/版本/结论留痕 | `TestAuditRecordsDecisions`、`TestAuditBaselineStable` |
| 持久索引旧根不可变、确定可复现 | `TestPersistentTreapInsertDelete`、`TestPersistentTreapRootsIsolated` |
| 同提交多边合并、删除级联撤销 | `TestMultipleLinksSameCommit` |

## 8. 范围说明（有意的边界）

- 本实现为单机内存引擎，`Gap`/`Horizon` 以管理接口显式建模，
  用来真实触发并测试“历史缺失/超水位”两类错误；接入持久化存储时
  `timeline`/`adjRoot` 的发布点即天然的 WAL/快照边界。
- 属性值以 `any` 存储并用 `reflect.DeepEqual` 比较，聚焦时态一致性
  而非类型系统本身；属性“定义版本锚定”的语义与真实平台一致。
- 基数仅建模源端 `MaxOut`；扩展入端/多字段约束只需追加版本化字段，
  不影响快照/遍历机制。
