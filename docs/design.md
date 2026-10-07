# 增量索引子系统设计说明

包路径：`idx/`。本子系统消费对象属性变更流，增量维护一套
"按属性取值 → 对象集合" 的倒排索引，并支持索引依据字段随对象类型
版本迁移而原子切换。

## 核心概念

- **事件（Event）**：`{ID, ObjectID, Property, Value, Null, Version}`。
  `Version` 是源系统为同一 `(ObjectID, Property)` 分配的逻辑序列号
  （如 CDC 日志位点），`ID` 全局唯一。
- **合法串行顺序**：同一 `(ObjectID, Property)` 上的事件按
  `(Version, ID)` 升序应用。这是系统判定"合法顺序"的唯一依据，
  **物理到达顺序不参与排序**。`ID` 提供 `Version` 相同时的确定性
  全序与重复事件去重。
- **LWW 寄存器**：每个 `(对象, 属性)` 一个 Last-Writer-Wins 寄存器，
  只保留当前按 `(Version, ID)` 全序胜出的取值。乱序到达的旧事件
  （不大于当前胜出者）被丢弃，重复事件幂等忽略。寄存器是索引的
  唯一事实来源，规模上界为 O(对象数 × 属性数)，与事件总量无关。
- **索引版本（epoch）**：以某个属性字段为依据的倒排索引
  （`取值 → 对象集合` + `对象 → 当前取值`）。每次切换产生新 epoch，
  旧 epoch 退役保留供审计。

## 关键取舍

### 1. 乱序/重复：LWW 而非事件日志重放

索引不保存事件历史，只在寄存器上比较 `(Version, ID)`。增量维护
单条事件为 O(1)，查询为 O(1) 定位 + O(k log k) 排序 k 个命中对象。
被放弃的方案：

- **按物理到达顺序覆盖**：乱序到达时旧值会覆盖新值，直接违反
  最终等价性要求。
- **保存全部事件、查询时重放**：查询开销随事件总量线性增长，
  违反查询开销约束。
- **向量时钟/版本向量**：变更流来自单一上游日志，`(Version, ID)`
  已构成全序，无需更重的因果追踪结构。

### 2. 切换：两阶段 + 缓冲，归属按"事件针对的字段"判定

切换协议：`BeginSwitch(newBasis)` →（校验、回填、缓冲事件）→
`CommitSwitch()` / `RollbackSwitch()`。

- **切换时刻**：`CommitSwitch` 成功返回的瞬间（活跃 epoch 指针的
  原子翻牌，在互斥锁内完成）。
- **跨越切换点事件的归属规则**：由**事件针对的属性字段**决定，
  与到达消费端的物理时序无关——旧依据字段的事件归入旧索引版本，
  新依据字段的事件归入新索引版本。切换进行期间到达的事件一律
  先缓冲，提交/回滚时按此规则分类应用。这覆盖了两种跨越情形：
  切换前到达但针对新字段的事件（进入回填或缓冲，归入新版本）；
  切换后到达但针对旧字段的事件（归入旧版本，保留完整历史）。
- **查询端一致性**：切换进行中查询返回 `ErrSwitchInProgress`，
  绝不返回新旧依据混杂的结果；切换完成后查询只命中新 epoch，
  其内容 = 回填（寄存器中既有最终取值）+ 缓冲的新字段事件，
  内部一致。
- **快速连续变更跨切换**：同一属性的多次连续变更无论与切换如何
  交错，最终都汇入同一寄存器按 `(Version, ID)` 决出唯一胜出者，
  中间取值不会固化进索引（`TestRapidChangesCrossingSwitch`）。

被放弃的方案：

- **按事件 Version 与切换生效版本比较来归属**：迁移公告的生效
  版本与事件版本来自不同时钟域时无法可靠比较；字段身份是事件
  自带的、无需外部时钟的确定性依据。
- **新旧索引并行运行、双读比对**：查询端会在比对窗口观察到
  两个版本的差异，违反"不得观察到混杂中间状态"。
- **无锁 CAS 状态机**：正确性论证复杂；单互斥锁的可线性化
  语义更简单且足以满足本地子系统的吞吐需求。

### 3. 失败回滚：缓冲事件不丢失

`CommitSwitch` 先运行校验器（默认 `RequireNonNullForAll`：所有已知
对象在新依据字段上必须有非空取值，可注入自定义校验器）。校验失败
或主动 `RollbackSwitch` 时整体回滚到切换前状态：

- 缓冲的旧字段事件应用到旧 epoch，继续生效；
- 缓冲的新字段事件写入寄存器（不丢失），待未来再次切换时经回填
  生效（`TestSwitchRollback` 验证两次切换后取值完整）。

### 4. 错误分类与优先级

四类错误互不相同的哨兵错误（`errors.Is` 可判定），同时触发时按
优先级只报告一类（`pickError`）：

| 优先级 | 错误 | 触发条件 |
|---|---|---|
| 0（最高） | `ErrDeprecatedNoReplacement` | 待索引属性已废弃且未指定替代字段 |
| 1 | `ErrSwitchValidationFailed` | 切换提交时校验失败（已自动回滚） |
| 2 | `ErrPropertyNotDefined` | 事件属性在其生效版本的类型定义中不存在 |
| 3 | `ErrSwitchInProgress` | 查询发生在切换未完成瞬间 |

任何错误路径都不修改索引状态（摄入校验先于应用、切换校验失败
自动回滚、查询只读），保证不出现新旧版本混杂。

### 5. 并发：单 RWMutex 可线性化

摄入、切换、查询可并发发起。所有公开方法在单个 `sync.RWMutex`
上串行化（查询走读锁，切换期间查询立即返回错误而非阻塞），
因此任意并发操作集合的可观察结果等价于按锁获取顺序的全局串行
执行。`go test -race` 与 `TestConcurrency`（并发摄入/切换/查询后
与朴素模型对照）共同验证。

### 6. 查询开销与事件总量无关

索引结构规模上界为 O(对象数 + 不同取值数)，不保存事件历史；
查询为哈希定位 + 命中集合排序。独立验证方式：

- **结构不变量**：`TestQueryCostIndependentOfHistory` 处理 20 万条
  乱序/重复事件后，断言 posting 总规模不超过对象数，并与朴素模型
  逐值对照内容正确。
- **基准**：`BenchmarkQueryScaling` 在固定对象数/取值基数下将累计
  事件量从 1e3 提升到 1e6（1000 倍），单次查询耗时大致持平
  （本地实测约 177ns → 508ns，变化来自缓存效应而非渐近增长，
  1e5 与 1e6 之间已完全持平）。复现：
  `go test -run xxx -bench=QueryScaling -benchtime=2000x ./idx/`

### 7. 可审计性

每次判定（应用/丢弃/去重/缓冲/归类/切换/回滚/查询/模型对照）都
记录 `AuditEntry{Kind, Event, EpochID, Basis, Detail}`，包含输入、
所依据的索引版本与结论，供事后核查。随机对照测试把每一次与朴素
模型的比对结论也写入审计（`AuditModelCheck`）。

## 测试矩阵

| 测试 | 覆盖要求 |
|---|---|
| `TestOutOfOrderDuplicateExhaustive` | 3 事件全排列 × 重复模式 = 48 种投递序列穷举 |
| `TestTombstone` | 置空事件乱序到达不得复活旧值 |
| `TestSwitchAttribution` | 跨越切换点事件归属 + 审计核对 |
| `TestSwitchRollback` | 校验失败整体回滚、缓冲事件不丢失 |
| `TestRapidChangesCrossingSwitch` | 连续变更跨切换，中间取值不固化 |
| `TestErrorPriority` | 多类错误同时触发只报最高优先级 |
| `TestAuditTrail` | 判定留痕完整性 |
| `TestModelRandomized` | 随机操作序列（含切换/回滚/乱序/重复）与朴素批量重建模型逐条对照，8 组配置 |
| `TestConcurrency` | 并发摄入/切换/查询的可线性化（-race） |
| `TestQueryCostIndependentOfHistory` | 查询开销与事件总量无关的结构证明 |
| `BenchmarkQueryScaling` | 查询耗时随事件量持平的时序证据 |

## 本地验证方法

```bash
export PATH=$PATH:/usr/local/go/bin   # Go 1.26+
go test ./...                          # 全量测试
go test -race -v ./idx/                # 竞态检测 + 详细输出
go test -run xxx -bench=. -benchtime=2000x ./idx/   # 基准
go test -coverprofile=coverage.out ./idx/ && go tool cover -func=coverage.out
gofmt -l . && go vet ./...             # 格式与静态检查
```
