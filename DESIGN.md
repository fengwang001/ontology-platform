# 采样剖析与热点归因器设计

## 1. 数据模型
- 栈 = 自底向上的帧名序列；`stack.Normalize` 去除相邻重复帧、按 MaxDepth 截断。
- 截断：丢弃超出深度的帧，返回的栈带 Truncated 标记；截断样本计入 tree 的 truncated 计数，不丢整个样本。
- 空栈非法：采样器拒绝并计入 invalid，不得插入树。

## 2. self / total 与求和恒等式
树有不具名合成根；每条样本沿路径下沉，每经过一个真实节点 total+1，
仅在最深（最终）节点 self+1。因此：
- `Σ self == 样本总数 N`（每个样本恰好在一个叶子落点计数一次）。
- `Σ total ≥ N`：每个样本经过的每层各计一次，祖先被后代重复计入；栈深均为 1 时等号成立。
严禁把 total 当 self 使用。

## 3. 递归帧的函数级归因
同函数可出现在一条栈的多层（如 A→F→G→F→H），按调用路径建树得到两个 F 节点，
外层 F 的 total 已包含内层 F 的全部子树。若把所有 F 节点 total 相加，
外层样本会在“外层 F”和“内层 F”上各计一次 → 重复计数。
规则：函数 F 的函数级 total 只累加**不被另一个 F 包裹的最外层 F 节点**
（DFS 时携带“路径上是否已见 F”，已见则跳过该函数名的 total 累加）。
函数级 self 为所有名为 F 的节点 self 之和（self 不重叠，可安全相加）。

## 4. 深度截断标记
截断节点即路径上第 MaxDepth 个节点，带 truncated=true；truncatedSamples 单独累加，
恰好等于上限的栈不截断。

## 5. 复杂度
- 插入：每层一次 map 查找并下沉，比较次数 ≤ 深度。10 万 × 深度 20，上界 10万*20*4。
- 归因：对只读快照一遍 DFS 聚合 + 一次排序；不重建树（rebuilds 恒为 0）。
- 排除某帧：DFS 遍历时跳过该名字节点，其子树提升到父位置；被排除帧的 self 丢弃。

## 6. 采样器
注入 Clock、Timer（Wait 返回至下次唤醒的墙钟时长）、Source（返回当前栈，忙时返回 ok=false）。
每个 Tick：Wait；Δ<0 或异常巨大 → 跳过并计 abnormalIntervals；
ok=false 或空栈 → 若忙 dropped++，否则 invalid++；正常则入树。
恒等式：`Σself + dropped == 正常 Tick 数`；回拨 Tick 不影响。Stop 用 sync.Once 幂等。

## 7. 并发
树用 RWMutex：插入写锁，查询在 RLock 下整体快照后释放，查询只见整次插入前后的状态。

## 8. 落盘格式（自描述）
头：魔数 `OPRF`，u16 版本=1，u32 样本数，u32 截断样本数；
节点记录（DFS 先序）：u8 深度，u8 标志位(bit0=trunc)，u32 total，u32 self，u16 名长+UTF8 名；
末尾 CRC32-IEEE（覆盖此前全部字节）。
截断分类：读头不足 ErrHeaderTruncated；头过而记录/CRC 区不足 ErrRecordTruncated；
记录完整但 CRC 不符 ErrCRC。返回最大可恢复前缀；父节点必先于子节点，故恢复树自洽：
恢复树 Σself == 已恢复节点承载样本数。
