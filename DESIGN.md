# 遥测断点补传规划器设计说明

## 结构
- `seqset`：有序区间集合（有序切片 + 二分查找），元素为闭区间 [Start,End] 加属性
  （请求次数 C、是否在途、请求时刻 Q、代际 Gen）。非导出计数器 visited 记录一次
  操作中二分查找考察的区间节点数。
- `ingest`：拥有 Engine/Device 状态（hi、lo、req/inl/lost 三个 seqset、各计数器、
  在途小顶堆），提供 Register/Ingest/Hello/Stats，并向 plan 暴露原子原语
  （SettleTimeouts/FirstRequestable/IssueRequest 等），锁在 Engine 上保证可线性化。
- `plan`：仅含 Plan，编排 结算超时 → 取队首可请求段 → 按 Lm/budget/K 切发。

## 关键取舍
- Lost 为终态、迟到不恢复：水位 f 与守恒式只依赖"已决/未决"二分，恢复会引入
  三态迁移并破坏 Lost 单调与 f 单调；迟到仅计 Late，实现零代价且语义清晰。
- c 不同的序号不并入同一请求：请求是重放单元，混 c 会使单序号的已请求次数无法
  从请求清单精确复原；req 集合维持"相邻节点 c 必不同"不变式，扫描只取队首，
  考察段数自然不超过请求数 + 超时段数 + 1。
- 按区间而非逐序号存储：序号至 1e12，逐序号内存不可行；缺口段数 g 远小于序号
  范围，查找考察复杂度 O(log g)。
- 有序切片 + memmove，而非平衡树：g 通常很小，切片缓存友好、实现可靠；visited
  按二分比较探测计，memmove 只做整体搬移、不逐节点比较键，不计入考察。
- 在途小顶堆 + Gen 惰性失效：Ingest/Hello 拆分或删除在途段时不清理堆，结算时按
  (start, Gen) 校验，stale 项跳过，避免堆中任意删除。
- 全局 maxNow：错误优先级 ErrClockBack > ErrNoDevice 要求设备不存在时也能判定
  时钟回退，故时钟水位记在 Engine 而非 Device。

## 被放弃的方案
- 逐序号 map：内存随 hi 线性增长，且失去区间合并带来的段数界。
- treap/红黑树：随机树高无确定性上界，无法保证 2*ceil(log2(g+2))+4 的硬界。
- Lost 可恢复：见上，破坏终态语义与单调性，收益仅为少计 Late。

## 本地验证
- `go test ./...` 与 `go test -race ./...`：表驱动用例覆盖恰等超时、部分到达后
  各自成段、c 不同不合并、budget 截短、K 截止、第 R 次超时才判丢、Lost 后迟到、
  Hello 抬 lo 丢在途、守恒式与错误优先级。
- 1500 组随机操作序列与逐序号朴素模拟逐步对照（输入/输出/判定依据打日志），
  并双引擎重放验证确定性。
- visited 与 Plan 考察段数的上界在 g=10 与 g=10000 两档断言并打印日志。
