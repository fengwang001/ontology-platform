# 水位线漂移/回拨检测器 — 推导与不变量

阈值 drift=10、rollbackTol=3，单源 "s"；D/R/RB = drift/reorder/rollback 计数。

| 步 | w | 观测后 last | 分类（依据） | D/R/RB |
|---|---|---|---|---|
| 1 | 100 | 100 | Normal（首条无基线） | 0/0/0 |
| 2 | 105 | 105 | Normal（105≤110 且 ≥100） | 0/0/0 |
| 3 | 120 | 120 | Drift（120>105+10） | 1/0/0 |
| 4 | 118 | 120 | Reorder（d=2≤3，last 不变） | 1/1/0 |
| 5 | 117 | 120 | Reorder（d=3≤3，含等号） | 1/2/0 |
| 6 | 116 | 120 | Rollback（d=4>3，last 不变） | 1/2/1 |
| 7 | 130 | 130 | Normal（130≯120+10，含等号） | 1/2/1 |
| 8 | 141 | 141 | Drift（141>130+10） | 2/2/1 |

(甲) 第7步判 Normal、last=130、driftCount=1；若条件误写成 `w >= last+drift`，130>=130 错判 Drift，driftCount 错成 2。
(乙) 第5步判 Reorder、reorder/rollback=2/0；若误写成 `last-w < tol`（严格小于），d=3 错判 Rollback，两计数错成 1/1。
(丙) 若第6步回拨被接受为 last=116，第7步 d=130−116=14>10 错判 Drift（正确 d=10、Normal）；违反不变量 1。

不变量（保证位置 / 钉住的测试函数）：
1. 水位线单调：`drift/drift.go` Observe 仅前进分支（含首条）写 last，回退两分支不动；api TestMonotonicLast。
2. 分类与朴素重算一致：`drift/drift.go` 用当前 last 单遍判类，wm 不重扫；api TestNaiveRecompute。
3. 计数守恒：`wm/wm.go` Observe 按类别恰好累加一个计数器；api TestCountConservation。
4. 失败不留痕：`api/api.go` New/Observe 均在触达状态前校验阈值/空 source/w<0；api TestRejectedOpsLeaveNoTrace。

O(1) 由 `wm/wm.go` 非导出字段 lastReadCount 记录，wm TestObserveReadsOneHistory 钉住恒为 1；并发由 mutex 保证，wm TestConcurrentObserve 含计数单调不减断言。
