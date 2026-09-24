# 并行任务图调度与失败传播 — 设计推导

## 1. 四类最终状态及判定

失败发生瞬间，把每个任务按生命周期位置归入四类，互斥且可由状态枚举区分：

1. **已成功 Successed**：失败前已返回 nil。状态终态，永不回滚、不改标。
2. **失败 Failed**：真正启动过、返回非 nil（含 panic 转换），报告带原始错误。
3. **被跳过 Skipped**：依赖链上存在失败、且从未被调度启动。原因（Origin）必须指向
   其可达失败集合中 ID 最小的失败任务，即「最初失败任务」的确定性定义
   （多失败同时发生时无真实先后，用 ID 最小作全序，保证逐字节确定）。A→B→C、A
   失败：C 的原因是 A，而不是直接上游 B。
4. **被取消 Canceled**：快速失败模式下，失败发生时**已经启动、尚未结束**的旁支任务。
   其 ctx 被取消，结束后的返回值一律丢弃。

区分依据只有一条事实：**失败发生瞬间任务是否已被派发（占用过并发槽）**。
占用过 → Canceled；从未进入就绪出队 → Skipped。两者用不同状态枚举，绝不可混用。

## 2. 两种模式语义

- **快速失败 FailFast（默认）**：首个失败记录后，立即取消 ctx；在跑任务→Canceled，
  未启动的全部下游→Skipped（Origin 取可达失败中最小 ID）。
- **尽力而为 BestEffort**：不因失败取消 ctx。失败任务的下游若依赖（传递）任一失败则
  Skipped；依赖集合与失败集不相交的分支继续跑到自然结束，可成功/失败。

Origin 计算：失败集确定后，沿出边传播；一个被跳过任务的 Origin 取其所有失败祖先中
ID 最小者。该值按 ID 全序收敛，与完成顺序、边添加顺序无关。

## 3. 调度器结构（sched）

单 dispatcher goroutine + 工作 goroutine 模型：

- 就绪队列是 dispatcher 私有的最小堆（ID 升序），保证同时就绪时取序确定。
- 并发槽用容量 P 的带缓冲 channel 信号量控制，P 为并发上限（默认节点数，最小 1）。
- 每个被派发任务由独立 goroutine 运行 `exec.Run`，结果经无缓冲事件 channel 回报；
  dispatcher 串行处理完成事件 → 更新本任务状态、减余依赖、把新就绪者入堆。
  因此所有共享状态只由一个 goroutine 读写，无需锁。
- 计数：`peakRun`（派发/回收时更新最大占用）；`decisions`（每次「就绪判定」：
  入堆 1 次 + 出堆 1 次）。每条边在依赖完成时检查 1 次，总计 ≤ 3V+E
  ≤ 4(V+E)，不存在完成时全图扫描。
- 终止：active==0 且堆空即停。取消后慢任务结束事件仍被消费（丢弃结果），
  故无 goroutine 泄漏。

## 4. 分层职责

- `graph`：增边（去重幂等）、依赖不存在报错、迭代 DFS 环检测（环路径逐边真实闭合，
  自环可检出）、拓扑分层（Kahn，按层收集；1000 层为 O(V+E) 不递归，无栈溢出）。
- `fail`：状态枚举 `State`（Successed/Failed/Skipped/Canceled/Pending/Running）、
  哨兵错误 `ErrCyclic`/`ErrNoSuchTask`、`SkipError{Origin}`（`errors.Is` 可判）、
  `PanicError`（带原始 panic 值）、`CanceledError`。
- `exec`：`Func` 签名 `func(ctx)`；`Run` 起一个 defer recover 协程，
  ctx 先取消则返回 CanceledError；panic 转 PanicError；正常错误原样返回。
- `report`：`Result{Task, State, Err, Origin}` 列表按 ID 排序，`Render` 输出
  逐字节确定的文本，只含状态/原因，不含时序或计数器。
- `sched`：组装上述四包，返回 `*report.Report` 及内部统计。

## 5. 取消后写回的丢弃

dispatcher 对已标记 canceled 的任务事件，只回收并发槽与 active，不写状态/错误/结果；
其状态恒为 Canceled。任务侧即便忽略 ctx 继续执行并最终返回，也无法影响报告。

## 6. 确定性策略

报告按 ID 排序；就绪取 ID 最小；Origin 取最小 ID；状态机单线程驱动。
故边添加顺序打乱、注入随机延迟，仅改变真实完成时序，报告逐字节一致。

## 7. 边界与故障注入

空图立即返回空报告；并发度 1 时堆中任务串行执行；重复边去重；缺任务函数或指向
不存在节点的边返回可判定错误；环在执行前拒绝。全部异常路径均不泄露 goroutine。
