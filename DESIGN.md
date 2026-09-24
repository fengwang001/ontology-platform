# 并行 DAG 任务调度与失败传播 — 设计

## 1. 模块划分（仅标准库，状态在进程内存）
- `graph`：任务图。`Node(id)` 加节点；`Edge(from,to)` 幂等加边。Build 用 Kahn 算法做拓扑
  分层（层=最长前驱链长度），迭代不递归，1000 层链不栈溢出；分层同时检测环：入度无法清零
  的节点即环上点，再在该子图上 DFS 回溯出一条真实闭合路径（首尾相同、每跳都是输入边）。
  自环、端点不存在、重复边（幂等）在 Build 时处理。
- `sched`：调度原语。`Slots` 是容量=并发上限的令牌信号量（缓冲 channel）；`ReadyQ` 同批就绪
  按 ID 升序弹出保证确定性；`Stats` 非导出计数 running/peak/decisions，通过访问器暴露。
- `fail`：每任务状态机。枚举 Success/Failed/Skipped/Canceled（内部 Pending/Running）。
  维护 started 集合作为“取消 vs 跳过”的唯一判据。
- `exec`：单事件循环驱动执行、panic 恢复、取消传播、结果收集。
- `report`：按 ID 字典序逐字节确定地渲染状态与原因。

## 2. 四类最终状态的判定推导
生命周期 Pending -> Running -> Success/Failed；传播只允许向下迁移。
- **已成功 Success**：传播发生前已写回成功，终态锁定，不回滚、不改标（晚到结果丢弃）。
- **失败 Failed**：任务体真正执行并返回 error（或 panic 被 recover 转换），带原始错误。
- **被跳过 Skipped**：终止时仍 Pending（从未开始）且有失败祖先。原因指向“最初失败源”而非
  直接上游：以失败任务为根做下游 BFS，只把仍 Pending 的节点标 Skipped，原因记为该根。
  故 A→B→C 中 A 失败时从 A 一次 BFS 把 B、C 都标为 skipped-by-A，C 的原因是 A 不是 B。
- **被取消 Canceled**：传播瞬间已是 Running（在 started 集合内）的旁支任务。判据“已开始且
  未终态”，与跳过的“从未开始”严格互斥。ctx 被取消，待其退出后记 Canceled；若忽略 ctx 稍后
  才写回结果，结果丢弃，状态保持 Canceled。

## 3. 两种模式语义
- **FailFast（默认）**：首个失败立即 cancel ctx，在跑旁支→Canceled；停止派发，所有仍 Pending
  且未标记的任务以该首失败为根标 Skipped（统一原因=首失败）。
- **BestEffort**：不 cancel ctx；不从失败任务派发后继，以每个失败为根把其后继 Pending 标
  Skipped；其余无依赖失败的分支继续跑完，可产生多个 Failed。某跳过点被多个失败祖先波及时，
  取“拓扑层最小、层同则 ID 最小”的失败为 origin（确定且为最早源）。该模式无 Canceled。

## 4. 执行循环与复杂度
单事件循环串行处理完成事件，状态迁移无需额外锁；任务体各自 goroutine，借 Slots 限并发。
就绪判定只在节点加入与完成时触发，每条边被消费一次，决策总数 O(V+E)，界 4(V+E)，
杜绝每完成一次全图扫描。peak 在每次获取令牌后更新。panic 在任务 goroutine 内 recover，
转成带 panic 标记的 Failed，调度器不崩。收敛：每任务恰好回写一次，终态计数==V 时退出并
WaitGroup 收尾，三路径均无 goroutine 泄漏。

## 5. 确定性与边界
报告按 ID 排序、就绪同批按 ID 升序，故完成顺序、加边顺序、随机延迟均不影响报告字节。
边界：空图合法；上限<=1 槽位置 1（串行，报告与并行一致）；重复边幂等；引用不存在节点
返回 error；环/自环执行前报错并给出可逐边验证的闭合路径。
