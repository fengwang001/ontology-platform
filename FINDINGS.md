# FINDINGS — 测试结论记录

## 表 ① 递归栈 `A→F→G→F→H` 的节点级与函数级归因

样本：`A→F→G→F→H` ×1、`A→F→G` ×1（共 2 个样本）。

| 节点（调用路径）   | self | total | 说明                     |
| ------------------ | ---- | ----- | ------------------------ |
| A                  | 0    | 2     | 两条样本的公共祖先       |
| A→F（最外层 F）    | 0    | 2     | 函数级 total 只计这一层  |
| A→F→G              | 1    | 2     | 第二条样本在此结束       |
| A→F→G→F（内层 F）  | 0    | 1     | 子树是外层 F 的真子集    |
| A→F→G→F→H          | 1    | 1     | 第一条样本的叶           |

- `Σself = 0+0+1+0+1 = 2 == 样本数`（恒等式 A 成立）。
- `Σtotal = 2+2+2+1+1 = 8 > 2`（恒等式 B：祖先重复计入，不能当 self 用）。
- **F 的函数级 total = 2**（最外层那次），而不是 2+1=3；内层 F 的样本必然
  先经过外层 F，简单相加会重复计数。测试 `TestAttrib` 已断言。
- 排除 G 后：G 的 self=1 归并到最近未排除祖先（外层 F），F 条目变为
  `{self:1, total:2}`，total 不变，G 条目消失。

## 表 ② 落盘文件截断点分类

测试固件：4 个样本、5 个节点（root/a/b/c/d），文件全长 175 字节
= 头部 32 + 记录 139（root@32、a@59、b@87、c@115、d@143，各 27/28 字节）
+ CRC 4。`TestTruncation` 用循环覆盖全部 174 个截断点，逐点断言分类与
恢复自洽（`Σself == 已恢复样本数`、无孤儿节点）。

| 截断点字节区间 | 分类（errors.Is）      | 恢复节点数（含根）     |
| -------------- | ---------------------- | ---------------------- |
| [1, 32)        | ErrHeaderIncomplete    | 1（空合成根，0 样本）  |
| [32, 59)       | ErrRecordIncomplete    | 1                      |
| [59, 87)       | ErrRecordIncomplete    | 1（仅 root 记录）      |
| [87, 115)      | ErrRecordIncomplete    | 2                      |
| [115, 143)     | ErrRecordIncomplete    | 3                      |
| [143, 171)     | ErrRecordIncomplete    | 4                      |
| [171, 175)     | ErrCRCMismatch         | 5（全部记录，仅缺 CRC）|

- 三类错误互不相同，`errors.Is` 可判定；`TestTruncation` 两两断言。
- 先序记录保证父先于子，任意前缀恢复出的森林无父缺失而子存在的情况。
- 完整文件 `Read` 往返后 self/total/截断标记逐节点一致（`TestRoundTrip`、
  `TestTruncatedFlagSurvives`）。

## 其他测试结论

- 丢样：1000 次应采样注入 137 次忙窗口，`dropped=137` 精确成立，且
  `Σself + dropped + invalid == expected`（`TestDrops` 表驱动 4 组场景）。
- 时钟回拨/巨跳：注入 Δ<0 与 Δ>10×Interval 各计 `anomalous`，跳过采样，
  恒等式不受影响（`TestClockAnomalies`）。
- 插入代价：10 万条深度 20 的栈，`insertOps` 介于 `深度×样本数` 与
  `4×深度×样本数` 之间，与节点总数无关（`TestInsertCostBound`）。
- 归因不重建树：多次 `BySelf/ByTotal/Excluding` 后 `Rebuilds()==0`。
- 并发：采样协程插入、4 个查询协程做快照归因，`-race` 干净，快照
  恒满足 `Σself == Samples`（`TestConcurrentSampling`/`TestConcurrentQuery`）。
