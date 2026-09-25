# SpaceSaving 推导与不变量落点

## 八步分步表（k=3，流 [3,1,3,2,4,1,3,5]）

| 步 | Key | 已有? | 动作 | 替换掉谁/新 error | 事后计数器 (Key,count,error) |
|---|---|---|---|---|---|
| 1 | 3 | 否 | 加 | - | (3,1,0) |
| 2 | 1 | 否 | 加 | - | (3,1,0) (1,1,0) |
| 3 | 3 | 是 | 增 | - | (3,2,0) (1,1,0) |
| 4 | 2 | 否 | 加 | - | (3,2,0) (1,1,0) (2,1,0) |
| 5 | 4 | 否 | 替换 | 踢 (1,1,0)，error=1 | (3,2,0) (2,1,0) (4,2,1) |
| 6 | 1 | 否 | 替换 | 踢 (2,1,0)，error=1 | (3,2,0) (4,2,1) (1,2,1) |
| 7 | 3 | 是 | 增 | - | (3,3,0) (4,2,1) (1,2,1) |
| 8 | 5 | 否 | 替换 | 踢 (1,2,1)，error=2 | (3,3,0) (4,2,1) (5,3,2) |

- (甲) Query(5)=3，error=2，真实计数=1。若 error 误记为 0：count-error=3>1，违反「count-error ≤ trueCount」（误差下界）。
- (乙) 第 5 步并列 count=1（Key=1、2），按规则踢 Key=1。最终 Query(1)=0、Query(2)=0（1 在第 8 步又被踢）。若误踢 Key 最大者：第 5 步踢 2 → 第 6 步 1 已有计数器增至 (1,2,0)，第 8 步踢 4，最终 Query(1) 错成 2。
- (丙) τ=8/3≈2.67。按 count-error>τ：仅 Key=3（3>2.67；4→1、5→1 均不满足）。若只按 count>τ：多返回 Key=5，其真实计数=1。

## 四条不变量的落点

1. 不低估与误差界（被监控键 count-error ≤ true ≤ count；未监控键由不变量 2 约束）：`ss/ss.go` Add 替换分支 `count=old+1, error=old`；测试 `TestInvariants`、`TestEightStep`。
2. 未监控键 trueCount ≤ 当前最小 count：替换永远取最小 count 计数器（`heap/heap.go` Min/ReplaceMin，`ss/ss.go` Add）；测试 `TestInvariants`。
3. 容量 ≤ k 且只在满时替换、替换后 count/error 按式赋值：`ss/ss.go` Add 的三分支；测试 `TestInvariants`、`TestEightStep`。
4. 失败不留痕：`api/api.go` New/Feed 先整体校验后落状态；测试 `TestFaultInjection`。
