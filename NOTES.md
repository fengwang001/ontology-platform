# WFQ 推导与不变量

规则：`F_f=max(F_f,V)+ceil(size/w_f)`；出队取队头完成时刻最小，并列取下标小者。weights=[2,1]。

| 步 | 操作 | F0 | F1 | V | 结果 |
|---|---|---|---|---|---|
| 1 | Submit(0,2) | 1 | 0 | 0 | 入队 |
| 2 | Submit(1,1) | 1 | 1 | 0 | 入队 |
| 3 | Dequeue | 1 | 1 | 1 | 流 0 |
| 4 | Dequeue | 1 | 1 | 1 | 流 1 |
| 5 | Submit(1,2) | 1 | 3 | 1 | 入队 |
| 6 | Dequeue | 1 | 3 | 3 | 流 1 |
| 7 | Submit(0,2) | 4 | 3 | 3 | 入队 |
| 8 | Dequeue | 4 | 3 | 4 | 流 0 |

(甲) 第 7 步丢掉 V 项：F0=1+ceil(2/2)=**2**（正确为 4）。
(乙) 第 1 步不按权重缩放：F0=0+2=**2**（正确为 1）。
(丙) 第 3 步二者队头都为 1：取下标最小→**流 0**；若取最大→**流 1**。

## 四条不变量（保证位置 / 钉住测试）

1. 与朴素参照一致：`wfq.Dequeue` 直接以最小堆堆根定位（wfq.go）/ `TestNaiveAgreement`
2. 完成时刻单调、流内升序：`flow.Enqueue` 的 `max(F,V)+inc` 后尾插（flow.go）/ `TestFinishMonotone`
3. 公平性：堆序 `less` 先比 finish、相等再比流下标（wfq.go）/ `TestFairnessOrder`
4. 失败不留痕：`wfq.New`/`Submit` 与 api 包装均先校验后改状态（wfq.go, api.go）/ `TestRejectionLeavesNoTrace`

附：复杂度 probes 只记堆根检查 1 次（wfq.go）/ `TestProbeCountConstant`；并发由 api 的 mutex 串行化（api.go）/ `TestConcurrentSubmit`。
