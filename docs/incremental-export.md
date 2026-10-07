# 增量导出位点推进与去重合并组件设计说明

包：`incremental`（源码见 `incremental/`）。

## 1. 问题与目标

本体平台的增量导出按多个周期分批执行：每个周期从上一次确认的位点
开始，向外输出新发生的写入。组件需要在周期中断、位点记录损坏、
写入重试、多链路并发的情况下保证：

- 不遗漏任何已应输出的写入；
- 同一条写入在输出中至多出现一次（推导回退场景允许“重复检查”，
  见 §4）；
- 中断恢复后的累计输出与假设从未中断的持续输出逐条一致。

## 2. 核心抽象

| 抽象 | 职责 | 实现 |
| --- | --- | --- |
| `Source` | 本体数据源的追加式写日志，`Scan(from,to]` 按位点升序返回写入 | `MemSource` |
| `CursorStore` | 各链路“上一次已确认结束位点”的持久化（缓存） | `MemCursorStore`，支持 `Corrupt` 故障注入 |
| `Ledger` | 各链路已确认增量记录的账台，**确认的事实来源** | `MemLedger`，支持 `Drop` 故障注入 |
| `CycleDeduper` | 周期内去重 + 有界跨周期近期窗口 | `dedup.go` |
| `DeriveSafeCursor` | 位点记录不可读时从历史增量记录推导安全位点 | `ledger.go` |
| `Exporter`/`Cycle` | 周期编排、错误判定、多链路并发 | `exporter.go` |

关键取舍：**账台（Ledger）是确认的唯一事实来源，位点记录
（CursorStore）只是它的缓存**。`Commit` 先原子追加增量记录，再更新
位点缓存。因此位点记录损坏永远可以用历史增量记录重建；反向则不
成立，所以历史缺口是独立的、更严重的错误类别。

## 3. 周期协议

1. `BeginCycle(chain, declaredStart, end)` 声明起止位点。起始位点必须
   等于上一次已确认的结束位点，不允许凭空选择；否则返回
   `ErrKindStartMismatch`。
2. `Submit(write)` 提交写入。同一写入（按 ID）在同一未确认周期内
   无论重试多少次，只有首次被接受并进入输出缓冲，其位置不随重试
   次数或时刻改变；夹在中间的其它写入不受影响。
3. `Commit()` 只有在周期内全部应输出的写入都进入缓冲后才允许调用；
   追加增量记录 → 更新位点缓存 → 发布输出，此后结束位点才算确认。
4. `Abort()`（或进程崩溃）丢弃缓冲，不确认任何位点。重新发起的
   周期从上一个已确认位点重新开始；由于源日志扫描 `(start, end]`
   是确定的，且周期内去重以首次接受为准，重发周期的输出与假设
   中断从未发生的持续输出逐条一致（测试
   `TestAbortAndRestartProducesIdenticalOutput`、
   `TestRepeatedInterruptsEqualUninterrupted`）。

## 4. 位点不可读时的安全推导

`CursorStore.Load` 返回 `ErrCursorCorrupt` 时：

1. 记录 `ErrKindCursorUnreadable` 事件（挂在 `Cycle.Findings` 或
   错误的 `Error.Findings` 上）；
2. 若本进程缓存了最近确认/推导的位点，直接使用（进程内恢复）；
3. 否则 `DeriveSafeCursor` 从 1 号记录开始校验连续性（序号连续、
   `Start` 衔接前一条 `End`），取连续前缀末端为安全位点。

安全性论证：连续前缀中的每条记录都是曾经被确认的增量，其末端
位点一定曾经被确认过，因此推导结果**不晚于**真正已确认的位点
——宁可重复检查，不得遗漏。

两种退化情形：

- **尾部记录缺失**与“该周期从未确认”不可区分，推导自然得到更
  保守的位点，不报错（这是安全的）；
- **中间缺口**可被确定检测（序号断档且之后仍有可见记录），返回
  `ErrKindHistoryGap`，错误中携带缺口前的安全位点
  （`Error.SafeCursor`）。调用方可显式以该位点重新发起周期：
  已输出部分由跨周期去重窗口抑制，缺失部分被重新输出
  （测试 `TestDerivedSafeCursorAllowsRestartWithoutLoss`）。

## 5. 去重判定与有界开销

去重分两层，均与链路历史总量无关：

- **周期内集合**：只保存当前未确认周期首次接受的写入 ID，
  `Commit`/`Abort` 即整体丢弃。规模 ≤ 本周期写入数。
- **跨周期近期窗口**：固定上限 `Options.DedupWindow`，只保留最近
  已确认记录尾部的写入 ID，用于位点推导回退后的重复检查。

可复核方式：`Cycle.DedupStats()` 暴露两个结构的实时规模，
`TestDedupWindowStaysBounded` 在 50 个周期持续追加历史的过程中
逐周期断言二者分别不超过“本周期写入数”与窗口上限——开销只与
当前周期及固定窗口有关，不随历史总量增长。

## 6. 错误分类与固定判定顺序

优先级（高 → 低）固定为：

1. `ErrKindStartMismatch`：起始位点与已确认结束位点不匹配；
2. `ErrKindCursorUnreadable`：位点记录不可读，触发推导；
3. `ErrKindHistoryGap`：推导发现历史增量记录存在缺口；
4. `ErrKindResourceExhausted`：输出过程中资源不足，中止周期。

理由：该顺序与流水线阶段（声明 → 读取 → 推导 → 输出）一致。
起始位点不匹配是调用方违反协议的逻辑错误，继续执行会污染整条
链路，必须最先暴露；位点不可读触发恢复流程；历史缺口只能在恢复
流程中被发现；资源不足只可能发生在输出阶段。补充规则：推导因
缺口失败时已确认位点的权威值不可得，起始位点校验无从适用，
直接报告缺口（错误已携带安全位点）。测试
`TestErrorPriorityStartMismatchBeatsUnreadable`、
`TestErrorPriorityHistoryGapBeatsResource` 固化该顺序。

## 7. 多链路并发

每条链路有独立的互斥锁、位点记录、账台记录序列与输出序列；
`BeginCycle` 获取链路锁，`Commit`/`Abort` 释放。不同链路的周期
可真正并发，同链路周期被串行化，因此每条链路的结果等价于它
单独串行执行的结果（测试 `TestConcurrentChainsIndependent`，
`-race` 下验证）。

## 8. 被放弃的方案

- **全量历史去重索引**：判定“是否已输出过”最直接，但内存随历史
  总量线性增长，违反有界开销要求。放弃，改为“周期内集合 + 有界
  窗口”，窗口外的重复以“宁可重复检查”兜底。
- **位点记录作为主存储、账台仅作审计**：位点记录损坏时将无从
  恢复。放弃，改为账台为事实来源、位点记录为可重建缓存。
- **提交时两阶段写（先位点后记录）**：位点先于记录确认会在
  崩溃后产生“位点已推进但无输出记录”的空洞，重启后漏输出。
  放弃，确认顺序固定为“先记录后位点”。
- **中断周期保留部分输出**：会使重启后的输出依赖中断点位置，
  无法保证与未中断输出一致。放弃，未确认周期的输出整体丢弃。

## 9. 测试与本地验证

```bash
# 全量测试（含竞态检测）
go test -race ./...

# 差分对照的逐步输入/输出/判定依据日志
go test ./incremental -run TestDifferentialAgainstNaiveModel -v
```

覆盖要点：

- 周期在确认前各个时刻（未提交/提交一半/全部提交未确认）中断并
  重新发起，及确认后中断（`TestAbortAndRestartProducesIdenticalOutput`、
  `TestInterruptAfterCommitKeepsConfirmedCursor`）；
- 位点记录不可读时的安全推导与历史缺口
  （`TestCursorUnreadableDerivesSafeCursor`、
  `TestHistoryGapYieldsSafeCursorNotBeyondConfirmed`、
  `TestDerivedSafeCursorAllowsRestartWithoutLoss`）；
- 同一写入多次重试且与其它写入交错时的去重与位置保持
  （`TestRetryDedupKeepsFirstAcceptancePosition`）；
- 跨周期重试由有界窗口抑制（`TestCrossCycleRetrySuppressedByWindow`）；
- 资源不足中止且不确认任何位点（`TestResourceExhaustedAbortsCycle`）；
- 错误判定优先级（`TestErrorPriority*`）；
- 多链路并发等价于各自串行（`TestConcurrentChainsIndependent`）；
- 多次中断后累计输出与持续输出逐条一致
  （`TestRepeatedInterruptsEqualUninterrupted`）；
- 与朴素参照模型在 30 个随机种子、每个 400 步的随机中断/重试/
  损坏/重启序列上逐步对照，每步记录输入、输出与判定依据
  （`TestDifferentialAgainstNaiveModel`，`-v` 可见日志）。
