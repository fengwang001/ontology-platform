# DESIGN — 采样剖析与热点归因器

## 1. self / total 定义与求和恒等式

每条样本是一条调用路径 root→…→leaf（root 先、叶在后）。插入时：路径上**每个**节点
`total++`，仅**叶**节点 `self++`。合成根节点（frame 为空、depth 0）只累计 total。

- **恒等式 A**：`Σ self(所有节点) == 总采样数 N`。每个样本恰好给唯一叶节点的 self
  贡献 1，self 是样本集的一个划分，可任意按维（节点、函数）求和而不重复计数。
- **恒等式 B**：`Σ total(所有节点) == Σ 每条样本(深度+1) > N`（深度 ≥1 时严格大于，
  仅合成根就贡献 N）。total 沿祖先链重复计数，**绝不能把 total 之和当 self 用**。
- 测试同时断言 A 与 B，防止把 total 误作 self。

## 2. 递归帧的函数级归因推导

按调用路径建树时，递归函数 F 会出现在多个节点上（如 `A→F→G→F→H` 里两个 F 节点）。

- **函数级 self**：对所有同名节点的 self 求和。合法，因为 self 是样本的划分。
- **函数级 total 不能简单相加**：内层 F 节点的子树是外层 F 节点子树的**真子集**
  （凡是经过内层 F 的样本必然先经过外层 F），两次相加把同一样本计了两遍。
- **正确定义**：F 的函数级 total = 所有**最外层** F 节点（即不存在同为 F 的祖先）
  的 total 之和。证明：最外层 F 节点的子树两两不相交（若相交则其一为另一的祖先，
  与"最外层"矛盾），且它们的并恰好覆盖所有经过 F 的样本，故求和 = 经过 F 的样本数，
  不多不少。
- **排除某帧 X 的归因**：把 X 节点从树中摘除，其子树接到最近未排除祖先上。样本
  是否经过某节点不变，故其余节点 **total 不变**；被摘节点的 **self 归并**到最近未
  排除祖先（相当于把 X 内联进调用者）。排除后恒等式 A 仍成立。

## 3. 深度截断

规范化时超过 `maxDepth` 的帧被丢弃，但**样本仍插入**（不静默丢样）：截断点节点打
`Truncated` 标记，`Tree` 累计 `TruncatedSamples` 可读出。深度恰好等于上限不截断，
上限加一才截断。连续重复帧先去重再判深度。

## 4. 复杂度约束

- **插入**：每帧在父节点的 `children` map 上恰好 1 次查找（未命中再 1 次插入），
  与已有节点总数无关。非导出计数器 `insertOps` 记录比较/查找次数，经导出方法
  `InsertOps()` 读出；上界 `4 × 深度 × 样本数`。
- **归因**：`NewReport` 对快照做一次先序扁平化；`BySelf/ByTotal/Excluding` 各自
  只是一遍扫描 + 一次排序，**不重建树**。`rebuilds` 计数器恒为 0，测试断言。

## 5. 采样正确性（故障注入）

- 采样器可注入时钟 `Clock`、栈来源 `Source`、忙窗口 `Busy`；固定间隔 `Interval`。
- **恒等式 C**：`Σself + dropped + invalid == expected`，其中
  `expected = 非异常 tick 数`。
- **丢样**：`Busy()` 为真 → `dropped++`，不取样。
- **时钟回拨/巨跳**：`Δnow < 0` 或 `Δ > 10×Interval` → `anomalous++`，跳过该次
  采样且不计入 expected，间隔计算不产生负数/巨大值。
- **空栈 / 来源错误**：拒绝并计 `invalid++`。
- `Stop()` 幂等（`atomic.Bool`），停止后 `Tick` 为 no-op。

## 6. 落盘格式与截断恢复

```
header  32B: magic "ONTOPRF1"(8) | ver u16 | reserved u16 | samples i64
             | truncatedSamples i64 | nodeCount u32
record 27B+name: parentIdx i32 | self i64 | total i64 | flags u8
             (bit0=truncated, bit1=root) | nameLen u16 | name bytes
trailer 4B: CRC32-IEEE，覆盖此前全部字节
```

- 记录按**先序**写出，父记录必先于子记录 ⇒ 任意记录前缀仍是无孤儿的合法森林。
- 截断分类（`errors.Is` 可区分）：`[1,32)` → `ErrHeaderIncomplete`；
  `[32, recordsEnd)` → `ErrRecordIncomplete`；`[recordsEnd, len)` → `ErrCRCMismatch`。
  单条记录的真前缀无法被误解析为更短的完整记录（nameLen 在定长偏移处，前缀长度
  不足 27+nameLen 即判不完整），故分类对全部截断点确定。
- `Recover` 返回**最大可恢复前缀**对应的快照，其 `Samples` 取已恢复节点的 self
  之和，保证「self 之和 == 已恢复节点的样本数」这一自洽性。

## 7. 并发

插入（采样协程）持 `sync.RWMutex` 写锁，`Snapshot()`（归因协程）持读锁深拷贝，
查询只读不可变快照 ⇒ 不会观察到半更新的树；`-race` 干净。
