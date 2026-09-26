# WFQ 推导与不变量

## 八步推导（weights=[2,1]，inc=ceil(size/w)，F = max(F,V)+inc）

| 步 | 操作 | F_0 | F_1 | V | 该步结果 |
|---|---|---|---|---|---|
| 1 | Submit(0,2) | 1 | 0 | 0 | inc=ceil(2/2)=1，F_0=max(0,0)+1 |
| 2 | Submit(1,1) | 1 | 1 | 0 | inc=1，F_1=max(0,0)+1 |
| 3 | Dequeue | 1 | 1 | 1 | 队头 1=1 并列取下标小，返回流 0 |
| 4 | Dequeue | 1 | 1 | 1 | 仅流 1 非空，返回流 1 |
| 5 | Submit(1,2) | 1 | 3 | 1 | inc=2，F_1=max(1,1)+2 |
| 6 | Dequeue | 1 | 3 | 3 | 仅流 1 非空，返回流 1 |
| 7 | Submit(0,2) | 4 | 3 | 3 | inc=1，F_0=max(1,3)+1=4 |
| 8 | Dequeue | 4 | 3 | 4 | 仅流 0 非空，返回流 0 |

- (甲) 丢掉 `max(F,V)` 的 V 项：F_0 = 1+1 = **2**（正确值 4）。
- (乙) 不按权重缩放（inc=size=2）：F_0 = 0+2 = **2**（正确值 1）。
- (丙) 并列应返回**流 0**；若并列取下标最大则返回**流 1**。

## 四条不变量：保障位置与钉住它的测试

1. 与朴素参照一致：`wfq.Scheduler.Dequeue` 用最小堆（键=(队头完成时刻, 流下标)）定位最小，与 O(n) 扫描同序（wfq/wfq.go）；测试 `TestNaiveConsistency`。
2. 完成时刻单调：`flow.Flow.Submit` 先取 `max(F,V)` 再累加 inc，包只追加到队尾（flow/flow.go）；测试 `TestMonotonic`。
3. 公平性：`inc=ceil(size/w)` 权重缩放 + 完成时刻最小者先出队（flow/flow.go、wfq/wfq.go）；测试 `TestFairness`。
4. 失败不留痕：`New/Submit/Dequeue` 全部先校验后改状态，校验失败直接返回哨兵错误（wfq/wfq.go）；测试 `TestFailureAtomic`。
