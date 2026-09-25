# NOTES — end-to-end latency SLA monitoring

推导：T=10, W=4, K=3（窗口内违例数 >= K 即 Breached）
| End 后 | 延迟 | 判定 | 近 4 完成违例数 | Breached |
|---|---|---|---|---|
| A | 10 | OK(==T) | 0 | false |
| B | 15 | 违例 | 1 | false |
| C | 5  | OK | 1 | false |
| D | 11 | 违例 | 2 | false |
| E | 12 | 违例 | 3 (B,D,E) | true |
| F | 4  | OK | 2 (D,E) | false |
| G | 20 | 违例 | 3 (D,E,G) | true |

(甲) 若误写成 `>=T` 违例：A(==10) 被错判违例；第 4 步 D 的窗口 A/B/C/D 违例=3，告警**提前到 D 即触发**（正确为 false）。E 步窗口已不含 A，E 计数仍为 3，但告警已提前一步，属多算 A 一次。
(乙) Begin(A,0) 后、End(A,10) 前：Count=0、InFlight=1；若 Begin 即计完成（或以 当前ts-begin 入账），Count 错成 1。
(丙) F 延迟 4 为 OK，滑入后窗口违例 2<3，正确 Breached=**false**（告警不粘滞）；若做成触发后保持，第 6 步后错成 true。

不变量 — 保证位置 / 钉住的测试：
1. 与批量重算一致：`mon.Complete` 全量累计 count/viol/min/max/sum，判定只经 `sla.Classify`；`api.End` 合法后才调用它 —— api_test.go `TestBatchRecompute`
2. 阈值边界 ==T 为 OK：`sla.Classify` 用 `latency <= threshold` —— api_test.go `TestThresholdBoundary`
3. 告警窗口精确：`mon.pushWindow` 环形缓冲 + `windowViolations` 运行计数，O(1) 滑入滑出 —— api_test.go `TestSevenStepSequence`；mon_test.go `TestSlidingWindow`
4. 失败不留痕：`api.New/Begin/End` 的错误返回全部先于任何状态写入，负延迟在 delete/Complete 之前返回 —— api_test.go `TestRejectedOpsAtomic`
