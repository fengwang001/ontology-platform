# 迟到率自适应水位线：推导与不变量

参数 min=2 max=10 step=2 W=3 hi=2 lo=0；初值 maxSeen=wm=-∞, delay=2。lc=结算前窗口迟到计数，delay 为结算后值。

| 步 | TS | 迟到 | maxSeen | wm | delay | lc | 结算 |
|---|---|---|---|---|---|---|---|
| 1 | 10 | 否 | 10 | 8 | 2 | 0 | — |
| 2 | 11 | 否 | 11 | 9 | 2 | 0 | — |
| 3 | 9 | 否(9==wm) | 11 | 9 | 2 | 0 | ↓触发，触底仍 2 |
| 4 | 5 | 是 | 11 | 9 | 2 | 1 | — |
| 5 | 6 | 是 | 11 | 9 | 2 | 2 | — |
| 6 | 15 | 否 | 15 | 13 | 4 | 2 | ↑ 2→4 |
| 7 | 16 | 否 | 16 | 12 | 4 | 0 | — |
| 8 | 17 | 否 | 17 | 13 | 4 | 0 | — |
| 9 | 18 | 否 | 18 | 14 | 2 | 0 | ↓ 4→2 |

（甲）滞后下限 = 2。无下限：第 3 步 delay 错降到 0；之后第 6 步升到 2、第 9 步又降，最终错成 0；正确应为 delay=2（wm=14）。
（乙）严格小于：9==wm 判「正常」。误写 `<=`：第 3 步错判迟到，窗口 1 的 lateCount 从 0 错成 1，落入 (0,2) 开区间反而不结算、掩盖异常。
（丙）只增不减：第 6 步 2→4 后第 9 步无法回落，最终错停在 4（正确 2）；违反不变量 3（双向单调收敛，须能逐步回落 minDelay）。

## 不变量：代码保证位置 / 钉测测试

1. 与固定水位线逐条一致：`wmline.(*Line).Observe` 先按旧 wm 判迟到再推进 maxSeen/wm，中性窗口 `adapt.(*Window).settle` 不改 delay；`TestFixedDelayMatchesNaive` 钉。
2. delay 恒在 [minDelay,maxDelay]：`adapt.(*Window).settle` 上调 min(maxDelay,…)、下调 max(minDelay,…) 双向夹逼，`adapt.New` 先拒非法参数；`TestDelayBounded` 钉。
3. 收敛：settle 每窗口只走一个 step、双向单调，上调后连续 lateCount<=lo 窗口必回落到 minDelay；`TestConvergence` 钉。
4. 失败不留痕：`adapt.New` 先做五项哨兵错误校验、全部通过才构造状态，`api.New` 原样上抛；`TestRejectionsStateUntouched` 钉。
