# 服务端点分片维护器 — 设计说明

包路径：`ontology/endpointshard`。本文档记录模块划分、关键取舍、被放弃的方案与本地验证方法。

## 模块划分

| 文件 | 职责 |
| --- | --- |
| `endpointshard/types.go` | 公开数据模型：`Endpoint`（含派生条件 `Ready/Servable/IsTerminating`）、`SyncReport`、`QueryResult`、`ShardInfo` |
| `endpointshard/errors.go` | 错误类别 `ErrorKind`（优先级：参数非法 > 服务不存在 > 服务已存在）与构造器 |
| `endpointshard/treap.go` | `sizeIndex`：以 `(端点数, 分片编号)` 为键的确定性 treap，承载全部"按规模选分片"的查询 |
| `endpointshard/service.go` | 单服务状态机：同步、整理（删空分片 + 至多一次合并）、容量调整、消费者查询、代次与报告 |
| `endpointshard/manager.go` | 服务生命周期、参数校验顺序、每服务锁、操作计数器 `Stats` |

协作关系：`Manager` 只做校验、寻址与加锁；全部语义集中在 `serviceState`；`serviceState` 通过 `sizeIndex` 完成落位/整理/缩容中的选分片操作，通过 `ready`/`draining` 两个索引支撑消费者查询。

## 核心不变量

- 分片编号从 1 开始单调递增、永不复用（`nextNum` 只增不减，被拒绝的操作不触碰它）。
- 任意两次相邻的原子操作之间不存在空分片（每次同步/缩容结束时删空），因此空分片只可能由本次操作的移除产生，数量受本次变更集大小约束。
- 当前端点集合与内容恒等于最近一次成功同步的期望集（缩容只移动端点，不改内容），因此同步的差分可以直接在"当前映射 vs 新期望集"上计算，无需保存历史快照。
- 分片的修改代次只在它出现在变更报告（含新建、删除）时递增 1；与当前完全相同的同步产生空报告且不触碰任何代次。

## 关键取舍

### 1. 用确定性 treap 做"分片规模索引"

落位（端点数最多但未满、并列取小编号）、整理（删空、选最小的两个分片）、缩容（找所有超容分片）都是"按规模选分片"的查询。`sizeIndex` 以 `(size, num)` 全序为键，四种查询全部是 O(log S)（S 为分片数）：

- `maxNonFull(m)`：先查"小于 m 的最大 size"，再查该 size 下的最小 num，两段都是普通 BST 上界/下界查找。
- `min` / `twoMin`：左脊柱 / 截断中序遍历。
- `collectSizeGT(m)`：只递归 size > m 的子树，代价与超容分片数成正比。

优先级取 `splitmix64(num)`，是编号的纯函数，因此树的形状只取决于存活键集合，与调用顺序无关、无随机性——这保证了"精确可复现"，也让与朴素模型的逐步对照严格成立。

### 2. 同步的差分直接在当前状态上计算

不变量 3 使得同步无需保存上一份期望集。开销为 O(|desired| + Δ·log S)：读入期望集不可避免地为 O(|desired|)；扫描当前端点找移除项为 O(|current|) = O(|desired| + Δ)；其余工作（落位、索引维护、整理）只与变更端点数 Δ 和变更分片数有关。未受影响的端点除"作为输入被读入一次"外不产生任何开销，未受影响的分片完全不被访问。

### 3. 消费者查询只扫"可能出现在结果里"的端点

`ready`（健康且非终止）与 `draining`（健康且终止）两个索引互不相交且覆盖全部健康端点。查询只遍历其中一个集合，代价 O(R log R)（R 为结果大小，log 因子来自排序）；非健康端点、非就绪端点零开销。`Fallback` 定义为"就绪集为空、启用了回退规则"，即使回退集也为空——这样结果为空时仍能区分"没有就绪但走了回退"与（不存在的）"直接就绪为空"两种语义。

### 4. 每服务一把读写锁

`Manager` 的注册表一把 RWMutex，每个服务再一把 RWMutex（同步/缩容写锁，查询/Inspect 读锁）。同一服务上的同步与读取互不交错，不同服务完全并行；整体可线性化。被拒绝的操作在加锁前（参数校验）或加锁后未做任何变更前（存在性检查）返回，满足"不改变任何状态"。

### 5. 缩容的确定性顺序

缩容先把所有超容分片按编号升序逐出"标识字典序最大"的端点（每片逐出 `size - newM` 个），再把全部被逐出端点按标识字典序依次按新增规则落位，最后整理一次。逐出与落位在同一锁内完成，是同一原子调整，报告与代次规则与同步一致。选择"先全部逐出、再统一落位"而非"边逐出边落位"，是因为规格把多个被逐出端点的落位顺序定义为全局字典序。

## 被放弃的方案

- **按 size 分桶 + 桶内集合（`map[int]map[int]bool`）**：查询"最大未满 size"需要扫描空桶，最坏 O(M)；M 大时不可接受。放弃。
- **懒删除堆（min/max 两个 heap）**：堆顶过期条目需要惰性弹出，且"最大 size 取最小编号"与"并列取小编号选两个"在堆上表达别扭（需要弹出-恢复逻辑），正确性论证比 treap 复杂。放弃。
- **查询结果用有序结构（如按区域的有序集合）免去排序**：需要把每次状态位变更的索引维护做成 O(R) 的插入删除（有序切片）或再引入一棵平衡树，把成本转嫁给同步路径；为省查询端的 O(R log R) 排序不划算。放弃。
- **分片编号回收复用**：能省一点编号空间，但会破坏"编号单调、永不复用"的可推理性和报告的可解释性，且规格明确禁止。放弃。
- **同步时保存上一份期望集做差分**：与不变量 3 重复，浪费一倍内存。放弃。

## 性能的可验证证明

不依赖计时的断言（`Manager.Stats` 计数器，`perf_test.go`）：

- `TestSyncCostIndependentOfUnaffectedState`：100 个分片与 10000 个分片的服务上各做一次"单端点状态翻转"同步，`ShardsTouched` 恒为 1，`SizeIndexNodeVisits` 仅从 ~十 级增长到 ~百 级（对数），远小于线性。
- `TestSyncPlacementCostIndependentOfShardCount`：10000 个满分片下新增一个端点，只触及新建分片。
- `TestQueryCostProportionalToResult`：20000 个端点中仅 3 个就绪，`QueryEndpointsSeen` == 结果大小 3。

计时佐证（`bench_test.go`）：`BenchmarkSyncSmallDelta` 固定 20000 端点、分片数 100/1000/10000 三档，单端点变更同步耗时基本持平（残余成本是读入期望集本身的 O(|desired|)，三档相同）；`BenchmarkQuery` 在 100000 端点上做结果为 16 的查询为微秒级。

## 本地验证方法

```bash
export PATH=$PATH:/usr/local/go/bin

# 全量测试（含确定性单测、并发、随机对照、性能计数）
go test ./endpointshard/

# 竞态检测 + 打印每次操作的输入/实际输出/判定依据
go test -race -v ./endpointshard/

# 随机对照换种子复现（默认 1619）
ENDPOINTSHARD_SEED=42 go test ./endpointshard/ -run TestRandomizedDifferential -count=1

# 基准
go test ./endpointshard/ -run XXX -bench . -benchtime 200x

# 静态检查
go vet ./endpointshard/ && gofmt -l endpointshard/
```

测试与规格的对应关系：

| 规格要求 | 测试 |
| --- | --- |
| 已有端点不换分片、状态位/区域只更新内容 | `TestSyncStablePlacementAndContentUpdate`、`TestQueryReflectsStatusBitChanges` |
| 新增落位：最满未满、并列取小编号、无未满则新建、同次按字典序 | `TestSyncPlacementFullestNonFull`、`TestSyncPlacementTieBreaks` |
| 空分片删除、编号不复用 | `TestSyncEmptyShardDeletionAndNumberNoReuse` |
| 合并边界（之和恰等于 M） | `TestSyncMergeBoundary` |
| 每次最多一次合并 | `TestSyncAtMostOneMerge` |
| 变更报告精确集合与代次、相同同步空报告 | `TestSyncReportExactnessAndGenerations` |
| 回退条件、同区域优先、组内字典序 | `TestQueryReadyAndRegionPriority`、`TestQueryFallback` |
| 容量调整变大/变小/级联/非法 | `TestResizeGrowNoMovement`、`TestResizeShrinkEvictsAndReplaces`、`TestResizeShrinkCascade`、`TestResizeNoopAndInvalid` |
| 错误类别与优先级、被拒绝操作零副作用 | `TestErrorPriority`、`TestRejectedOpsLeaveNoState` |
| 并发线性化 | `TestConcurrentLinearizable`（完成顺序即合法串行序，逐条重放朴素模型比对报告与终态；每次查询结果必为某个完整同步状态的视图） |
| 随机序列逐步对照朴素模型 | `TestRandomizedDifferential`（3000 步，逐步比对报告、查询结果、错误类别与全量快照） |
| 性能可验证 | `TestSyncCostIndependentOfUnaffectedState`、`TestSyncPlacementCostIndependentOfShardCount`、`TestQueryCostProportionalToResult`、基准 |
