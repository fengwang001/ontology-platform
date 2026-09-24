# 设计推导：并行任务图的调度与失败传播

模块 `ontology`，仅标准库，状态全在进程内存。包划分：`graph`（建图/环检测/拓扑分层）、
`sched`（调度、并发上限、就绪队列）、`exec`（单任务执行、panic 捕获）、
`fail`（状态枚举与错误分类）、`report`（确定性报告）、`cmd/demo`（验收演示）。

## 一、失败后四类终态的判定推导

任务的生命周期：`pending → ready → running → 终态`。终态按两个正交维度判定：
**是否已经开始执行**、**自身是否返回了错误**。

1. **已成功 Success**：在首个失败发生前已跑完并返回 nil。判定：任务 goroutine 结束时
   其 ctx 尚未被取消且返回 nil。一旦落账永不改标（报告条目只写一次）。
2. **失败 Failed**：真正执行且返回非 nil 错误（非 `context.Canceled`），或发生 panic。
   报告保留原始错误；panic 包成 `fail.PanicError`，`errors.Is(err, fail.ErrPanic)` 为真。
   注意：即使取消已生效，只要任务返回了真实错误仍记 Failed——多个任务同时失败时
   全部记录，不被首个失败吞掉。
3. **被跳过 Skipped**：从未进入 running。判定依据：**goroutine 从未派发**。失败任务 F
   的传递下游在 F 完成时被直接标记 Skipped，原因记为 **F 自己**（沿 dependents 遍历时
   把最初的失败 id 一路透传，而不是直接上游）。因此 `A→B→C` 中 A 失败时，C 的原因
   指向 A 而非 B。同一节点被多个失败波及时，先落账者（即更早完成的失败）优先，不覆盖。
4. **被取消 Cancelled**：失败发生时**正在执行**的旁支。判定依据：**goroutine 已派发、
   返回时 ctx 已被取消、且未返回真实错误**（返回 nil 或 `context.Canceled`）。
   与 Skipped 的区分标准即「是否已经开始跑过」：实现上用 `inflight` 集合记录已派发
   任务，fail-fast 取消时只把不在 `inflight` 且未落账的任务标 Skipped；`inflight`
   中的任务等其返回后按上表分类为 Cancelled（或 Failed）。
   被取消后任务仍写回的结果**直接丢弃**：状态在分类时一次性落账，之后不再改写。

关键时序点：任务是否「在失败前完成」以其 goroutine 返回瞬间的 `ctx.Err()` 为准，
在 goroutine 内捕获，而不是以调度器处理完成事件的时刻为准——否则已成功的任务会被
随后的取消误标为 Cancelled。

## 二、两种模式的语义差异

- **快速失败（默认）**：首个失败落账后立即 `cancel()`，不再派发新任务。所有未开始
  任务（含与失败无关的分支）统一标 Skipped，原因指向首个失败任务；运行中任务待返回
  后标 Cancelled/Failed。语义：整图中止。
- **尽力而为**：不取消 ctx。仅失败任务的传递下游被标 Skipped（原因各自指向其最早的
  失败祖先），无关分支继续跑完。此模式下不出现 Cancelled。
- 因此同一图（`A→B→C` 加并行慢任务 S）下：快速失败得 `A=Failed, B/C=Skipped(A),
  S=Cancelled`；尽力而为得 `A=Failed, B/C=Skipped(A), S=Success`。

## 三、并发与复杂度约束的实现

- 并发上限：调度循环只在 `running < limit` 时派发，非导出计数器记录历史峰值
  `maxConcurrent`，经 `Stats()` 暴露。`limit<=0` 归一为 1（退化为串行）。
- 就绪判定次数：初始扫描每节点一次（N 次）；此后仅在任务**成功**时对其每个直接下游
  做一次计数递减（≤E 次）。总计 ≤ N+E，远低于上界 4(N+E)，绝不全图重扫。
- 无泄漏：所有派发的 goroutine 都经缓冲 channel（容量=limit）交回结果，调度循环在
  `running==0` 且就绪空时才返回，返回前每个 goroutine 都已汇合；`defer cancel()`
  释放 ctx 资源。三条路径（全成功/快速失败/尽力而为）均满足 `runtime.NumGoroutine`
  回基线。
- 链式 1000 层：拓扑分层用 Kahn 迭代、环检测用显式栈的迭代 DFS，无递归，不爆栈。

## 四、确定性的来源

- 就绪队列始终按任务 ID 升序弹出（有序插入）；同一时刻多个就绪时取字典序最小者。
- 报告按任务 ID 字典序逐行输出 `id \t status \t reason \t err`，与边的添加顺序、
  任务完成顺序无关；完成顺序的不确定只影响调度过程，不影响落账内容。
- 边的存储用集合（重复加边幂等），节点/边枚举前一律排序。

## 五、故障注入的处置

- **环**：`sched.Run` 先调 `graph.FindCycle`（迭代 DFS，灰点回溯取路径），检出即返回
  `*graph.CycleError`，路径中每条边都来自输入且首尾闭合；自环路径为 `[a a]`。
- **panic**：`exec.Run` 内 `recover`，转为 `Failed` + `PanicError`，调度器不崩溃。
- **多任务同时失败**：分类规则中「真实错误优先于取消」，故全部落账为 Failed；
  各自下游的 Skipped 原因分别指向各自最早的失败祖先。
- **取消后写回**：Cancelled 落账后该 id 不再接受任何状态变更，迟到的返回值被丢弃。
