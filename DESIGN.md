# 属性索引重建的审计溯源证明器 — 设计说明

代码位于 `ontology/` 包，端到端演示在 `cmd/indexaudit/`。

## 1. 要解决的问题

对象的某些属性建有“按值查找”的二级索引。索引损坏后必须重建，但“重建完成”
本身只是一句声明，不能作为正确性依据。复核者需要在**不信任重建执行日志**
的前提下，仅凭一份审计记录与对象当前状态，独立判定索引的每个条目是否正确；
重建期间写入仍在持续，必须精确划分基线范围与增量范围；重建中途失败时，
半成品条目绝不允许对外提供查询。

## 2. 关键取舍

### 2.1 用全局单调 LSN 而非墙钟时间做基准点

每次写入在 `Store` 的单一互斥锁上取得一个严格递增的整数 `LSN`
（`store.go: putLocked`）。重建发起时在同一个串行点读取
`fence = Store.nextLSN`（已分配的最大 LSN），于是：

- `LSN <= fence` 的写入全部属于**基线范围**；
- `LSN > fence` 的写入全部属于**增量范围**。

围栏与 LSN 分配共用一把锁，任何写入的 LSN 都不可能“落在边界上”：
不存在两写同 LSN、也不存在 LSN 空洞，因此基线与增量**不重叠、不遗漏**。
墙钟时间会受 NTP 回拨、调度抖动影响，无法给出这种全序，故放弃。

### 2.2 单全局串行域：写入、状态迁移、复核采集共用一把锁

`IndexManager.Write` 在同一个临界区内完成“对象状态落实 + 存活审计条目追加”，
二者共享同一 LSN。重建围栏、基线扫描、增量封存、以及复核的世界快照采集
（`verify.go: CaptureWorldLocked`）也都在这把锁内排队。

直接推论：

- 增量应用顺序与写入对对象生效的顺序**天然一致**，不可能颠倒
  （不需要额外队列或时间戳排序）；
- 并发复核不会遭遇“审计是新的、对象是旧的”这类 TOCTOU 窗口——
  审计与对象视图要么来自同一串行点，要么来自之后某个同样自洽的串行点；
- 整套行为等价于某个全局串行顺序，线性一致性由单锁直接保证。

放弃的方案：多版本并发控制 + 后台追赶队列。它能提升并发度，但要额外证明
队列应用顺序、解决围栏竞态，复杂度与本问题“可独立复核”的核心无关。
单锁模型在验证层面最容易被复核者信任，性能取舍见 §2.6。

### 2.3 审计记录是“可复核的证据”，不是条目计数

`AuditRecord`（`audit.go`）包含：

- 范围标识：`BaselineStartLSN / BaselineEndLSN(=fence)`；
- 完成时点：`CompletionLSN`（增量全部应用、索引封存的时点；同样是 LSN，
  不是墙钟）；
- **逐条**对应关系 `EntryProof{值, 对象ID, 属性, 对象版本, 来源LSN, 范围}`，
  范围字段标明该条目来自基线还是增量；
- 对“范围元数据 + 全部条目”的 SHA-256 摘要，用于检测审计落盘/传输中的篡改。

摘要是**自证而非信任锚**：复核器会从内容重算摘要，并进一步把每条值与对象
当前状态对照。即使摘要被重算伪造，值层面的不一致仍会被抓到。

封存之后的写入不会让审计过期：`Write` 以 `RangeDelta` 语义在同一点覆盖
存活审计中该对象的条目，并重算 `CompletionLSN` 与摘要。这样“审计 + 当前
对象状态”在任何时点都互为充分统计量。

放弃的方案：审计只保存条目总数。总数相同可以掩盖张冠李戴、脏值、悬空对象，
无法支持条目级定位，被明确否决。

### 2.4 状态机与失败原子性

`absent -> rebuilding -> active`，重建失败进入 `failed`；`failed` 可通过
再次成功重建回到 `active`（`index.go`）。

- `rebuilding/failed` 下查询一律以 `ErrIndexUnavailable` 拒绝；
  `absent`（从未建过）与“未声明”是另外两类，错误值不同，
  调用方可用 `errors.Is` 严格区分。
- 重建工作条目只存在于 `indexState.workEntries`，**只有**在完整审计
  `Seal()` 成功后才原子替换为 `st.audit` 并迁移到 `active`。
  失败路径 `abortLocked` 丢弃整个工作集，因此半成品永不可见。

查询拒绝优先级在 `Query` 中按固定顺序实现：
未声明（`ErrIndexNotDeclared`） > 无完整审计不可用（`ErrIndexUnavailable`）
 > active 但带未修复不一致（正常返回结果，仅在 `QueryResult` 上声明）。

### 2.5 独立复核：双向、逐条、确定性

复核器（`verify.go`）的输入只有审计记录与对象状态，**不读取重建执行日志**。
它做三件事：

1. 摘要重算比对；
2. 正向逐条比对：审计值 vs 对象当前属性值，并核验 `SourceLSN` 确为
   `CompletionLSN` 时点该属性的生效版本；识别重复条目与悬空条目；
3. 反向枚举：对象当前有值但审计缺条目（`entry_missing`）。

不一致类别可区分：`entry_value_mismatch`、`entry_object_missing`、
`entry_duplicate`、`entry_missing`，每条都带索引名、键、对象 ID、
期望值与实际值——只报“整体不一致”的情况不存在。

`checkWorld` 是对不可变 `WorldSnapshot` 的纯函数，因此同一输入多次复核
结论逐字节相同（幂等）。只读复核（`VerifyAudit`）不修改任何状态；
是否据此标记索引由调用方显式选择（`VerifyManager(..., flagOnMismatch)`），
把“观察”与“处置”分离。

### 2.6 与规模无关的单条目复核（O(1) 取证）

审计条目的 `SourceLSN` 直接指认来源写入。`Store.valueAtLocked` 在按 LSN
排序的属性历史上，用审计给出的 `SourceLSN` 定位后，**只读 1 条来源记录 +
至多 1 条邻接边界记录**即可证明它是封存时点的生效版本，不回溯整条历史。

`Store.historyInspections` 是一个白盒计数器：测试
`TestSingleEntryVerifyConstantHistoryReads` 让对象历史从 51 增长到 4051，
断言 `VerifyEntry` 的历史读取数恒定为 1（上界 2），把“不线性增长”
从口头主张变成可执行、可观测的证据。

## 3. 被放弃的方案汇总

- 墙钟/版本向量做基准点：全序不严格、有回拨风险。
- 多版本 + 异步增量队列：并发好但顺序正确性证明复杂，与核心目标无关。
- 审计只存计数或哈希根：无法逐条定位，且哈希根无法证明“值对得上对象”。
- 复核信任重建日志：违背“仅凭审计与当前状态”的前提，日志可被伪造。
- 失败后保留旧索引继续服务：本题要求失败重建整体视为未完成；旧索引已被
  本次重建意图取代，继续提供会让“正在重建中不可用”的状态语义失真。

## 4. 错误类别

| 类别 | 触发场景 | 查询后果 |
| --- | --- | --- |
| `ErrIndexNotDeclared` | 类型未声明该索引 | 拒绝（最高优先级） |
| `ErrIndexUnavailable` | rebuilding / failed / absent | 拒绝；与未声明可区分 |
| `ErrIndexInconsistent` | active 但复核曾标脏 | 不拒绝，结果中带声明 |
| `Mismatch`（四种 Kind） | 复核发现的条目级问题 | 出现在复核报告，逐条定位 |

## 5. 测试覆盖面

`ontology/*_test.go`：

- `TestBoundaryMembershipExact`：边界恰在某次写入前后的归属（LSN2 归基线、
  LSN3 归增量），并用时间点快照独立佐证，再断言无对象遗漏/重叠。
- `TestFailedRebuildUnavailable` / `TestAbsentVsUndeclared`：中途失败后
  半成品不可查询、状态为 failed、与“未声明/不存在”严格区分、可恢复。
- `TestIndependentVerifyDetectsTamper`：人为篡改条目后，不看重建日志也能
  定位到具体对象/键/期望值；查询带不一致声明但仍返回。
- `TestVerifyDetectsMissingEntry`：反向缺失检测。
- `TestConcurrentVerifiersConsistent`：并发写入 + 并发复核，所有复核都抓到
  同一条注入不一致；修复后停写，多复核结论完全一致；`-race` 下运行。
- `TestDifferentialRandomInterleaving`：40 个种子 × 120 步随机写入/重建
  交错，与独立朴素模型（map 重建）在查询结果、条目内容、复核结论三方对照。
- `TestDifferentialRandomFailures`：25 个种子随机注入两类失败，断言失败后
  绝不返回半成品、恢复后立即再次等价于朴素模型。
- `TestSingleEntryVerifyConstantHistoryReads`：O(1) 历史读取取证。
- `TestVerifyIdempotentAndReadOnly` / `TestRejectedRequestsHaveNoEffect`：
  只读幂等、拒绝请求不改状态。

每次判定的输入/输出/依据均通过 `DecisionLog` 以 JSON 行打印；
测试用缓冲捕获并在失败时转储，demo 默认打到 stderr。

## 6. 本地验证方法

```bash
# 注意：容器内 Go 位于 /usr/local/go/bin，如不在 PATH 请先加入
export PATH=$PATH:/usr/local/go/bin
export GOCACHE=/tmp/gocache GOPATH=/tmp/gopath   # 沙箱缓存目录可写时

go test -race -v ./...                     # 全量 + 竞态检测
go test -coverprofile=cov.out ./ontology/  # 覆盖率
go tool cover -func=cov.out | tail -1      # 当前约 81%
go vet ./...
gofmt -l .
go run ./cmd/indexaudit                    # 端到端演示（人类结论 stdout，判定日志 stderr）
```

## 7. 已知边界与后续演进

- 单锁串行域是为“可证明”选择的最简单模型；超大规模写入压力下可演进为
  分片 LSN + 每类型锁，正确性论证不变（围栏仍取在同一分片的串行点）。
- 值以规范化字符串承载；实际系统应在写入边界把标量/时间/枚举规范化，
  避免同义不同形。
- 删除对象目前以“无当前值”表达并由 `entry_object_missing` 捕获；
  若引入墓碑语义，应在 `EntryProof` 增加删除来源记录。
