# 采样剖析与热点归因器 — 设计推导

## 1. 模型

- 一次采样得到一条调用栈：帧序列 `f0..fn-1`（f0 为最外层）。
- 规范化（`stack.Normalize`）：先折叠相邻重复帧（递归自调用的同义噪音），
  再按最大深度 `maxDepth` 截断；空帧名合法；空栈为无效样本。
- 调用树（`tree.Tree`）：前缀 trie。根为哨兵；每个真实节点记
  `self`（样本叶恰好落在该节点的次数）、`total`（经过该节点的样本数）、
  `truncated`（有多少样本在此节点被截断）。

## 2. self / total 定义与两条恒等式

插入一条栈（长度 k）：沿 f0..fk-1 下钻，路径上每个真实节点 `total++`，
仅叶节点 `self++`。因此：

- 恒等式 A：`Σ_所有真实节点 self = S`（S=有效样本数）。每条样本恰好给
  一个节点贡献 1 次 self，截断样本仍以截断点为叶，贡献不丢失。
- 不等式 B：`Σ total = Σ 样本栈深 ≥ S`，栈深 >1 时严格大于。total 在
  每条祖先链上重复计入，故 total 之和没有“样本总数”含义。

测试同时断言 A 与 `Σtotal > S`（存在深栈时），防止把 total 当 self。

## 3. 递归帧的函数级归因

路径建树使同一函数 F 出现为多个节点。朴素地 `Σ total(F节点)` 会把一条
  经过两层 F 的样本在 F 上计两次（外层 F 的 total 已包含内层 F 子树）。

函数 F 的 total 定义：对每条样本栈，只要栈中出现 F，就计 1 次。等价的
树算法：DFS 携带“栈上是否已见过 F”，**只统计不被另一 F 包含的最外层
F 节点**（outermost-F）：在某个 F 节点已开启的子树内，遇到内层 F 直接
跳过其子树、不再贡献 F 的 total。

对栈 `A→F→G→F→H`（单条样本）建树后：

- A.total=1,A.self=0；F(外).total=1,self=0；G.total=1,self=0；
  F(内).total=1,self=0；H.total=1,self=1。
- F 的函数级 total = 1（只取最外层 F），不是 1+1=2。

`Exclude(frame)` 同理：归因时在子树中首次遇到该帧即剪枝（被排除的调用
整支移除），一遍 DFS 完成，不重建树。

## 4. 深度截断

`len(stack) > maxDepth` 时只保留前 maxDepth 帧；末帧节点带截断标记
（`TruncatedSamples` 计数 +1，节点 `truncated=true` 并记次数），该样本
叶即截断点，self/total 照常累计。Tree 暴露可读出的总截断样本数；
`maxDepth<=0` 表示不限深。恰好等于上限不截断，上限+1 截断。

## 5. 复杂度

- 插入只沿父节点的子 map 按名查找一次/层，比较次数 ≤ 栈深。
  `tree.insertCmp` 为非导出计数器，断言插入 1e5 条深 20 栈时
  `insertCmp ≤ 1e5*20*4`，与既有节点总数无关（不遍历树）。
- 归因：持读锁对现有树做一遍 DFS 收集 + 一次 `sort.Slice`；
  `rebuildCount` 恒为 0（没有任何重建路径）。

## 6. 采样器

注入时钟 `Clock.Now` 与“下一次采样时刻”驱动（demo/测试用可控 tick）。
每个 tick 取一次栈：`busy()` 返回 true 记丢样；栈为空记无效；
间隔 `d=now-last` 满足 `d<=0 || d > 2*interval` 记异常间隔并跳过
（时钟回拨因此不产生负数或巨大等待，且不影响恒等式 A）。
恒等式：`Σself + Dropped = 应采样次数`（测试 1000/137，此时
Invalid=Anomaly=0）；更一般地 `Σself+Dropped+Invalid+Anomaly = 应采样`。
停止幂等：`Stop` 经 sync.Once + done channel，可重复调用。

## 7. 并发

Tree 用 `sync.RWMutex` 保护：插入持写锁在锁内完成整条路径，查询持读锁
快照；查询不可能看到“子 total 已加、父未加”。`-race` 下并发采样/归因。

## 8. 落盘格式与截断恢复

文件 = 头 + 逐节点记录 + CRC32 尾。

- 头：magic `"prof1"`、uvarint 版本(1)、uvarint 深度上限、uvarint 节点数、
  uvarint 总采样数、uvarint 截断样本数。
- 记录（前序遍历）：uvarint 深度(根=0)、uvarint 帧名长度+UTF-8、
  uvarint self、uvarint total、1 字节截断标志、uvarint 截断计数。
- 尾：4 字节大端 CRC32-IEEE（覆盖头与全部记录）。

截断分类（`errors.Is`）：

- `ErrHeader`：文件短于最小头或头字段/魔数不完整/损坏。
- `ErrRecord`：头完整但某条记录字段不完整。
- `ErrCRC`：头与全部记录完整、CRC 尾缺失或不匹配。

恢复：无论哪种错误都返回“最大可恢复前缀”。前序流深度 d>已恢复深度+1
  则记录不可能完整（父缺失），停止恢复；因此恢复树恒有
`Σself(恢复节点)= 已恢复节点的样本数`（逐样本叶保持完整），无父缺。
零样本时头后直接跟 CRC：任何缩短都落 ErrCRC，恢复空树。
