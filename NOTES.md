# NOTES

推导 B=3, r=1（剩余令牌 = 该事件处理完成后）：

| 事件 | 剩余令牌 | 判定 | admitAt |
|---|---|---|---|
| k1@0 | 2 | 立即放行（初始满桶） | 0 |
| k2@0 | 1 | 立即放行 | 0 |
| k3@0 | 0 | 立即放行 | 0 |
| k4@1 | 0（补1→1，扣1） | 立即放行 | 1 |
| k5@1 | 0 | 等待，队列{k5} | 2 |
| k6@4 | 1（补3→FIFO先给k5用tick2令牌→余2→自扣1） | 立即放行 | 4 |

(甲) 前三突发正确：0/0/0。若忽略初始满桶、空桶只按速率补：tick0 无令牌，错成 k1=1、k2=2、k3=3。
(乙) k5 背压等待、admitAt=2、不丢弃。若做成丢弃：Dropped()=1，最终放行总数=5（应为 6）。
(丙) k5 先到(TS=1)、k6 后到(TS=4)；正确 k5=2、k6=4，k5 先放行。LIFO：k6 到达触发补 3 令牌时栈顶先取，k6=4 先放行，k5 被挤到 4，FIFO 被破坏。

不变量（代码保证位置 / 钉住的测试）：
1. 与朴素参照一致：lim/lim.go ingest 的容量钳制递推 admit=max(TS,aPrev+ceil((1-sPrev)/r)) 与 reconcile 的 FIFO 服务后桶余额校正 / TestFeedMatchesNaiveReference
2. 背压不丢弃：lim.dropped 永不自增，Dropped() 直接读它 / TestDroppedAlwaysZero
3. 公平与单调：等待队列 append + head 指针 FIFO，放行按 seq 顺序且 at≥TS / TestFIFOAndMonotonic
4. 失败不留痕：tb.New 参数校验 + lim.Feed 整批预校验通过后才触碰状态 / TestRejectedOpsLeaveNoTrace

复杂度：未导出字段 lim.probe 记录最近一次补充后放行时检查的队列条目数 / TestHeadPointerAdmissionIsConstantTime
并发：lim.Limiter 互斥锁覆盖 Feed/Dropped，SelfCheck 每次新建实例核验四条 / TestConcurrentFeedAndReads、TestFeedVectorsAndSelfCheck
