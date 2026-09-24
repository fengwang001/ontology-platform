# NOTES：抢占式变更事件处理器

## 一、八步推导（记法：pending 为 prio:[ids]；current 为 {prio,pos}；suspend 自底向上；`-` 为空）

朴素参照 = 在线贪心：每次 Process 取「已提交且未处理」中（prio 降序，到达 FIFO 升序）的第一个。

| # | 操作 | pending | current | suspend(底→顶) | done |
|---|---|---|---|---|---|
| 1 | Submit(E1,1) | 1:[E1] | - | - | [] |
| 2 | Submit(E2,1) | 1:[E1,E2] | - | - | [] |
| 3 | Process | 1:[E1,E2] | {1,1} | - | [E1] |
| 4 | Submit(E3,3) | 1:[E1,E2] 3:[E3] | {3,0} | [{1,1}] | [E1] |
| 5 | Submit(E4,5) | 1:[E1,E2] 3:[E3] 5:[E4] | {5,0} | [{1,1},{3,0}] | [E1] |
| 6 | Process | 1:[E1,E2] 3:[E3] | {3,0} | [{1,1}] | [E1,E4] |
| 7 | Process | 1:[E1,E2] | {1,1} | [] | [E1,E4,E3] |
| 8 | Process | - | {1,2} | - | [E1,E4,E3,E2] |

**(甲)** 第 4 步保存现场 `{prio:1, pos:1}`（prio=1、pos=1）。若抢占不存现场、恢复时游标重置 0：第 8 步恢复为 {1,0}，**E1 被重复处理**，第 8 步后 done=[E1,E4,E3,E1]；继续排空则 [E1,E4,E3,E1,E2]，E1 出现两次，违反不重复。
**(乙)** 第 6 步 E4 处理完后先恢复 **{3,0}（E3 批次，最近被抢占者）**。若 suspend 当 FIFO（先恢复最早被抢占的 {1,1}）：6~8 步处理 E4,E2,E3，done=[E1,E4,E2,E3]，E2 被提前到 E3 之前，违反 LIFO 恢复语义。
**(丙)** 同优先级必须 FIFO：E1 先于 E2。若同优先级用栈（LIFO），第 3 步处理 E2，done=[E2,E4,E3,E1]，E1/E2 相对顺序颠倒；若按 ID 排序，相对顺序由 ID 大小决定而非到达顺序（ID 升序时偶然一致，降序时同样颠倒）。

## 二、四条不变量：保证位置与钉住测试

1. 与朴素参照一致：`sched/sched.go` 的 `Submit`（抢占压栈）与 `Process`（选最高非空档、耗尽恢复栈顶）；测试 `TestNaiveReference`（sched/sched_test.go）。
2. 不重复不丢失：`Process` 中 `pos++` 单调推进、耗尽才弹栈/置空；测试 `TestExactlyOnce`。
3. 同优先级 FIFO：`q/q.go` 的 `Push` 尾部追加 + `At` 按下标顺序取；测试 `TestFIFOPairs`。
4. 失败不留痕：`Submit`/`Process` 先校验后改状态（校验全部前置）；测试 `TestFaults`。
