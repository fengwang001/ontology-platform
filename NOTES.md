# 对象池 NOTES

## 八步推导（maxIdle=2，空闲栈栈顶在右）

| 步 | 操作 | 返回块 / 操作后空闲列表 | Idle | Total |
|---|---|---|---|---|
| 1 | Acquire | 新建 b0，空闲 [] | 0 | 1 |
| 2 | Acquire | 新建 b1，空闲 [] | 0 | 2 |
| 3 | Acquire | 新建 b2，空闲 [] | 0 | 3 |
| 4 | Release(b0) | 空闲 [b0] | 1 | 3 |
| 5 | Release(b1) | 空闲 [b0 b1] | 2 | 3 |
| 6 | Release(b2) | 已达 maxIdle=2，回收 b2，空闲 [b0 b1] | 2 | 2 |
| 7 | Acquire | 弹栈顶得 b1，空闲 [b0] | 1 | 2 |
| 8 | Acquire | 弹栈顶得 b0，空闲 [] | 0 | 2 |

(甲) LIFO：第 7 步返回 b1、第 8 步返回 b0；若用 FIFO 则第 7 步返回 b0、第 8 步返回 b1。
(乙) 若忽略 maxIdle 把 b2 也压栈：Idle=3、Total=3；正确应为 Idle=2、Total=2。
(丙) 若不检测重复释放：b0 被压栈两次，Idle 虚计为 2 而实际只有 1 块（守恒破坏）；
随后两次 Acquire 都弹出 b0，同一块同时发给两个持有者，状态唯一性破坏、两持有者互相覆写数据。

## 四条不变量落实位置与钉住测试

1. 守恒与唯一：`blk/blk.go` 状态机迁移（InUse/Idle/Reclaimed 唯一）+ `opool/opool.go`
   在锁内维护 live 集合与 total（Acquire 增、驱逐减）。钉于 TestNaiveModelRandom、TestConcurrentNoDuplicate。
2. 与朴素参照一致：`api/api.go` SelfCheck 内置朴素栈模拟逐步比对。钉于 TestEightStepSequence、TestNaiveModelRandom。
3. 满池驱逐：`opool.Release` 用 `blk.Full` 判定后 `Reclaim` 并从 live 删除，绝不再发。钉于 TestEightStepSequence（第 6 步）、TestEviction。
4. 失败不留痕：`opool.New`/`Release` 先校验后变更，哨兵错误 ErrInvalidMaxIdle/ErrDoubleRelease/ErrUnknownBlock。钉于 TestErrorsDistinctAndAtomic。
