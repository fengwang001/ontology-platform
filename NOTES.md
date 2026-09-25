# NOTES — SpaceSaving 推导与不变量

## 八步推导（k=3，事件流 [3,1,3,2,4,1,3,5]）

| 步 | Key | 已有? | 动作 | 替换掉谁/新err | 之后全部计数器 (Key,count,err) |
|---|---|---|---|---|---|
| 1 | 3 | 否 | 加 | - | (3,1,0) |
| 2 | 1 | 否 | 加 | - | (3,1,0)(1,1,0) |
| 3 | 3 | 是 | 增 | - | (3,2,0)(1,1,0) |
| 4 | 2 | 否 | 加 | - | (3,2,0)(1,1,0)(2,1,0) |
| 5 | 4 | 否 | 替换 | 汰(1,1,0)，err=1 | (3,2,0)(2,1,0)(4,2,1) |
| 6 | 1 | 否 | 替换 | 汰(2,1,0)，err=1 | (3,2,0)(4,2,1)(1,2,1) |
| 7 | 3 | 是 | 增 | - | (3,3,0)(4,2,1)(1,2,1) |
| 8 | 5 | 否 | 替换 | 汰(1,2,1)，err=2 | (3,3,0)(4,2,1)(5,3,2) |

(甲) Query(5)=3，err=2，真实计数=1。若 err 误记为 0：count-err=3-0=3 > 1=真实值，违反「count-err ≤ trueCount」（不变量1的误差下界）。
(乙) 第5步并列 min=1 的是 Key=1 与 Key=2，按规则汰 Key 最小者=1。最终 Query(1)=0、Query(2)=0。若误汰 Key 最大者：第5步汰2、第6步 1 已有计数器增为 (1,2,0)、第8步并列{1,4}汰4，最终 Query(1) 错成 2。
(丙) τ=8/3≈2.67。按 count-err>τ：仅 Key=3（3-0=3>2.67；4→1、5→1 均不满足），返回 {3}。若误用 count>τ：Key=5（count=3）也被选中，多返回 Key=5，其真实计数=1。

## 四条不变量：代码位置 / 钉住它的测试

1. 不低估（被监控键 count≥true；未监控键 Query=0 是定义，其真值由不变量2 约束）且 count-err≤true≤count：ss.Add 替换分支记 err=被汰者 count（ss/ss.go）；TestInvariantsVsNaive、TestCanonicalReplay。
2. 未监控键 trueCount ≤ 当前最小 count：ss.Add 替换语义（新 count=最小+1）保证；TestInvariantsVsNaive。
3. 容量≤k 且替换记账 count=旧+1/err=旧：ss.Add 三分支（ss/ss.go）；TestCanonicalReplay、TestInvariantsVsNaive。
4. 失败不留痕：api.New/api.Feed 先整体校验后改状态（api/api.go）；TestRejectAtomic。
