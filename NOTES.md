# NOTES

## 八步推导（d² 为欧氏平方距离）

| # | 操作 | 返回 | 关键 d² / 并列 |
|---|---|---|---|
| 1 | Add(0,0) | 0 | 站点 0 |
| 2 | Add(1,1) | 1 | 站点 1 |
| 3 | Add(4,0) | 2 | 站点 2 |
| 4 | Nearest(0,2) | 1 | s0=4, s1=2, s2=20，s1 最小 |
| 5 | Nearest(1,0) | 0 | s0=1, s1=1 并列，s2=9，取最小索引 0 |
| 6 | Add(0,2) | 3 | 站点 3 |
| 7 | Nearest(0,2) | 3 | s3=0，查询点即站点本身 |
| 8 | Within(1,0,1) | 0 | r²=1：s0=1、s1=1 在圆上不计，s2=9、s3=5 |

- (甲) 正确返回 1（d²=2 唯一最小）。若误用曼哈顿：s0=2、s1=2、s2=6，0/1 并列取最小索引，会错返 0。
- (乙) 正确返回 0（d² 同为 1，并列取小索引）。若用 `<=` 保留最后扫描者，会错返 1。
- (丙) 正确返回 0（s0、s1 恰在圆上，严格 `<` 排除）。若误写 `<=`，会错成 2，多算站点 0 和 1。

## 四条不变量

1. 与朴素扫描一致：`sites/sites.go` 的 `Nearest()` 网格螺旋外扩并按停搜界提前终止；`TestNaiveConsistency` 钉住。
2. 距离精确 / 严格圆内：`nbr/nbr.go` 的 `Dist2()`（int64 精确）与 `Inside()`（`d² < r²`）；`TestDist2Exact`、`TestWithinStrict` 钉住。
3. 并列稳定：`sites/sites.go` 的 `Nearest()` 在 d² 相等时显式取较小索引；`TestTieStable` 钉住。
4. 失败不留痕：`sites/sites.go` 的 `Add`（越界在加锁前、重复在 append 前判定）与 `Within`（r<0 在加锁前判定）先校验后改状态，`api` 仅透传；`TestRejectedOpsNoTrace` 与 `TestAPIRejects` 钉住。
