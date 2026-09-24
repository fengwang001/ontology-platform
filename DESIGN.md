# 并行任务图调度与失败传播 — 设计推导

## 1. 包划分
- `graph`：任务图（邻接表+入度）、重复边幂等、缺失依赖校验、迭代式环检测（返回真实闭合路径）、Kahn 拓扑分层。
- `sched`：就绪最小堆（ID 升序）、并发计量器（峰值）、就绪判定计数；只做调度原语，不执行任务。
- `exec`：单 goroutine 事件循环 + 每任务一个 goroutine；context 取消；按 concurrency 闸门发放令牌。
- `fail`：状态枚举、哨兵错误（ErrFailed/ErrSkipped/ErrCancelled/ErrPanic，可 `errors.Is`）、panic 捕获、根因反查。
- `report`：收集结果，按 ID 字典序逐字节输出。

## 2. 四类最终状态的判定
任务生命周期只在事件循环线程上迁移：`pending → running → 终态`。
1. **成功 Success**：已运行并返回 nil。终态一经写入永不改变——“不回滚”由单调迁移保证。
2. **失败 Failed**：真正运行过，返回 error 或 panic（panic 被 recover 包成 `PanicError`，`errors.Is(err, ErrPanic)`）。报告保留原始错误。
3. **跳过 Skipped**：**从未开始**（未拿到令牌、未启动 goroutine），且其任意祖先存在失败。跳过原因必须指向**根失败**：对每个失败源做可达性反查（从失败点沿“失败 → 依赖它的下游”传播，多源时取 ID 最小的失败源，保证确定性），而非直接上游。A→B→C、A 失败时，B、C 都指向 A。
4. **取消 Cancelled**：失败发生时**已经开始**（goroutine 已启动、可能在跑）的旁支任务。事件循环取消 context；任务需尊重 ctx 并返回；若它在取消后仍把结果写回，结果通道消息到达时任务已被标 Cancelled，直接丢弃，状态不变。

区分依据：`Skipped` 从未 `running`；`Cancelled` 曾进入 `running`。慢任务与快失败并行时，慢任务必为 Cancelled 而非 Skipped。

## 3. 两种模式
- **快速失败（默认 FailFast）**：首个失败到达即 cancel 总 context：所有 running 任务最终 Cancelled；所有尚未启动的任务若祖先失败（关闭后无新成功，等价于“剩余全部”）标 Skipped，原因按根因反查。
- **尽力而为（BestEffort）**：不取消正在运行的任务；不依赖任何失败任务的旁支继续跑完；只有失败任务的下游标 Skipped。多个失败全部记录；被跳过任务的根因 = 其失败祖先中 ID 最小者。

失败到达即做一次传播标记：FailFast 把所有 pending 标记 doomed；BestEffort 只标记失败节点的下游。被标记的 pending 立即定终态（避免无完成事件而死等）。

## 4. 复杂度与并发
事件循环：节点出队 n 次、入队 n 次、边在完成事件中松弛共 e 次 → 就绪判定（成功后逐条松弛 + 入队/取队）总次数 ≤ 4(n+e)，不全图扫描。
并发由带缓冲信号量（容量=上限）限制；`running` 计数在同一循环线程更新，峰值只在单调点记录。500 任务/上限 8 时峰值恰 ≤ 8；上限 1 退化为串行。

## 5. 确定性
就绪多个时按 ID 升序取（最小堆）；报告按 ID 排序；根因多源取最小 ID；随机延迟只改变完成时序，不改变终态与文本。

## 6. 故障与边界
- 环：执行前 Kahn 迭代，剩余节点中 DFS 出一条首尾闭合、边边真实存在的路径（含自环）。
- 缺失依赖、空 concurrency：构建/启动期可判定错误。
- panic：任务包装函数 recover，返回带原始值的错误。
- 无泄漏：Run 等待所有已启动 goroutine 退出（wg.Wait 在返回前）；被取消任务必须尊重 ctx（测试任务如此实现），返回后 NumGoroutine 回基线。
- 1000 层链：图与传播全部迭代，无递归。
