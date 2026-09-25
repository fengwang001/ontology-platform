# NOTES

## 六步推导（权重表见题目，A<B<C<D）
1. ①加入 k1：A=10 最大 → k1=A
2. ②加入 k2：B=9 最大 → k2=B
3. ③加入 k3：A=B=6 并列，取字典序最小 → k3=A
4. ④加入 k4：C=9 最大 → k4=C
5. ⑤AddNode(D)：仅 k1 的 11>10 迁 A→D；k3 对 D 为 6，不大于原最大 6，不迁；k2/k4 不动
6. ⑥RemoveNode(A)：A 仅拥 k3；k3 在 B/C/D 中 B=D=6 并列取最小 → k3=B；其余不动
- （甲）并列误取字典序最大 → k3 归 B；误写成「后扫描覆盖先扫描」(A→B→C) → k3 也归 B（B 后于 A 同分覆盖）。
- （乙）mod3→mod4：k1=10：B→C；k2=13：1→1 留 B；k3=11：C→D；k4=5：C→B。HRW 只迁 k1，取模多了 k3 C→D、k4 C→B 两次无谓搬迁。
- （丙）每次调用重新播种：k3 在 B/D（权重各次随机）间反复横跳，同键多次结果不一致；违反不变量 3（确定性），朴素扫描也随之失效，连带违反不变量 1。

## 不变量：保证位置 / 钉住的测试
1. 与朴素扫描一致：rnd.Best 逐个节点比权重、并列取最小 ID，cluster 的 Owner/AddNode/RemoveNode 都经它定归属；TestOwnerMatchesNaiveScan、TestSixStepTable。
2. 最小搬迁：AddNode 仅在新权重严格更大时改一条归属；RemoveNode 只遍历 owned[id]；TestMinimalMigration、TestRemoveInspectsOnlyOwnedKeys。
3. 确定性：rnd.Weight 为 FNV-1a(长度前缀 key‖node)，无随机/时间/PID；TestDeterministic、TestConcurrentOwner。
4. 失败不留痕：cluster.go 三个哨兵错误均在任何状态修改前返回；TestRejectedOpsLeaveNoTrace。
