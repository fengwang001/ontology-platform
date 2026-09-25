# NOTES: queue via two stacks

## 推导：何时转移栈元素
- 正确做法：Enqueue 压入 in 栈；Dequeue 从 out 栈弹出；仅当 out 为空时，才把 in 整体倒入 out。
- 为何不能每次 Dequeue 都倒：out 非空时其栈顶就是队首，若此时把 in 倒在它上面，新元素会压住更早的元素，弹出顺序被破坏，FIFO 不再成立。
- 为何不能每次 Enqueue 都倒回：把 out 倒回 in 再压入，单次 Enqueue 移动 n 个元素，n 次操作共 O(n^2)，摊还退化为 O(n)，吞吐骤降（线上事故根因）。
- 为何「out 空才倒」摊还 O(1)：每个元素至多入 in 一次、随倾倒出 in 入 out 一次、出 out 一次，移动次数为常数；m 次操作总移动 O(m)，摊还每次 O(1)。
- 计数上界：每元素至多入 in 1 次、随倾倒移动 1 次、出 out 1 次，共 3 次移动；总移动 = 入队 E + 倾倒 P + 出队 D，P<=E 且 D<=操作数，故 moves <= 2*操作数，摊还每次操作 <= 2。

## 语义与代码位置
- FIFO 保序：queue/queue.go 的 Dequeue + pourLocked；测试 TestFIFO（check/check_test.go），对照 check.Verify（check/check.go）。
- 空队语义：queue/queue.go 的 Dequeue/Peek 返回 (零值, false)；测试 TestFIFO 用例 "empty/single dequeue/peek"。
- 惰性转移：queue/queue.go 的 pourLocked 仅在 out 为空时倒栈；测试 TestFIFO 用例 "1..5 then drain"、"alternating 10000"。
- 错误：无（空队已被覆盖）；哨兵错误 stack.ErrPopEmpty / stack.ErrPeekEmpty / queue.ErrDequeueEmpty 可用 errors.Is 区分，cmd/demo 判定。
- 边界：单元素、大量元素、交替入出队，见 TestFIFO 各用例。
- 复杂度：queue.go 非导出计数器 moves（Moves() 读取）；测试 TestMoves 断言摊还 <=2 且错误实现单次操作与 n 成正比。
- 并发：queue.go 内 sync.Mutex 串行化写；测试 TestConcurrentReads（16 读者只读，-race 干净）。
