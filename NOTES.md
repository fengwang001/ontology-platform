# NOTES — 无锁 MPMC 队列

## 一、第三节推导：New(3) 八步分步表

| # | 操作 | 之后 Len | 队列内容(首→尾) | 本步返回 |
|---|------|---------|----------------|---------|
| 1 | Enqueue(1) | 1 | [1] | nil |
| 2 | Enqueue(2) | 2 | [1,2] | nil |
| 3 | Enqueue(3) | 3 | [1,2,3] | nil |
| 4 | Enqueue(4) | 3 | [1,2,3] | ErrFull（状态不变） |
| 5 | Dequeue | 2 | [2,3] | (1,true) |
| 6 | Dequeue | 1 | [3] | (2,true) |
| 7 | Dequeue | 0 | [] | (3,true) |
| 8 | Dequeue | 0 | [] | (0,false) |

**(甲)** 单元素态（第 1 步后）无哨兵实现里 head==tail 指向唯一节点；若把 head==tail 判空，Dequeue 返回 (0,false)，而正确实现返回 (1,true)——元素 1 被判丢、永远取不出，Len 与内容从此矛盾。哨兵使空队列判据是 head.next==nil，与 head==tail 无关。
**(乙)** 入队值取 0：Enqueue(0) 后第 1 次 Dequeue 即暴露——无 ok 签名时它返回 0，与空队列 Dequeue 的零值 0 完全相同；下游（如 for v:=Dequeue(); v!=0 的取值循环）会把「空」当成「取到了 0」继续处理一个从未入队的元素，也会把真入队的 0 当成空而提前停。
**(丙)** Enqueue(1);Enqueue(2);Close() 后：排空实现 Dequeue 依次返回 (1,true)、(2,true)、然后恒 (0,false)；清空实现第一次 Dequeue 就返回 (0,false)，1、2 被丢弃。正确实现（排空）不丢任何元素。

## 二、四条不变量：保证位置与钉住测试

1. 与朴素参照一致：q.go 的 Enqueue/Dequeue 用 CAS 单步生效、count 原子增减，语义等同互斥切片队列；测试 q.TestMatchesNaiveReference。
2. FIFO 且守恒：链表追加在尾、摘取在头（q.Enqueue/q.Dequeue），count 只在链接成功/摘除成功时 ±1；测试 q.TestFIFOAndLenConservation。
3. 空/满精确：空判据 head.Next()==nil、满判据 count>=max，均 O(1) 读原子量；测试 q.TestEmptyFullExact。
4. 失败不留痕：ErrBadMaxLen/ErrFull/ErrClosed 都在任何状态修改之前返回；测试 q.TestFailureLeavesNoTrace。
