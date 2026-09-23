# 并行任务图调度与失败传播 — 设计

## 1. 四类最终状态及判定推导

每个任务运行期只有一个瞬时阶段：`waiting → ready → running → terminal`，终态四选一：

| 状态 | 判定依据（按序判定，互斥） |
| --- | --- |
| Succeeded | 已实际执行且函数返回 nil；终态一旦写定，任何后续信号都不改标、不回滚。 |
| Failed | 已实际执行且函数返回非 nil（含 panic 恢复后合成的错误）。错误保留原始值。 |
| Skipped | **从未开始**：从未被调度取走过。某上游失败后，该任务不再可达（有一条失败路径通向它，或快速失败模式下仍在等待/就绪）。 |
| Canceled | **已经开始**（已被 worker 取走、执行函数已调用），失败传播触发 ctx 取消后由 ctx 中止，函数返回 `context.Canceled`。 |

区分 Skipped 与 Canceled 的唯一依据是「失败传播发生时函数是否已被调用」，用一个单调迁移的状态机
（waiting/ready → running 的迁移在加锁下单次完成）保证可判定。取消后迟到的结果一律丢弃：
worker 交回结果时若终态已被标记为 Canceled，则结果不得覆盖。

## 2. 失败传播与「根因」

- 每次失败按发生顺序取得单调递增 `rank`（同 rank 内按 ID 字典序兜底）。
- 跳过原因指向**根因**而非直接上游：传播沿边携带 (rank, id)，每经过一个节点取最小值。
  故 `A→B→C` 中 A 失败时，C 的原因是 A。多条失败路径汇合时取 rank 最小（再比 id）的失败任务，
  随机延迟下仍逐字节确定。
- 传播是惰性的：只在「某任务终态落定」时遍历其出边（O(边数) 总量），不全图扫描。

## 3. 两种模式

- FastFail（默认）：首个失败 → ctx 取消所有 running 任务（终态 Canceled）；所有未开始任务
  Skipped。原因：可达失败路径的带根因，不可达的旁支统一指向首个失败（rank 0）。
- BestEffort：首个失败后调度不停止；不依赖任何失败任务的分支继续跑完（可 Succeeded/Failed）；
  只有「存在一条从失败任务到达它的路径」的下游被 Skipped；没有失败上游的 running 任务正常完成，
  不产生 Canceled。

## 4. 包结构（均仅用标准库，状态在进程内存）

- `graph`：`Graph` 加边（重复边幂等）、依赖不存在报错、DFS 环检测（环路径每条边都在输入且首尾闭合，
  含自环）、Kahn 拓扑分层 Layers。
- `fail`：`Tracker` 状态机——状态枚举、状态迁移、panic 捕获、根因表、错误哨兵
  （`ErrCycle`/`ErrUnknownDep`/`ErrPanic`/`ErrCanceled`，支持 errors.Is）。
- `exec`：`Runner` 执行单个任务函数（defer recover panic→错误），定义 Task/Result。
- `sched`：`Scheduler`。单个协调 goroutine 拥有状态机；就绪集合用 ID 升序小顶堆，tie 取最小 ID。
  worker 池受信号量上限控制；`peakRunning` 记历史峰值，`decisions` 记就绪判定次数。
  协调循环每次只做常数工作 + 出边遍历，故 `decisions ≤ 4(V+E)`。
- `report`：按 ID 字典序渲染终态与原因，保证逐字节确定。
- `cmd/demo`：不读参不联网，逐项 OK/FAIL 判定。

## 5. 复杂度 / 并发 / 无泄漏

- 时间 O(V+E)；空间 O(V+E)。链式 1000 层用迭代 Kahn，无递归栈风险。
- worker 数恒 ≤ 上限 N（零依赖时也 ≤ N），由大小为 N 的令牌通道严格保证。
- 收尾：协调器 drain 完所有结果、关闭结果通道后退出；worker 随派发通道关闭退出；
  Run 返回前全部 goroutine 结束（含失败/取消路径）。

## 6. 确定性

报告按 ID 字典序；就绪多任务按 ID 升序派发；根因按 (rank,id) 全序。故完成顺序、
加边顺序、随机延迟均不影响报告字节。
