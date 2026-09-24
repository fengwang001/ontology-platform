# 水位线漂移/回拨检测器 NOTES

## 八步推导（driftThreshold=10, rollbackTolerance=3, source="s"）

| # | w | last(后) | 分类 | drift | reorder | rollback |
|---|---|----------|------|-------|---------|----------|
| 1 | 100 | 100 | Normal(首条) | 0 | 0 | 0 |
| 2 | 105 | 105 | Normal | 0 | 0 | 0 |
| 3 | 120 | 120 | Drift(120>105+10) | 1 | 0 | 0 |
| 4 | 118 | 120 | Reorder(d=2<=3) | 1 | 1 | 0 |
| 5 | 117 | 120 | Reorder(d=3<=3) | 1 | 2 | 0 |
| 6 | 116 | 120 | Rollback(d=4>3) | 1 | 2 | 1 |
| 7 | 130 | 130 | Normal(130==120+10 不超) | 1 | 2 | 1 |
| 8 | 141 | 141 | Drift(141>130+10) | 2 | 2 | 1 |

(甲) 第7步：正确判 Normal，last=130，driftCount=1。若误写 `w>=last+thr`（含等号），第7步错判 Drift，driftCount 错成 2（正确为 1）。
(乙) 第5步：正确判 Reorder，reorder=2、rollback=0。若误写 `last-w<tol`（严格小于），d=3 落入回拨，错判 Rollback，reorder 错成 1、rollback 错成 1（正确为 2、0）。
(丙) 若回拨也更新 last=116，第7步 d=130-116=14>10，错判 Drift（正确 last=120、d=10 判 Normal）。违反不变量1（last 只进不退）。

## 四条不变量：保证位置 / 钉住测试

1. last 只进不退：`drift.(*Source).Observe` 仅在 w>last 的分支赋值 last；TestMonotonicLast。
2. 分类与朴素重算一致：`wm.(*Manager).Observe` 单遍判定即朴素规则本身；TestClassifyMatchesNaive（与测试内独立重算对拍）。
3. 计数守恒：`wm.(*Manager).Observe` 按分类对对应计数器各 ++ 恰好一次；TestCountsConserved。
4. 失败不留痕：`wm.New`/`wm.(*Manager).Observe` 先校验参数、通过后才触碰任何状态；TestRejectLeavesNoTrace。
