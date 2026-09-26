# NOTES — 蒙特卡洛积分推导与不变量

## 一、分步推导：f(x)=x²，[a,b]=[0,2]，采样点 [0.5, 1.5, 1.0, 0.0]

| 步 | x | f(x)=x² | 累计 sum | n |
|----|-----|---------|----------|---|
| 1 | 0.5 | 0.25 | 0.25 | 1 |
| 2 | 1.5 | 2.25 | 2.50 | 2 |
| 3 | 1.0 | 1.00 | 3.50 | 3 |
| 4 | 0.0 | 0.00 | 3.50 | 4 |

精确积分 ∫₀²x²dx = 8/3 ≈ 2.6667；采样均值 sum/n = 0.875。

- (甲) 正确 Estimate = (b−a)·sum/n = 2·3.5/4 = **1.75**；漏除 n 得 (b−a)·sum = 2·3.5 = **7.0**。
- (乙) 漏乘 (b−a) 得 sum/n = 3.5/4 = **0.875**。
- (丙) 递推 avg=(avg+f(x))/2：0.125 → 1.1875 → 1.09375 → 0.546875；
  Estimate = 2·0.546875 = **1.09375**（最近点权重 1/2，最早点仅 1/16，偏离等权均值 0.875）。

## 二、四条不变量：保证位置与钉住测试

1. 常数函数精确：Estimate 只做 `(b-a)*sum/n`（mc/mc.go Estimate），sum=n·c 故恒等于 c·(b−a)；测试 `TestConstantFunctionExact`。
2. 与朴素参照一致：Add 只累加 `sum += f(x)`、n++（mc/mc.go Add），与批量重放同序同值；测试 `TestMatchesNaiveReplay`。
3. 零宽区间：a==b 时因子 (b−a)=0，Estimate 恒 0（mc/mc.go Estimate）；测试 `TestZeroWidthInterval`。
4. 失败不留痕：New 先校验再构造、Add 先校验越界再改状态、Estimate 只读（mc/mc.go New/Add/Estimate）；测试 `TestRejectedOpsLeaveNoTrace`。
