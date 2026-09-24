# 采样剖析与热点归因器 — 设计

## 1. self 与 total

每条样本是一条栈（根 → 叶，叶为当前执行帧）。建树时沿栈走路径：

- 每个叶节点 `self += 1`（时间只算在当前帧）。
- 路径上每个节点（含叶）`total += 1`。

两条恒等式：

- `Σ self == 样本数`：每条样本恰好在叶上加 1 次 self。
- `Σ total != 样本数`：深度 d 的样本在 d 个节点上各加 1 次 total，祖先被重复计入。

## 2. 递归帧的函数级归因

递归使同一函数在不同路径层级出现多个节点（如 `A→F→G→F→H` 有两个 F 节点）。
设外层 F 节点 total=t1（它统计该样本 1 次），内层 F 节点 total=t2，且 t2 的
样本集合是 t1 的真子集。函数 F 的总占用若取 `t1+t2`，内层样本被计两次。
因此函数级 total 只累加**不被任何同函数祖先节点包含的最外层 F 节点**的 total。
同理函数级 self：只取最外层 F 节点的 self（内层 F 节点 self 为 0；连续递归帧
经去重折叠后，叶承载 self，内层 F self=0）。

## 3. 规范化与深度截断

- 空栈拒绝，计入 invalid；帧名允许空串；连续重复帧折叠为一帧。
- 栈深 > 上限 MaxDepth 时只保留前 MaxDepth 帧，最深保留帧带 `Truncated` 标记，
  截断样本数 +=1；恰好等于上限不截断。绝不丢弃整条样本。

## 4. 复杂度

- 插入：用 map 以 `(父id, 帧名)` 查子节点，每帧 1 次比较，插入深度 d 的栈为
  O(d)，与已有节点数无关。计数器 cmpCount：每帧比较记 1。
- 归因：对快照树做一遍 DFS（边累计边收集），随后一次 sort.Slice；不重建树，
  rebuildCount 恒为 0。

## 5. 并发

采样协程持写锁更新，查询协程持读锁取**不可变快照**（复制节点切片）后再排序，
故查询不可能观察到 half-update。Stop 用 sync.Once，幂等。

## 6. 采样器

注入：Clock（tick 推进 + now）、Source（返回栈）。busy 窗口 Source 返回
ErrDropped，计入 dropped；非回拨但 Source 失败计入 invalid，self 恒等式仍成立。
间隔 now-prevTick ≤0 或 ≥ 间隔×100（时钟回拨/巨跳）时跳过该次，计入
abnormalIntervals。

## 7. 落盘格式与截断

Header(25B)：魔数 4B `PROF`、版本 1B、MaxDepth u32、Samples/Dropped/Abnormal/
Invalid 各 u32（小端）；随后每条记录：
`depth u32 | nameLen u16 | name | self u32 | total u32 | trunc u8 | crc32 u32`，
记录为先序 DFS。文件 CRC 即最后一条记录的 crc 字段，覆盖 header + 之前全部数据。

截断点 c（保留前 c 字节）三类错误（errors.Is 可判）：

- `c < 25`：ErrHeaderIncomplete；恢复 0 节点。
- `c ≥ 25` 且落在某条记录头/体部：ErrRecordIncomplete；恢复此前完整记录。
- `c ≥ 25` 且恰在某条记录结束（含其 crc 字段）：记录完整；该 crc 不等于按
  header+已恢复数据重算的值（短于真实文件时必然）：ErrCRC，恢复此前完整记录。

解析边读边挂树；任何恢复前缀都无父缺失（先序 + depth），并校验
`Σ 叶 self == 已恢复样本数`，保证自洽。

## 8. 边界

零样本归因返回空切片、nil error；单样本；全同栈；深度 1；空栈拒绝；
空帧名合法；上限边界见 §3。
