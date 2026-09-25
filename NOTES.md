# 写者优先读写锁：推导与不变量

| # | 操作 | 读者集合 | 写者 | 等待队列(FIFO) | 结果 |
|---|---|---|---|---|---|
| 1 | AcquireRead(R1) | {R1} | - | [] | 进入 |
| 2 | AcquireRead(R2) | {R1,R2} | - | [] | 进入 |
| 3 | AcquireWrite(W1) | {R1,R2} | - | [W1写] | 等待 |
| 4 | AcquireRead(R3) | {R1,R2} | - | [W1写,R3读] | 等待（有写者等待，读者排其后） |
| 5 | ReleaseRead(R1) | {R2} | - | [W1写,R3读] | 释放；W1因R2在读仍不可放行 |
| 6 | ReleaseRead(R2) | {} | W1 | [R3读] | 释放；放行队首写者W1 |
| 7 | AcquireRead(R4) | {} | W1 | [R3读,R4读] | 等待（写者持有） |
| 8 | ReleaseWrite(W1) | {R3,R4} | - | [] | 释放；无等待写者，FIFO放R3再放R4，并发持有 |

(甲) 读者优先实现只看"无写者持有"，第4步R3会直接进入与R1、R2同读；读者源源不断则W1永远等不到，写者饿死。
(乙) W1正确结果是等待（第3步入队）；若允许写者与读者同时持有，违反不变量1（互斥）。
(丙) 第8步后无等待写者，按FIFO先放行R3再放行R4（两者可并发）；LIFO则会变成R4先、R3后。

## 不变量保证位置与钉住测试

1. 互斥：授予只经 rw.CanRead/CanWrite 判定，lock 的 request 与 pump 仅在条件成立时落状态（rw/rw.go、lock/lock.go）；TestRandomMatchesReference、TestConcurrentAPI 钉住。
2. 写者优先：rw.CanRead 要求 waitW==0（等待写者 O(1) 标志），写者入队 IncWaitWriter、出队 DecWaitWriter（rw/rw.go、lock/lock.go）；TestEightSteps、TestRandomMatchesReference 钉住。
3. 与朴素参照一致：lock.pump 先放行队中最早的可授予写者，再在无等待写者时按 FIFO 批量放读者（lock/lock.go）；TestRandomMatchesReference 钉住。
4. 失败不留痕：空ID/未持有/重复释放三类在任何状态改动前返回哨兵错误（lock/lock.go）；TestSentinelErrorsAPI、TestSelfCheck 钉住。
