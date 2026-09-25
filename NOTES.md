# Object Pool NOTES（maxIdle=2，空闲栈写作 [栈底…栈顶]）

| # | 操作 | 返回块 / Release 后空闲栈 | Idle | Total |
|---|---|---|---|---|
| 1 | Acquire | b0；idle=[] | 0 | 1 |
| 2 | Acquire | b1；idle=[] | 0 | 2 |
| 3 | Acquire | b2；idle=[] | 0 | 3 |
| 4 | Release(b0) | idle=[b0] | 1 | 3 |
| 5 | Release(b1) | idle=[b0,b1]（顶=b1） | 2 | 3 |
| 6 | Release(b2) | 已满，回收 b2；idle=[b0,b1] | 2 | 2 |
| 7 | Acquire | 弹栈顶 b1；idle=[b0] | 1 | 2 |
| 8 | Acquire | 弹栈顶 b0；idle=[] | 0 | 2 |

- (甲) LIFO：第7=b1、第8=b0。若误用 FIFO：第7=b0、第8=b1。
- (乙) 忽略 maxIdle 把 b2 也压栈：Idle=3、Total=3（违反上限）；正确 Idle=2、Total=2。
- (丙) 不检测重复释放：b0 被压两次 idle=[b0,b0]，Idle 虚高、状态唯一被破坏；随后两次 Acquire 都拿到 b0，同一物理块被两个持有者同时占用。

不变量保证位置 / 钉住的测试：
1. 守恒唯一：opool.go 单一 mutex 保护 live 集合与 idle 栈，迁移只走 blk.Block 合法边 → TestConservationAndNaive（守恒），并发唯一占用由 TestAPIConcurrency 钉住。
2. 朴素参照一致：opool.go Acquire 弹栈顶、Release 压栈顶（blk.Stack 的 LIFO）→ TestConservationAndNaive（八步序列另由 TestEightSteps 钉死）。
3. 满池驱逐：opool.go Release 中 blk.AtCapacity 命中即 reclaim 并从 live 删除 → TestEightSteps（b2 终态）+ TestConservationAndNaive（回收块不再返回、Idle<=cap）。
4. 失败不留痕：opool.go Release 全部判定先于任何改动、New 先校验参数 → TestRejectedNoTrace（三类哨兵错误互不相同）。
