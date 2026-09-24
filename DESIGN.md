# 采样剖析与热点归因器 — 设计推导

## 1. 采样模型

按固定间隔抓取一条调用栈（帧序列，栈底在前：`A→B→C` 表示 A 调用 B 调用 C）。
每条合法栈插入调用树一次，恰好贡献 1 个采样。

## 2. self 与 total

- 树节点按「调用路径」唯一：路径相同才是同一节点，故递归同名帧可落在不同节点。
- 一条栈 `f0..fk` 从根沿路径插入，路径上**每个节点** total 加 1。
- 仅末节点（栈顶 `fk`）self 加 1。

恒等式：

1. `Σ node.self == 总采样数 S`：每条合法栈恰有一个栈顶，只在末节点落 self。
2. `Σ node.total >= S`，通常 `> S`：祖先的 total 重复计入后代采样，单链深 k 时
   `Σ total = k+1`（栈深 1 时取等）。故 total 之和不能当作采样数。

## 3. 递归帧的函数级归因

节点级 total 按路径分别统计。函数级 total 若把所有 F 节点 total 相加，
祖先 F 的子树包含后代 F，同一条样本会被算两次。

规则：函数 F 的 total 只计入**不被同一次遍历中另一个 F 祖先包含**的最外层 F 节点
（进入 F 子树后，内部再遇到 F 不累加）。这样每条样本在每条含 F 的栈路径上
至多计入一次，数值上等于「该样本是否经过 F」。

对单条栈 `A→F→G→F→H`：最外层 F 节点的 total=1，内层 F 被其包含，
故函数级 total(F)=1，而非 1+1=2。函数级 self 不受此影响：self 只落在末节点，
不同栈的末节点互不包含，可直接按函数名求和。

## 4. 深度截断

`Normalize(frames, maxDepth)` 去相邻重复帧（递归帧不相邻，保留）。
- 规范化后长度 > maxDepth：保留前 maxDepth 帧，截断点节点标记 `Truncated=true`，
  样本仍计入树，并令 `TruncatedSamples++`；不静默丢弃。
- 长度恰好 maxDepth：不截断。
- 空栈拒绝插入，计入 `InvalidSamples`，不贡献 self/total。

## 5. 采样器

时钟与 ticker、栈来源均可注入。每个 tick：
- 期望采样数 `wanted++`；
- 时钟回拨或间隔越界（负/过大）：跳过本次，`anomalous++`，不取栈；
- 取栈返回 `ErrBusy`：`dropped++`；空栈/错误：`invalid++`；
- 其余插入树。
恒等式：`self 之和 + dropped + anomalous + invalid == wanted`。
`Stop` 经 `sync.Once` 幂等。

## 6. 复杂度

- 插入：沿路径走 maxDepth 步，每步 map 查找一次，比较数 O(深度)，与节点总数无关；
  非导出计数器累计比较次数，10 万条深 20 栈上界 `100000*20*4`。
- 归因：对现有树一遍 DFS 收集 + 一次 `sort.Slice`；不重建树，
  `rebuildCount` 恒为 0。

## 7. 并发

树用 `sync.RWMutex` 包裹整棵树的一次插入；查询在 RLock 下取快照，
不会看到「子已加父未加」的半更新。归因全程只读。

## 8. 落盘格式

自描述头 + 逐节点记录（前序 DFS，含 depth 以恢复父子关系）+ 整体 CRC32(IEEE)：

```
"prof1"               5 字节魔数/版本
u64le sampleCount     合法采样数
u32le nodeCount
u32le crc32           对其后全部节点字节的校验
每条记录: u8 flags(bit0=truncated) u16le depth u32le self u32le total
          u16le nameLen utf8(name)
```

读回逐记录解析；CRC 位于文件末尾固定 4 字节。
- 头部不足/魔数错：`ErrBadHeader`；
- 某记录在完整 CRC 边界前截断：`ErrTruncated`；
- 字节完整但 CRC 不符：`ErrCRC`。
读回始终输出「最大可恢复前缀」节点；按 depth 挂接，缺父节点的记录拒绝，
故恢复树仍满足 `Σ self == 已恢复节点携带的样本数`（前缀来自原树的合法前序切分，
切分点处末节点 self 可能因兄弟未落盘而变小，但不会凭空出现）。
