# 蒙特卡洛积分 NOTES

## 一、推导（f(x)=x², [a,b]=[0,2], 采样点 [0.5, 1.5, 1.0, 0.0]）

| 步 | x | f(x)=x² | 累计 sum | n |
|---|---|---|---|---|
| 1 | 0.5 | 0.25 | 0.25 | 1 |
| 2 | 1.5 | 2.25 | 2.50 | 2 |
| 3 | 1.0 | 1.00 | 3.50 | 3 |
| 4 | 0.0 | 0.00 | 3.50 | 4 |

精确积分 ∫₀² x² dx = 8/3 ≈ 2.6667。

- (甲) 正确 Estimate = (b−a)·sum/n = 2·3.5/4 = **1.75**。漏除 n：`(b−a)·sum` = 2·3.5 = **7.0**（错）。
- (乙) 漏乘 (b−a)：`sum/n` = 3.5/4 = **0.875**（错）。
- (丙) 递推 avg=(avg+f)/2：0.125 → 1.1875 → 1.09375 → 0.546875；Estimate=(b−a)·avg = 2·0.546875 = **1.09375**（错，最近点权重 1/2，最早点仅 1/16）。

## 二、四条不变量的保证位置与钉住测试

1. 常数函数精确：Estimate 公式 `(b−a)·sum/n` 中常数 c 的 sum=n·c 精确相消（`mc/mc.go` Estimate）；测试 `TestConstantExact`。
2. 与朴素参照一致：Add 只做 `sum+=f(x); n++`，与批量重放同序同值（`mc/mc.go` Add）；测试 `TestMatchesNaiveReplay`。
3. 零宽区间：a==b 时 (b−a)=0 乘任何有限均值都得 0（`mc/mc.go` Estimate）；测试 `TestZeroWidthInterval`。
4. 失败不留痕：所有校验先于状态修改，拒绝路径不触碰 sum/n（`api/api.go` New/Add、`mc/mc.go` Add/Estimate）；测试 `TestRejectionLeavesNoTrace`。
