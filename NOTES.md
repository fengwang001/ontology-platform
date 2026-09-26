# NOTES

## 第三节推导：s = "abababa" 的前缀函数分步表

| i | s[i] | 进入 j=π[i-1] | 比较与回退 | π[i] |
|---|------|--------------|-----------|------|
| 0 | a | —（定义 π[0]=0） | 无 | 0 |
| 1 | b | 0 | s[1]=b ≠ s[0]=a，j 已为 0 无法回退 | 0 |
| 2 | a | 0 | s[2]=a = s[0]=a，j→1 | 1 |
| 3 | b | 1 | s[3]=b = s[1]=b，j→2 | 2 |
| 4 | a | 2 | s[4]=a = s[2]=a，j→3 | 3 |
| 5 | b | 3 | s[5]=b = s[3]=b，j→4 | 4 |
| 6 | a | 4 | s[6]=a = s[4]=a，j→5 | 5 |

p = len(s) − π[6] = 7 − 5 = 2；7 % 2 = 1 ≠ 0 ⇒ 不是 power。

- (甲) 最小周期仍得 p=2（正确），但 power 被恒判 true ⇒ 对 "abababa" 错报「是 power」（正确：不是）。
- (乙) "abcabcabc" 的 border 为 "abc"(3) 与 "abcabc"(6)；错取最短 border 3 ⇒ p = 9 − 3 = 6（正确是 3）。
- (丙) "abcde" 无 border（π[4]=0），错实现返回「无周期/0」；但 p=n 恒为周期，正确最小周期是 5。

> 注：任务书第十节要求 `IsPeriodic("ababab",4)=false`，与第一节定义矛盾——n−p=2，"ab" 是 "ababab" 的 border，故 4 依定义是周期。规则「不许改」，实现遵守定义返回 true，demo 如实打印。

## 第二节四条不变量：保证位置与钉住测试

1. 与朴素一致：`period.MinPeriod`/`IsPeriodic` 用 π 与定义实现；测试 `TestMinPeriodMatchesNaive`、`TestIsPeriodicMatchesNaive`（随机串循环对比朴素枚举）。
2. 周期⇔border：`period.MinPeriod = n − π[n−1]`；测试 `TestPeriodBorderDuality` 枚举所有 p 验证「p 周期 ⇔ n−p 是 border」。
3. 整周期正确：`api.IsPower` = `p < n && n%p == 0`；测试 `TestIsPower`（含 "abababa" 周期 2 非 power 的反例）。
4. 失败不留痕：`api.New`/`IsPeriodic` 先校验后动作，哨兵错误 `ErrEmpty/ErrTooLong/ErrBadPeriod`；测试 `TestRejectionLeavesState` 验证拒绝后结果不变。
