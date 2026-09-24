# 滑动窗口最大值：相等元素推导与不变量

## 1. [3,3,2], w=2 逐步对照（窗口 [j,j+1]，输出 3,3）

做法 A：弹出相等队尾
- i=0 推 3：[(0,3)]，输出 3
- i=1 推 3：弹出 (0,3)，队列 [(1,3)]，输出 3
- i=2 推 2：[(1,3),(2,2)]，左界移到 1，输出 3

做法 B：不弹相等（只弹严格小于）
- i=0 推 3：[(0,3)]，输出 3
- i=1 推 3：保留相等，[(0,3),(1,3)]，输出 3
- i=2 推 2：[(0,3),(1,3),(2,2)]；Evict 左界 1 淘汰 (0,3)，得 [(1,3),(2,2)]，输出 3

结论：i=1 时 (0,3) 仍在窗口 [0,1] 内，是与队首等值、在滑窗淘汰前仍可能被查询的合法候选；
做法 A 在它被左边界正常淘汰之前就将其提前丢弃，违反不变量 3（此处仅因 (1,3) 等值顶替才没暴露错值）。
选择做法 B：队尾只在“严格小于”新值时弹出，过期只由按左边界的 Evict 负责。

## 2. 不变量保证位置与钉住测试

1. 结果正确：slide.Maxes 满窗才输出，对照朴素双循环；由 TestMaxesNaive 钉住。
2. 队列单调：mono.Push 的弹出循环与 SelfCheck 保证非严格递减、队首即最大；由 TestSelfCheckInvariants 钉住。
3. 候选不早退：mono.Push 只用 `<`（保留相等，TestEqualCandidatesKept），过期仅 mono.Evict 按左界淘汰（TestExpiredCandidatesEvicted）。
4. 失败不留痕：mono.Push 与 slide.Maxes 先校验后改状态，哨兵错误 ErrBadIndex/ErrWindowNotPositive/ErrWindowTooLarge；由 TestRejectedPushLeavesNoTrace、TestSentinelErrors 钉住。

摊还复杂度：mono.ops 统计全部入队/出队，每元素至多入一次、出一次，故 ops<=2n；由 TestAmortizedOps 钉住（n=1000,100000，相等/递增/递减三形态）。
