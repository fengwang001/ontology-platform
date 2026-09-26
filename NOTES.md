# RR 调度器推导与不变量

## 第三节：时间片表（quantum=4；P1(arrival 0,burst 10)、P2(1,4)、P3(3,3)、P4(3,2)）

| 片 | 时间段 | 运行 | 片后剩余 | 完成（时刻） |
|----|--------|------|----------|--------------|
| 1 | [0,4)   | P1 | 6 | 否 |
| 2 | [4,8)   | P2 | 0 | 是 @8 |
| 3 | [8,11)  | P3 | 0 | 是 @11 |
| 4 | [11,13) | P4 | 0 | 是 @13 |
| 5 | [13,17) | P1 | 2 | 否 |
| 6 | [17,19) | P1 | 0 | 是 @19 |

完成时刻：P1=19，P2=8，P3=11，P4=13。

- (甲) P1 最后一次运行在 [17,19)，跑 2 个时间单位，完成时刻 19。若一律跑满 quantum=4：P3 错成 @12、P4 错成 @16，P1 末片变成 [20,24)，完成时刻错成 **24**。
- (乙) t=4 时 P1 被抢占且排在片期间新到的 P2/P3/P4 之后，正确下一个是 **P2**；若被抢占进程放回队首（LIFO），下一个会错成 **P1**（P1 将空转独占，P2/P3/P4 全部被推迟）。
- (丙) P3、P4 同在 t=3 到达，按 (arrival, pid) 升序 **P3 先、P4 后**；若按 burst 短进程优先，会错成 **P4(2) 先、P3(3) 后**。

## 第二节：四条不变量的保证位置与钉住测试

1. 与朴素参照一致：`rr/rr.go` Run 按事件整块推进；`api/api.go` naiveSim 逐 tick 独立参照，二者对拍。测试 `TestMatchesNaiveReference`。
2. 守恒：每片服务量恰为 min(remaining, quantum)，remaining 初值=burst、减到 0 才记完成（`rr/rr.go` Run 主循环）。测试 `TestConservation`。
3. 完成时刻 ≥ arrival+burst 且互不相同：完成只发生在 remaining==0 的片终点，片长 ≥1 故时间严格前进（`rr/rr.go`）。测试 `TestCompletionTimesValid`。
4. 失败不留痕：`proc/proc.go` Registry.Add 与 `rr/rr.go` New 全部先校验后写入，拒绝路径零副作用。测试 `TestRejectedOpsKeepState`。
