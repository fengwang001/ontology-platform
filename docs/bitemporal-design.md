# 双时态快照导出器 — 设计说明

代码位于 `bitemporal/`，无第三方依赖。本文说明模型语义、关键取舍、被放弃的
方案，以及本地验证方法。

## 1. 数据模型

- **有效时间轴（valid time）**：离散的 `int64` 域 `[MinTick, MaxTick)`，记录
  持有半开区间 `[Start, End)`：`Start` 被覆盖，`End` 不被覆盖。
- **事务时间轴（transaction time）**：单调只增的系统时钟 `Tick`；同一次
  `Write` 在其线性化点被盖上当前时钟值。
- **记录 `Record`**：`(ObjectID, TypeName, [Start,End), TxTime, Seq, Value)`。
  `Seq` 是全局只增的到达序号（1 起）。同一有效区间、不同 `TxTime` 的多条记录
  表示对历史的更正。
- **Schema**：对象类型在事务时间 `DefinedAt` 起被定义；未定义前导出该类型必须
  拒绝。

## 2. 导出语义

对一次导出，先在**一个临界区内**冻结绑定 `Cutoff{T, Seq}`：

- `T`：请求的事务时间点；
- `Seq`：冻结那一刻已提交写入的最大全局到达序号。

对每个对象、每个有效时间点 `v`，可见记录是满足

```
r.Seq <= Cutoff.Seq  AND  r.TxTime <= Cutoff.T  AND  r.Start <= v < r.End
```

的记录中 `TxTime` 最大者；`TxTime` 相同取 `Seq` 更大（更晚到达）者。不存在则该
点为**未知（unknown）**，导出中显式输出 `Value == null` 的时间段，绝不以默认值
填充。

### 2.1 “事务时间恰等于 T”的归属

题目要求：`TxTime == T` 是否纳入取决于其到达与 `T` 绑定的关系，且该口径对整份
导出一致。系统在同一事务时间刻度内允许多次写入（同 tick），仅靠 `TxTime` 无法
区分先后。因此绑定同时包含到达序号 `Seq`：

- 在冻结点**之前或当时已提交**、且 `TxTime <= T` 的记录纳入；
- 冻结之后才到达的记录（即使 `TxTime == T`）一律不纳入。

`(T, Seq)` 在同一个 RWMutex 临界区读出，构成一个全局线性化点。整份导出（无论
分多少批、批次何时调度）都复用同一个 `Cutoff`，所以不同对象不可能出现口径分裂。
这也是“并发持续写入不污染已冻结结果”和“整份结果等价一次性读取”的实现机制。

### 2.2 更正折叠与审计视图分离

- 导出视图只暴露可见记录，被更正的旧记录不出现于快照；
- 审计视图 `AuditView` 独立地保留全部到达记录（`History`、`RecordsAt`、
  `VersionsOver`），包括后来被更正掉的版本。两者是同一数据上的两个独立视图。

## 3. 拒绝优先级与原子性

`Prepare` 在产生任何部分结果**之前**按固定优先级校验：

1. `CodeRetention`：`T < 系统保留最早可查事务时间`；
2. `CodeSchemaUndefined`：对象类型在 `T` 时尚未定义（`DefinedAt > T`）；
3. `CodeInvalidRange`：导出范围不是非空半开区间、或越出时间域。

导出过程中扫描到记录损坏（完整性哈希不匹配、测试钩子标记的不一致）返回
`CodeInconsistency`，批次导出立即整体失败，**不返回残缺快照**。错误类型
`ExportError{Code, Msg}` 可被调用方稳定区分。被拒绝的请求不修改任何记录、不
占用保留资源（冻结只是一次只读快照）。

## 4. 规模无关的点查性能（可验证）

朴素实现要回答“某对象在某有效点、截至某事务时间的可见记录”，必须扫描该对象
跨全部有效/事务时间的所有记录，随历史线性增长。本实现使用**有效时间二分规范
覆盖段树（canonical dyadic segment tree）**：

- 把 `int64` 有效域顺序保序地映射到 `uint64`（`u = uint64(t) + 2^63`），所有
  区间运算在无符号域进行，规避有符号溢出；
- 每个插入区间被切成至多 `2 * 64` 个**对齐的二的幂块（dyadic node）**，每块
  维护一条按 `Seq` 追加的链；
- 点查询只走根到叶的 **64 条链**；由于系统时钟单调，链内 `TxTime` 随 `Seq`
  非降，因此每条链上用两次二分分别定位“已到达前缀”和“`TxTime <= T` 的末位”，
  取到该链候选。

单次点查的记录比较次数上界为

```
O(log D · log N)，  D = 2^64（链数恒为 64），N = 该对象历史记录数
```

**它不随 N 线性增长**。这一点由测试 `TestPointLookupScaleIndependent` 直接测量并
断言：对象历史从 200 增至 1400（7 倍），单点评分比较次数几乎不变（实测 2→4，
远低于线性增长应有的约 7 倍），并对照固定理论上限。导出整对象时间轴使用扫描线
（与该对象在窗口内的记录数线性相关），但题目要求的“单个对象、某个有效时间点”
的规模无关性由上述索引保证，且计数在代码中由 `visibleSeq` 实际返回并打印。

## 5. 快照构造与确定性

- `buildSegments` 对窗口内记录生成开始/结束事件，事件同点时“结束先于开始”，
  正确处理左闭右开边界；用按“`TxTime` 大、同 `TxTime` 时 `Seq` 大”排序的堆
  维护当前可见集合，扫描出常量值运行段；
- 无覆盖的前缀、后缀、空洞都输出为显式 unknown 段；相邻等价段合并；
- 序列化为 `json.Marshal(Snapshot)`：字段经 `CanonicalBytes` 按 key 排序，
  批次对象 ID 排序。因此同一对象在两个不同导出请求（不同 `T` 之间无新增记录）
  的结果**字节级一致**，由 `TestDeterministicReexport` 逐字节断言。

## 6. 并发与可串行化

- 存储用一把 `sync.RWMutex` 线性化所有 `Write`、时钟推进与冻结读；段树索引自身
  再带一把 `sync.RWMutex`，使点查与持续写入并发安全；
- 每次导出的可观察结果等价于在其冻结点上的一次串行读；多个导出（不同 `T`）与
  写入的交错因此等价于某个全局串行顺序。
- `TestConcurrentExportsSerializability` 在持续写入下并发冻结并观测，结束后把
  每次捕获的 `(T, Seq)` 对最终日志做离线重放，逐一核对当时可见记录——这正是
  “存在全局串行顺序”的检验方式。

## 7. 被放弃 / 未采用的方案

- **仅按 `TxTime` 过滤、不绑定 `Seq`**：无法定义同 tick 内“冻结后到达”的记录
  归属，会让同一 `T` 的不同对象口径不一致。改为 `(T, Seq)` 双绑定。

- **区间树 / R 树做点查**：平均虽快，但最坏情况可能与重叠区间数线性相关，无法
  给出题目要求的、可验证的最坏规模无关界。改用规范覆盖段树，点查链数恒为 64。

- **每链只看最新一条记录**：当最新记录 `TxTime > T` 时会错误地丢掉链上更旧但
  合格的记录。改为“利用链内 `TxTime` 非降做二分”，既正确又保持对数代价。

- **直接在有符号 `int64` 上做区间分块**：中点/对齐运算在 `MinInt64` 附近溢出。
  改为保序映射到完整无符号域；并显式回避 Go 中 `1<<64 == 1`（不截断）这一坑，
  使用 `bits.TrailingZeros64/LeadingZeros64`。

- **全时间轴上逐 tick 物化 unknown**：域跨度 2^64，不可行。改为扫描线只在事件
  边界产生运行段，unknown 以运行段而非逐点表示。

- **导出失败时返回已算出的部分快照**：违反“不得返回残缺快照”。批次内任一对象
  出现不一致即整体返回错误。

## 8. 本地验证

需 Go 1.26（仓库 `go.mod` 要求 1.26.5）。若 `go` 不在 PATH，使用
`export PATH=$PATH:/usr/local/go/bin`；沙箱里建议 `export GOCACHE=/tmp/gocache`。

```bash
# 全量测试（含竞态检测，多跑以暴露交错问题）
go test -race -count=3 ./...

# 详细输出（决策日志写入测试注入的 buffer）
go test -race -v ./...

# 覆盖率
go test -coverprofile=coverage.out ./...
go tool cover -func=coverage.out

# 静态检查与格式
go vet ./...
gofmt -l .
```

### 测试覆盖面（对应题目要求）

- `TestHalfOpenBoundary`：左闭右开边界点（9/10/19/20）与 unknown 显式输出；
- `TestCorrectionCollapse`：更正与原记录在不同 `T` 的取舍，审计视图保留两者；
- `TestTransactionBoundT`：`TxTime == T` 时按冻结前后到达决定归属，口径一致；
- `TestBatchingOneCutoff` / `TestConcurrentWritesDoNotPolluteFreeze`：分批共享同一
  冻结点，导出期间并发写入不污染；
- `TestDeterministicReexport`：不同请求间无新增记录时字节级一致；
- `TestRejectionPriority`：保留范围 > schema 未定义 > 范围非法的优先级，无部分
  结果；
- `TestInconsistencyDetected`：记录不一致可检测、可分类、整体失败；
- `TestCanonicalCover`：含 int64 极端点的规范覆盖正确（不重叠/不遗漏/对齐）；
- `TestPointLookupScaleIndependent`：规模无关点查的可验证上界；
- `TestConcurrentExportsSerializability`：持续写入 + 多导出并发的离线串行重放；
- `TestDifferentialAgainstNaive`：与独立朴素双时态模型在 40 个随机种子、随机操作
  序列、多个 `T`、密集有效点网格与时间轴分段上全面对照；
- `TestDecisionLogContainsInputs`：判定输入/输出/依据通过注入的 `DecisionLogger`
  打印（生产可注入任意 `io.Writer`）。
