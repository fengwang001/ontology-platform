# 采样剖析与热点归因器设计

## 1. 数据模型

- 栈 = 帧名序列，根在首（`[A,B,C]` 表示 A 调用 B 调用 C）。
- `stack.Normalize(frames, maxDepth)` 去重相邻帧（尾帧递归折叠），深度超过
  `maxDepth` 时在 `maxDepth` 处截断，最后一帧名追加截断标记并返回
  `truncated=true`。空帧名 `""` 合法；空切片为无效栈。
- 调用树按「调用路径」建节点：key 为 `父指针 + 帧名`，故同名函数在不同
  路径上是不同节点。

## 2. self / total 与两条恒等式

- 一条样本落树：路径上每个节点 `total++`，仅末节点 `self++`。
  因此 `self(n)` = 以 n 为末帧的样本数；`total(n)` = n 子树全部样本数，
  且 `total(n) = self(n) + Σ total(child)`。
- 恒等式 1：**Σ_self = 有效样本数 N**（每条样本恰在末节点贡献 1 self）。
- 恒等式 2：**Σ_total ≠ N**，根栈时 Σ_total = Σ 各样本栈深 ≥ N
  （祖先被后代样本重复计入），深度全为 1 时取等号。
- 截断样本：末节点（截断点）带 `Truncated` 位，仍按末节点计 self；
  树级 `truncatedSamples` 累加，不丢整样。深度恰好等于上限不截断。

## 3. 递归帧的函数级归因

栈 `A→F→G→F→H` 建树为路径 A-F-G-F-H（两个 F 是两个节点）。
若把所有 F 节点的 total 相加，外层 F 的 total 含内层 F 子树，内层 F
被重复计入，故错误。规则：

- 函数 F 的**函数级 total** = 只累加「沿路径不被另一个 F 祖先包含」的
  最外层 F 节点的 total；内层 F 由其外层 F 代表。
- 函数级 self = 全部名为 F 的节点 self 之和（self 只落在路径末帧，
  不存在祖先与后代同时吃同一 self 的情况，可直接相加）。
- 本例：H self=1；F(内层) self=0,total=1；G total=2；F(外层) total=4；
  A total=5。函数 F 级 total = 4（最外层那次），不是 4+1=5。

## 4. 排除帧归因

`Exclude(root, name)`：一遍 DFS，遇到名为 name 的节点将其旁路——
其 self 计入调用方（父），其余子树在逻辑树上上提。等价于按样本：
样本经过任一 name 帧时，其 self 归到路径上 name 帧外侧最近的非 name
帧；不经过 name 的样本不变。

## 5. 复杂度

- 插入一条栈：沿路径逐级查 `map[childKey]*Node`，比较次数 = 栈深
  （map 访问按常数 4 计），与已有节点总数无关。计数器 `insertCompares`
  满足 `Σ ≤ N × depth × 4`。
- 归因只对已有节点一遍扫描 + 一次 `sort.Slice`；永不重建树，
  `rebuildCount` 恒为 0。

## 6. 采样器

- 注入 `Clock`（Now/Sleep，Sleep 可被 stop 立即唤醒）与 `StackSource`
  （返回栈，或哨兵错误 `ErrDropped` 表示忙窗口丢样）。
- 每次 tick 取一次栈：丢样 → `dropped++`；空栈 → `invalid++`；
  有效 → 规范化并插入。恒等式：
  `Σ_self + dropped + invalid = expected`（expected = tick 数）。
- 时钟异常（回拨/间隔 ≤0）→ 跳过该次、`anomalies++`，不影响恒等式。
- 停止幂等：`sync.Once` 关 stop 通道。快照在 RWMutex 下复制整树，
  查询不可能看到「子 total 已加而父未加」的半更新状态。

## 7. 落盘格式（小端）

```
"prof\x01"            magic+version   5B
u64 maxDepth          8B
u64 sampleCount       8B（有效样本）
u64 truncatedSamples  8B
u64 nodeCount         8B
u32 headerCRC32       4B（覆盖前 37B）        头共 45B
节点区：先序 DFS，每条记录定长 10B 头 + 变长名
  u8 flags(bit0=Truncated)  u8 depth   u16 nameLen
  u64 self  u64 total  name 字节（4 字节对齐补齐）
u32 bodyCRC32         4B（覆盖整个节点区）
```

按读到的长度分类，`errors.Is` 可区分：

- 不足 45B，或头 CRC 不符 → `ErrTruncatedHeader`；
- 节点区中途不足一条完整记录，或 nodeCount 未读满 → `ErrTruncatedRecord`；
- 节点全部读完但尾 CRC 缺失/不符 → `ErrCRC`。

任何错误下都返回「最大可恢复前缀」：先序 + depth 保证父先于子，
完整记录才挂载，恢复树仍满足 Σ_self 自洽，不会父缺子存。
