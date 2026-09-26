# Weighted Median — NOTES

## 推导：Insert 顺序 (30,3)(10,3)(40,3)(20,3)，W=12，W/2=6（实数比较）

| k | value | weight | P_k |
|---|------:|-------:|----:|
| 1 | 10 | 3 | 3 |
| 2 | 20 | 3 | 6 |
| 3 | 30 | 3 | 9 |
| 4 | 40 | 3 | 12 |

- （甲）正确：`P_k ≥ 6` 最小 k=2 → **20**。错成忽略权重的普通中位数（4 个取中间两个平均）：(20+30)/2 = **25**。
- （乙）错写成 `P_k > 6`：P_2=6 不合格，首个合格 k=3 → **30**。
- （丙）错写成 `P_k ≥ W = 12`：只有 P_4=12 合格 → **40**。

代码中以 `2*P >= W` 做精确整数比较，与 `P ≥ W/2` 实数比较等价。

## 四条不变量：保证位置 / 钉住的测试

1. 两侧权重约束：`median/median.go` 的 `Median()` 分支选择（子树和由 `wmid/treap.go` 的 `pull()` 维护）；测试 `TestSideWeightBounds`。
2. 最小性：`median/median.go` 中 `2*(acc+leftSum) >= W` 时继续向左，保证左侧前缀严格 < W/2；测试 `TestMinimality`。
3. 与朴素参照一致：`api/api.go` 的 `SelfCheck()` 内置序列逐条比对朴素排序扫描；测试 `TestNaiveAgreement`（表驱动多序列）。
4. 失败不留痕：`api/api.go` 的 `Insert` 先校验再加锁；`wmid/treap.go` 遇重复值立即原样返回、不改任何节点；测试 `TestRejectedOpsNoTrace`、哨兵错误 `TestSentinelErrors`。

复杂度：`median/median.go` 非导出字段 `steps` 记录最近一次 Median 遍历节点数；白盒测试 `median/median_test.go` 的 `TestTraversalBound` 断言 steps ≤ 2⌈log2(m)⌉+2。
