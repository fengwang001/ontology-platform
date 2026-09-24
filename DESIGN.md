# 采样剖析与热点归因器 — 设计推导

## 架构

- `stack`：调用栈规范化（连续同名帧去重、最大深度截断、空栈拒绝）。
- `sampler`：固定间隔采样，时钟/栈来源/忙窗口均可注入，计数丢样、无效样本、异常间隔。
- `tree`：调用路径树聚合，维护每个节点的 self/total。
- `attrib`：热点归因（按 self/total 排序、排除某帧、函数级 total），一遍扫描 + 一次排序。
- `dump`：落盘/读回（自描述头 + 逐节点记录 + CRC32），支持截断分类与最大前缀恢复。
- `cmd/demo`：逐条判定演示。

## self 与 total 的定义与恒等式

每个样本是一条调用路径，落在树的叶子节点上。定义：

- `self(n)`：恰好终止于节点 n 的样本数。
- `total(n)`：经过节点 n 的样本数，即 `total(n) = self(n) + Σ total(child)`。

两条恒等式：

1. `Σ self(n) == 总采样数 S`。每个样本有且仅有一个终止节点，恰好给某个节点的 self 贡献 1，故 self 构成对 S 的划分。
2. `Σ total(n) != S`（一般更大）。一个深度为 d 的样本给路径上 d 个节点的 total 各加 1，祖先被重复计入；`Σ total = Σ 样本深度 ≥ S`，等号仅当所有栈深为 1（根不计入时）。因此 total 之和不能当样本数用，测试同时断言两条以防混用。

## 递归帧的函数级归因推导

按调用路径建树时，递归函数 F 会出现在多个节点上（如 `A→F→G→F→H` 中两个 F 节点）。节点级 total 沿路径有包含关系：后代 F 的 total 已包含在祖先 F 的 total 中。若把「所有名为 F 的节点的 total」简单相加，同一样本被计入次数等于其路径上 F 的出现次数，结果可超过 S，错误。

正确定义：**函数 F 的函数级 total = 所有「最外层 F 节点」的 total 之和**，最外层指从根到该节点的路径上没有其他 F。

证明：任取一个含 F 的样本，其路径上恰有一个最外层 F 节点（路径上第一个 F），该样本被且仅被该节点的 total 计入一次；不含 F 的样本不被计入。故函数级 total 恰好等于「经过 F 的样本数」，既不多也不少，`≤ S` 恒成立。

self 无此问题：self 只在终止节点非零，函数级 self = 所有 F 节点的 self 之和，仍是划分。

## 深度截断

栈深超过 `maxDepth` 时保留前 `maxDepth` 帧，**不丢弃样本**：树的 `TruncatedSamples` 计数加一，截断点（第 maxDepth 层）节点置 `Truncated` 标记，可读出。深度恰好等于上限不截断。

## 复杂度

- 插入：子节点用 `map[frame]*Node`，每层一次哈希查找，代价 O(栈深)，与已有节点总数无关。非导出计数器 `lookups` 记录查找次数，上界 `4 × 栈深 × 样本数`。
- 归因：一遍 DFS 聚合 + 一次排序，不重建树；`rebuilds` 计数器恒为 0，测试断言。

## 并发

`tree` 用 `sync.RWMutex`：插入持写锁，查询先 `Snapshot()`（读锁下深拷贝）再离线聚合，因此查询不会观察到半更新的树。`sampler.Stop` 用 `sync.Once` 幂等；计数器用 `atomic`。

## 丢样与时钟回拨

每次调度 tick：时钟回拨（`now < last`）→ 计入 `anomalous` 并跳过，间隔计算不会出现负数/巨大值；忙窗口 → 计入 `dropped`；空栈 → 计入 `invalid`；否则规范化后插入。恒等式：`ticks == samples + dropped + invalid + anomalous`，其中 `samples == Σ self`。

## 落盘格式

```
header(28B): magic u32 | version u16 | flags u16 | nodeCount u32 | samples i64 | truncatedSamples i64
record:    parentIdx i32 | self i64 | total i64 | flags u8 | frameLen u16 | frame bytes   (23B + 帧名)
tail:      crc32(header+records) u32
```

记录按先序写出，父节点下标恒小于子节点，故任意完整前缀都不会出现「父缺子在」。截断分类（`errors.Is` 可区分）：`< 28B` → `ErrHeaderIncomplete`；头完整但记录不足 `nodeCount` → `ErrRecordIncomplete`；记录完整但 CRC 缺失或不符 → `ErrCRCMismatch`。`Recover` 返回最长完整前缀重建的树与分类错误，恢复树满足 `Σ self == 已恢复节点承载的样本数`。
