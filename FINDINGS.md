# 实验结论

## 已验证结论（随测试推进补充）

- self/total 恒等式：零样本、单样本、全同栈、深度 1、混合深度五组场景下 `Σ self == Samples` 恒成立，`Σ total > Σ self`（样本数 > 0 时），见 `tree/tree_test.go` 的 `TestIdentities`。
- 深度截断：深度 9、上限 8 时 `TruncatedSamples=1`，截断点节点带 `Truncated` 标记，样本仍计入 self 之和；深度恰等于上限不截断。
- 插入代价：10 万条深度 20 的栈共 2,000,000 次查找，恰为 `深度×样本数`，低于上界 `4×深度×样本数`，与已有节点总数无关。
- 丢样：1000 次应采样注入 137 次忙窗口，`dropped=137` 且 `Σ self(863)+dropped(137)==1000` 精确成立。
- 时钟回拨：注入一次回拨后 `anomalous=1`，该次采样被跳过，`Σ self+dropped+invalid+anomalous==ticks` 恒等式在四组故障注入场景下均成立；停止后 Step 为 no-op，Stop 幂等。

## 表① 递归栈 `A→F→G→F→H` 的节点级与函数级归因

样本：5 次 `A→F→G→F→H`，2 次 `A→F→G`（共 7 个样本）。

| 节点路径 | self | total |
| --- | --- | --- |
| A | 0 | 7 |
| A→F | 0 | 7 |
| A→F→G | 2 | 7 |
| A→F→G→F | 0 | 5 |
| A→F→G→F→H | 5 | 5 |

- `Σ self = 7 == 样本数`；`Σ total = 38 > 7`（根 total 为 7，祖先重复计入）。
- F 的函数级 total = **7**（最外层 F 节点的 total），不是两个 F 节点相加的 12；G=7，H=5。
- 排除帧：`Excluding("b")` 把 b 的 self 记到最近可见祖先 a，b 不出现在结果中。
- 归因不重建树：多次 BySelf/ByTotal/Excluding 后 `Rebuilds()==0`；并发查询与采样在 `-race` 下干净，快照恒满足 `Σ self == root.total`。

## 表② 落盘文件截断点分类

样本文件：root→A→B→C 链（5 个样本），共 127B = 头 28B + root 记录 23B + A/B/C 记录各 24B + CRC 4B。逐字节截断 1..126 全部经 `dump.Recover` 分类（`dump/dump_test.go` 的 `TestTruncation` 循环覆盖）。

| 截断字节区间 | 分类（errors.Is） | 恢复节点数（含根） |
| --- | --- | --- |
| 1–27 | ErrHeaderIncomplete | 1（空根） |
| 28–50 | ErrRecordIncomplete | 1 |
| 51–74 | ErrRecordIncomplete | 1（root 记录已完整） |
| 75–98 | ErrRecordIncomplete | 2 |
| 99–122 | ErrRecordIncomplete | 3 |
| 123–126 | ErrCRCMismatch | 4 |

- 三类错误可用 `errors.Is` 两两区分；CRC 被篡改（非截断）同样归入 ErrCRCMismatch。
- 先序记录保证父先子后，任意完整前缀恢复出的树无孤儿节点，且恒满足 `Σ self == 恢复树 Samples`、`total ≥ self`。
- 完整文件回环（空树、单链、带截断标记的分叉树）后样本数、节点数、`Σ total` 均一致。
