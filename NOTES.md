# 推导与不变量

初始日志 Seq=1 key=a、Seq=2 key=b；`Start()` 取当时 maxSeq，故 **S=2**。八步表：

| 步 | 动作 | 段 | 发出Seq | lastEmitted | 已发序列 |
|---|---|---|---|---|---|
| 1 | Write(c,1)→3 | — | 无 | 0 | [] |
| 2 | Next | 快照 | 1 | 1 | [1] |
| 3 | Next | 快照 | 2 | 2 | [1,2] |
| 4 | Write(d,1)→4 | — | 无 | 2 | [1,2] |
| 5 | Next | 增量 | 3 | 3 | [1,2,3] |
| 6 | Write(e,1)→5 | — | 无 | 3 | [1,2,3] |
| 7 | Next | 增量 | 4 | 4 | [1,2,3,4] |
| 8 | Next | 增量 | 5 | 5 | [1,2,3,4,5] |

Finish：快照区间 [1,2]，增量区间 [3,5]，无缝核验通过。
(甲) Seq=3> S=2，虽写在首个 Next 之前，仍属**增量段**。错记 S=3（Start 时 maxSeq+1）：第5步的 3 被吞进快照段，Finish 错报 快照[1,3]、增量[4,5]（增量首条不再是 S+1=3）。
(乙) 增量段跳读最新：第7步发 5（越过4）；第8步再发 5（重复）或停滞无新条；序列 [1,2,3,5,5]，**缺 4、重 5**。
(丙) Start 推迟到第1步之后：S=3，快照[1,3]、增量[4,5]，序列仍 1..5 但切分变了。分歧点是**第1步写入与第2步首次 Next 之间**：seq3 已在日志中却 3>S；按"到达/读取先后"会把它卷进快照（即甲的错误），只有按 Seq 与 S 比较才能保证两段拼接等于连续读取。

不变量（代码保证位置 / 钉住的测试函数）：

1. 与朴素全量一致：`exp.Next` 目标恒为 lastEmitted+1、`exp.Finish` 逐条核验 1..last 存在 / `TestNaiveEquivalence`
2. 段切分正确：`exp.Next` 以 `target <= S` 判段，`Finish` 回传 S 与 last / `TestSegmentBoundaries`
3. 升序无重复、增量首条=S+1：`exp.Next` 只发 last+1 且按序定位 / `TestEightSteps`
4. 失败不留痕：`wlog.Write` 先校验后追加、`exp.Start` 超限不计 active、`api` 先验证后落状态 / `TestRejectedNoTrace`

另：`TestCheckCountConstant` 钉 lastChecked 为与 m 无关常数；`TestConcurrentExports` 钉并发结果逐字段相同。
