# NOTES

## 1. s="abababa" 的前缀函数递推（n=7）

| i | s[i] | 进入 j=π[i-1] | 比较与回退 | π[i] |
|---|---|---|---|---|
| 0 | a | — | 约定 π[0]=0 | 0 |
| 1 | b | 0 | s[1]=b ≠ s[0]=a，j=0 不再回退 | 0 |
| 2 | a | 0 | s[2]=a = s[0]=a，j←1 | 1 |
| 3 | b | 1 | s[3]=b = s[1]=b，j←2 | 2 |
| 4 | a | 2 | s[4]=a = s[2]=a，j←3 | 3 |
| 5 | b | 3 | s[5]=b = s[3]=b，j←4 | 4 |
| 6 | a | 4 | s[6]=a = s[4]=a，j←5 | 5 |

p = n − π[6] = 7 − 5 = **2**；7 % 2 = 1 ≠ 0，故 **不是 power**。

- (甲) 漏查 `n%p==0`：p=2 仍对，但会误判 power=true（"ab"^3="ababab" ≠ "abababa"，长度都对不上）。
- (乙) "abcabcabc"（n=9）：border 链 6→3→0；取最短 border 3 得 p=9−3=**6**，错——那是最大周期，最小周期是 9−6=**3**。
- (丙) "abcde"：π[4]=0，误报"无周期/0"；正确 p=5−0=**5**（p=n 恒为周期）。

## 2. 四条不变量：代码保证位置 / 钉住的测试

1. 与朴素一致：`period.Periods` 的 `n-π[n-1]`、`period.IsPeriodic` 按定义逐位比较；`TestNaiveConsistency`
2. 周期 ⇔ border：`pfx.Build` 算 π，`period` 沿 π 链枚举 border 双向核验；`TestPeriodBorderEquivalence`
3. 整周期正确：`period.IsPower` 要求 `p<n` 且 `n%p==0`（含 "abababa" 反例）；`TestPowerSemantics`
4. 失败不留痕：`api.New` 先校验后赋值、`api.IsPeriodic` 先判越界，哨兵错误；`TestRejectedOpsLeaveNoTrace`

另：线性比较计数在 `pfx` 非导出字段，`TestComparisonCountLinear` 钉住 ≤2n；并发只读由 `TestConcurrentReaders` 钉住。
