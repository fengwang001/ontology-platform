# NOTES — 无锁 MPMC 队列推导与不变量

## 一、New(3) 八步分步表（内容按队首→队尾）

| # | 操作 | 之后 Len | 队列内容 | 本步返回 |
|---|------|---------|----------|----------|
| 1 | Enqueue(1) | 1 | [1] | nil |
| 2 | Enqueue(2) | 2 | [1,2] | nil |
| 3 | Enqueue(3) | 3 | [1,2,3] | nil |
| 4 | Enqueue(4) | 3 | [1,2,3] | error: 满（ErrFull），状态不变 |
| 5 | Dequeue | 2 | [2,3] | (1, true) |
| 6 | Dequeue | 1 | [3] | (2, true) |
| 7 | Dequeue | 0 | [] | (3, true) |
| 8 | Dequeue | 0 | [] | (0, false) |

(甲) 单元素状态 Len==1 时，无哨兵链表 head==tail（都指向唯一节点），把 head==tail 判空 → Dequeue 返回 (0,false)；正确实现返回 (1,true)。差别：唯一的 1 被谎报为「空」而永久滞留，守恒被破坏。
(乙) 入队值取 0：New(1); Enqueue(0); Dequeue。第 1 次出队即暴露——取出真 0 得 0，空队列出队也得 0，二者同值。下游按「返回值即元素」处理时，会把空队列当成取到一个 0，凭空多消费一个元素，计数与守恒全错。
(丙) Enqueue(1); Enqueue(2); Close() 后：清空式 Close 的 Dequeue 立即 (0,false)，1、2 被丢弃；排空式（正确）依次 (1,true)、(2,true)、再 (0,false)，不丢任何元素。

## 二、四条不变量的保证位置与钉住测试

1. 与朴素参照一致：q 的 CAS 循环线性化点（链接成功/head 前移）⇔ api 内 naiveQ 逐步对拍；测试 TestMatchesNaive。
2. FIFO 且守恒：q.Enqueue 尾插、q.Dequeue 头取、len 原子 ±1；测试 TestFIFOAndConservation。
3. 空/满精确 O(1)：q.Dequeue 看 head.Next==nil、q.Enqueue 用 len 原子预占槽位判定满；测试 TestEmptyFullExact、TestDequeueVisitsOneNode。
4. 失败不留痕：ErrBadMaxLen/ErrFull/ErrClosed 三个哨兵错误均在任何状态修改之前返回；测试 TestRejectionLeavesNoTrace、TestErrorsDistinct。
