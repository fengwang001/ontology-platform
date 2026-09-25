# NOTES：端到端延迟 SLA 监控（T=10, W=4, K=3）

| 步 | 延迟 | 判定 | 最近4完成违例数 | Breached |
|---|---|---|---|---|
| 1 A: 0→10 | 10 | OK | 0 | false |
| 2 B: 20→35 | 15 | 违例 | 1 | false |
| 3 C: 40→45 | 5 | OK | 1 | false |
| 4 D: 50→61 | 11 | 违例 | 2 | false |
| 5 E: 70→82 | 12 | 违例 | 3 | true |
| 6 F: 90→94 | 4 | OK | 2 | false |
| 7 G: 100→120 | 20 | 违例 | 3 | true |

(甲) 若写成 `>=T` 即违例：A（延迟==10）被错判为违例；第4步窗口 [A违,B违,C OK,D违] 违例=3，告警**提前到第4步(D)**触发；第5步 A 已滑出窗口，窗口数值仍为 3，但触发已提前一步。
(乙) Begin(A,0) 后、End(A,10) 前：Count=0、InFlight=1；若把 Begin 当即完成，Count 会错成 1。
(丙) 第6步 B 滑出、F(OK) 滑入，近4=[C OK,D违,E违,F OK]，违例=2，应清除为 false；粘滞实现会错成 true。

## 四条不变量：保证位置与钉住测试

1. 与批量重算一致：增量统计在 `mon.(*Monitor).Complete`（count/sum/min/max/violations 与 inFlight--，mon/mon.go），在途数在 `mon.Begin`(++)/`Complete`(--)；二者仅由 `api.End` 成功完成后调用（未知 id、负延迟先返回）。测试 `TestBatchRecompute` 钉住。
2. 阈值边界精确、在途不计：`sla.Classify` 用 `latency <= threshold`（sla/sla.go）；`mon.Begin` 只增 inFlight 不碰完成统计。测试 `TestThresholdBoundary` 钉住 ==T 为 OK、T+1 违例、Begin 后 End 前 Count=0/InFlight=1。
3. 告警窗口精确：`mon.Complete` 环形缓冲 ring/head/filled + 运行计数 windowViol，O(1) 滑入滑出，`Breached` 判 `windowViol >= k`（mon/mon.go）。测试 `TestSlidingWindowBreach`、`TestRingWindowSlipOut` 与白盒 `TestRingWindowAndCheckCount` 钉住。
4. 失败不留痕：`api.Begin/End` 所有校验（重复、未知、负延迟）先于任何状态修改，返回互不相同的哨兵 `ErrDuplicateBegin/ErrUnknown/sla.ErrNegative`（api/api.go）。测试 `TestRejectedOpsLeaveNoTrace` 与 `TestSentinelErrorsDistinct` 钉住。
