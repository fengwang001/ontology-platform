# EWMA 推导（alpha=0.25）与不变量

| 步 | 操作后 hist | EWMA |
|---|---|---|
| 1 Add(10) | [10] | 10 |
| 2 Add(20) | [10 20] | 12.5 |
| 3 Add(10) | [10 20 10] | 11.875 |
| 4 Add(30) | [10 20 10 30] | 16.40625 |
| 5 Retract(10) | [10 20 30] | 16.875 |
| 6 Add(40) | [10 20 30 40] | 22.65625 |
| 7 Retract(20) | [10 30 40] | 21.25 |
| 8 Add(50) | [10 30 40 50] | 28.4375 |

(甲) 步1正确 EWMA=10（首值直取）；若 0 种子则 .25*10+.75*0=2.5。
(乙) 步5正确重算=16.875；反向一步 (16.40625-.25*10)/.75=18.541666…（≈18.54167）。
(丙) 步5若误删最早的 10（索引0），hist=[20 10 30]：20→17.5→20.625。

不变量（保证位置 / 钉住的测试）：
1. 与批量重算一致：ema/ema.go 的 recompute（Retract 调用）与 Add 增量式（同用 math.FMA）；TestInvariantBatchRecompute。
2. 初始化无 0 种子：ema/ema.go 的 Add 中 !init 分支直取首值；TestInvariantInitialization。
3. 撤回精确：ema/ema.go 的 Retract 删尾匹配项后整体 recompute；TestInvariantRetractExact。
4. 失败不留痕：ema/ema.go 的 New 先校验 alpha、Retract 先判空/查找再改写；TestInvariantFailureLeavesNoTrace。
