# 迟到率自适应水位线 NOTES

## 九行分步表（minDelay=2 maxDelay=10 step=2 W=3 hi=2 lo=0；初值 maxSeen=-∞ wm=-∞ delay=2）

| # | TS | 迟到? | maxSeen | wm | delay | lateCount | 结算 |
|---|----|-------|---------|----|-------|-----------|------|
| 1 | 10 | 否(wm=-∞) | 10 | 8 | 2 | 0 | - |
| 2 | 11 | 否(11≥8) | 11 | 9 | 2 | 0 | - |
| 3 | 9 | 否(9==wm,严格小于才算) | 11 | 9 | 2 | 0 | n=3,lc=0≤lo → delay=max(2,2-2)=2 |
| 4 | 5 | 是(5<9) | 11 | 9 | 2 | 1 | - |
| 5 | 6 | 是(6<9) | 11 | 9 | 2 | 2 | - |
| 6 | 15 | 否(15≥9) | 15 | 13 | 2 | 2 | n=3,lc=2≥hi → delay=min(10,2+2)=4 |
| 7 | 16 | 否(16≥13) | 16 | 12 | 4 | 0 | - |
| 8 | 17 | 否(17≥12) | 17 | 13 | 4 | 0 | - |
| 9 | 18 | 否(18≥13) | 18 | 14 | 4 | 0 | n=3,lc=0≤lo → delay=max(2,4-2)=2 |

- (甲) 滞后下限=minDelay=2。若无下限：第3步结算 delay 错降到 0；第9步结算后错停在 0（正确应为 2）。
- (乙) 第3步 TS=9==wm，按「严格小于」判为**正常**。若误写成 TS<=wm，第3步被错判迟到，窗口1 的 lateCount 由 0 错成 1。
- (丙) 只增不减时，第6步结算上调到 4 后永不回落，第9步后错停在 4（正确应为 2），违反不变量3（收敛：连续 lc≤lo 的窗口应使 delay 回落到 minDelay）。

## 四条不变量：保证位置与钉住它的测试

1. 固定 delay 与朴素一致：`wmline.Line.Observe` 先用旧 wm 判定再更新 maxSeen/wm（wmline/wmline.go）；测试 `TestFixedDelayEquiv`（api/api_test.go）。
2. delay 始终在界内：`wmline.Line.SetDelay` 钳制 [minDelay,maxDelay]（wmline/wmline.go）；测试 `TestDelayBounds`。
3. 收敛：`adapt.Controller.settle` 双向单调步进 ±step（adapt/adapt.go）；测试 `TestConverge`。
4. 失败不留痕：`api.New` 先校验全部参数、通过才建任何状态（api/api.go）；测试 `TestRejectNoSideEffect`。
