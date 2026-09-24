# 采样剖析与热点归因器 — 设计推导

## 1. 数据模型

- 样本 = 一条调用栈（帧序列，栈底在前）。`stack.Normalize` 负责：折叠**连续重复**帧（去重）、按 `maxDepth` 截断。
- 调用树：每个节点保存 `Self`（恰好落在本节点的样本数）与 `Total`（经过本节点的样本数）。根为虚拟节点。
- 截断：栈深超过上限时保留前 `maxDepth` 帧，截断点叶节点打 `Truncated` 标记，树的 `TruncatedSamples` 计数加一；样本本身仍插入，不丢弃。

## 2. self 与 total 的恒等式

定义：每条样本沿路径自根向下，给路径上**每个**节点的 `Total` 加 1，只给**叶**节点的 `Self` 加 1。

- 恒等式 A：`Σ Self = 总采样数`。每条样本恰好给一个叶节点贡献 1 次 Self，归纳即得。
- 恒等式 B：`Σ Total ≥ 总采样数`，且只要存在深度 ≥ 2 的栈则严格大于。每条样本给路径上 depth 个节点各贡献 1 次 Total，祖先被重复计入。
- 推论：把 Total 当 Self 用会高估（测试同时断言 A 与 B 防止混淆）。
- 节点级关系：`Total = Self + Σ children.Total`，递归展开即「Total = 子树 Self 之和」。

## 3. 递归帧的函数级归因推导

同一函数 F 出现在栈的多个层级（如 `A→F→G→F→H`）时，按调用路径建树会产生多个帧名为 F 的节点：外层 F₁（A 之下）与内层 F₂（G 之下），且 F₂ 是 F₁ 的后代。

- F₁.Total 已经包含经过 F₂ 的所有样本（F₂ 在 F₁ 子树内）。
- 若函数级 total = `Σ 所有 F 节点的 Total`，则 F₂ 的样本被算了两次：一次在 F₁.Total，一次在 F₂.Total。结论：**不能简单相加**。
- 正确口径：函数 F 的函数级 total = **只统计「祖先中没有 F」的最外层 F 节点**的 Total 之和。内层 F 的样本已由最外层 F 覆盖，既不漏也不重。
- 例：3 条 `A→F→G→F→H` + 2 条 `A→F→X`。F₁.Total=5，F₂.Total=3，朴素相加得 8（错），最外层口径得 5（对，等于经过 F 的样本总数）。
- Self 无此问题：Self 不在祖先间重复，函数级 self = 所有同名节点 Self 之和。

## 4. 复杂度约束

- 插入：每层一次哈希表查找（`children[frame]`），代价 O(栈深)，与已有节点总数无关。树内非导出计数器 `compares` 记录查找次数，插入 N 条深度 D 的栈总次数 = N·D ≤ N·D·4。
- 归因：一遍 DFS 聚合 + 一次排序，不重建树；`rebuilds` 计数器恒为 0，测试断言。

## 5. 采样器正确性

- 注入式时钟 `func() int64`（纳秒）与栈来源 `func() ([]string, bool)`；`ok=false` 表示「忙」，丢样并计入 `Dropped`。
- 恒等式：`Σ Self + Dropped = 应采样次数`（每次 Tick 且间隔正常即应采样一次）。
- 时钟回拨/跳变：`delta < 0 或 delta > 10×interval` 时跳过本次采样，计入 `BadInterval`，不产生负间隔或巨大间隔。
- 空栈拒绝并计入 `Invalid`；空帧名合法。`Stop` 用 `atomic.CompareAndSwap` 保证幂等。

## 6. 并发模型

树内嵌 `sync.RWMutex`：插入取写锁，归因/读取经 `Tree.Read(fn)` 取读锁。因此查询要么看到某次插入前、要么看到插入后的树，不会看到「子节点 Total 已加而父节点未加」的半更新状态。采样计数器用 `sync/atomic`。

## 7. 落盘格式与截断分类

字节布局（小端）：

```
头:   magic "ONTOPROF"(8) | version u32 | nodeCount u64        → 20 字节
记录: depth u32 | flags u8 | frameLen u32 | frame | self i64 | total i64  （先序 DFS，父先于子）
尾:   crc32-IEEE u32（覆盖此前全部字节）
```

- 读回时按序解析：头不完整 → `ErrHeaderIncomplete`；记录字节不足 → `ErrRecordIncomplete`；记录完整但 CRC 缺失/不符 → `ErrCRCMismatch`。三者用 `errors.Is` 区分。
- `Recover` 返回最大可恢复前缀：逐条解析完整记录建树。先序保证父记录先于子记录出现，故任何完整记录前缀都不会出现「父缺失而子存在」。
- 前缀自洽性：恢复出的树满足 `Σ Self = 已恢复样本数`（恢复样本数定义为前缀中各节点 Self 之和），且每节点 `Total ≥ Σ 子节点 Total`。
