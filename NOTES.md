# NOTES

## 一、八步推导（checkpoint 存“下一个编号” next；先持久化后发放）
| # | 操作 | 返回值 | 内存 next | checkpoint |
|---|---|---|---|---|
| 1 | Next | 0 | 1 | 1 |
| 2 | Next | 1 | 2 | 2 |
| 3 | Next | 2 | 3 | 3 |
| 4 | Next | 3 | 4 | 4 |
| 5 | SimulateCrash | — | 丢弃 | 4 |
| 6 | Recover | nil | 4 | 4 |
| 7 | Next | 4 | 5 | 5 |
| 8 | Next | 5 | 6 | 6 |

(甲) 惰性持久化（每 3 次 flush）：第 4 步后 ckpt 仅到 3，崩溃恢复后 next=3，第 7 步**重复发放 3**（3 在第 4 步已发）。
(乙) 把 next 误当“最后已分配”，Recover 写 next=ckpt+1=5，第 7 步发 5，**跳过 4（空洞）**。
(丙) 读 next→写 next+1 无锁：两 goroutine 同读到 next=k，都返回 **k（重复）**，再同写 k+1，**k+1 被跳过**（k=0 时重复 0、跳过 1）。

## 二、四条不变量：代码保证位置 / 钉住的测试函数
1. 不重复：`seq.Next` 先 `Persist(n+1)` 成功才返回 n，全程持互斥锁 —— TestEightStepSequence、TestConcurrentNext
2. 不空洞：`check.Restore` 原样读回 8 字节标量 next，`Recover` 不加不减 —— TestEightStepSequence
3. 与朴素参照一致：checkpoint 恒为 1 条 int64，恢复后 next==崩溃前已持久化值 —— TestSelfCheck、TestRestoreRecordCountConstant
4. 失败不留痕：持久化失败不推进内存/不改文件；坏 checkpoint、空 dir 整体失败且状态不变 —— TestPersistFailureLeavesNoTrace、TestCorruptRecover、TestNewInvalidDir
