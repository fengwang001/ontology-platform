# NOTES

## 第三节推导：a="abc", b="yabd", k=2（n=3, m=4）

`dp[i][j]` = `a[0..i)` 变 `b[0..j)` 的最小代价，b 的列为 `y a b d`：

| i | a[i-1] | dp[i][0] | dp[i][1] | dp[i][2] | dp[i][3] | dp[i][4] |
|---|--------|----------|----------|----------|----------|----------|
| 0 | —      | 0        | 1        | 2        | 3        | 4        |
| 1 | a      | 1        | 1        | 1        | 2        | 3        |
| 2 | b      | 2        | 2        | 2        | 1        | 2        |
| 3 | c      | 3        | 3        | 3        | 2        | 2        |

逐行示例（i=1）：`dp[1][1]`：'a'≠'y' → min(0+1,1+1,0+1)=1；`dp[1][2]`：'a'='a' → min(2+1,1+1,1+0)=1。
答案 `dp[3][4]=2`（插入 'y' + 替换 'c'→'d'）。

- **(甲)** 替换代价错定为 2：重算得 `dp[3][4]=3`（"插 y + 换 c→d" 变 1+2=3，算法改走"插 y、删 c、插 d"=3）。**错值 3**。
- **(乙)** 边界全初始化为 0：第 0 行/列全 0，前缀删除不再计费，重算得 `dp[3][4]=1`。**错值 1**。
- **(丙)** 错读 `dp[2][3]`（"ab"→"yab"）：查上表得 **1**（只插入 'y'），漏算了 'c'→'d' 的替换。**错值 1**。

## 第二节四条不变量的保证位置与钉住它们的测试

1. **与朴素一致**：`dist/dist.go` 中 band 只把 `|i-j|>k` 的单元当作 >k（inf=k+1），带内转移与朴素 DP 逐格相同；测试 `TestDistanceMatchesNaive`（dist/dist_test.go，随机串对对比无剪枝全表）。
2. **脚本有效**：`ops/ops.go` 从 `dp[n][m]` 沿等值前驱回溯，脚本长度=距离；`ops.Apply` 重放校验；测试 `TestEditScriptValid`（api/api_test.go）。
3. **上限正确**：`dist/dist.go` 末尾 `d>k → ErrExceedsCap`，且 `|n-m|>k` 提前判定；测试 `TestCapBoundary`（dist/dist_test.go，对每个 k 验证 iff）。
4. **失败不留痕**：`api/api.go` 的 `New`/`Distance`/`EditScript` 先校验后计算，`Calculator` 唯一的字段 k 创建后永不写入；测试 `TestRejectionKeepsState`（api/api_test.go，三类拒绝后结果与拒绝前逐对相同）。
