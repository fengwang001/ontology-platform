# 写者优先读写锁 — 推导笔记

## 第三节：八步分步表

记「读者」为当前持有读锁的集合，「写者」为当前持有写锁者（- 表示无），「队列」为等待队列（FIFO，左为队首）。

| 步 | 操作 | 读者 | 写者 | 等待队列 | 结果 |
|---|---|---|---|---|---|
| 1 | AcquireRead(R1) | {R1} | - | [] | 进入（无写者持有/等待） |
| 2 | AcquireRead(R2) | {R1,R2} | - | [] | 进入（同上） |
| 3 | AcquireWrite(W1) | {R1,R2} | - | [W1] | 等待（有读者持有） |
| 4 | AcquireRead(R3) | {R1,R2} | - | [W1,R3] | 等待（写者优先：有写者等待） |
| 5 | ReleaseRead(R1) | {R2} | - | [W1,R3] | 释放；W1 仍被 R2 阻塞，不放行 |
| 6 | ReleaseRead(R2) | {} | W1 | [R3] | 释放；读者清零，放行队首写者 W1 |
| 7 | AcquireRead(R4) | {} | W1 | [R3,R4] | 等待（写者持有，读写互斥） |
| 8 | ReleaseWrite(W1) | {R3,R4} | - | [] | 释放；无写者等待，按 FIFO 放行 R3、R4 |

**(甲)** 若只查「无写者持有」、不查「无写者等待」（读者优先），第 4 步 R3 会立即进入。此时只要读者源源不断，「无读者持有」永远不满足，W1 永远拿不到写锁——写者饿死。写者优先标志正是为堵住这个口子。
**(乙)** 正确结果：W1 进入等待队列（R1、R2 仍在读，写者授予条件不满足）。若实现允许写者与读者同时持有，违反第二节不变量第 1 条（互斥：读写不得同时持有）。
**(丙)** 第 8 步后队列中 R3、R4 均为读者且无写者等待，二者都可进入；FIFO 下先放行 R3、后放行 R4（二者同时持有读锁）。若用 LIFO 则先 R4 后 R3——顺序不同但都能进，本题规则取 FIFO。

## 第二节：四条不变量落点

1. **互斥**：`rw.State.CanRead/CanWrite` 的授予条件（rw/rw.go），`lock.drain` 一次至多放行一个写者且放行写者时读者已清零（lock/lock.go）。测试：`TestEightStepTrace`、`TestConcurrentMutualExclusion`。
2. **写者优先**：`rw.CanRead` 检查 `waitingWriters` 标志，有写者等待即拒新读者（rw/rw.go）。测试：`TestWriterPreference`、`TestEightStepTrace` 第 4 步。
3. **与朴素参照一致**：api 内未导出 `model` 按规则逐步手推同一随机序列，逐步比对持有者与放行结果（api/api.go）。测试：`TestReferenceModelRandom`。
4. **失败不留痕**：lock 先做全部校验（`ErrEmptyID`/`ErrNotHeld`/`ErrDoubleRelease`）再改状态，拒绝路径零写入（lock/lock.go）。测试：`TestFaultInjection`。

复杂度：`CanRead/CanWrite` 只读 `waitingWriters` 聚合标志，非导出计数器 `checked` 恒为 1，不随等待写者数 m 增长。测试：rw 包内 `TestGrantCheckIsConstant`（m=100/1000/10000）。
