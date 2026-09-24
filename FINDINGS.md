# FINDINGS

## 故障注入检出方式与分片状态

| 故障 | 检出方式 | 报告中的分片状态 |
| --- | --- | --- |
| 超时 | 分片挂起直至 ctx 截止，err 为 `context.DeadlineExceeded` | `timeout`，计入缺失清单 |
| 损坏 | `Claimed != len(Records)`，坏数据不并入 | `corrupt`，计入缺失清单 |
| 重复返回 | 同一分片 ID 出现多次，扇出按 ID 去重，首个结果生效 | 仅一个 `ok` 条目，Count/Sum 与单次一致 |
| 全部失败 | 成功分片数为 0 | 返回 `ErrAllFailed`（`errors.Is` 可判定），不出零值精确结果 |

## 五种聚合 × 缺失比例的可信度结论

| 聚合 | 全部成功 | 缺一个分片 | 缺一半分片 |
| --- | --- | --- | --- |
| Count | 精确 | 下界「≥ v」，区间 [v, +∞) | 下界「≥ v」，区间更宽 |
| Sum | 精确 | 下界「≥ v」，区间 [v, +∞) | 下界「≥ v」，区间更宽 |
| Min | 精确 | 上界「真实 Min ≤ v」，区间 [0, v] | 上界「真实 Min ≤ v」 |
| Max | 精确 | 下界「真实 Max ≥ v」，区间 [v, +∞) | 下界「真实 Max ≥ v」 |
| TopK | 精确（含排名） | 可信前缀 j = 得分 > B 的头部条数，入选确定、排名不定 | 同左，B 更大 → j 更小，可为 0 |

结论由 `report` 包测试覆盖：全部成功五种均精确；缺分片时 Count/Sum 为
LowerBound、Min 为 UpperBound、Max 为 LowerBound；TopK 在缺失上界小/大时
可信前缀分别为 K 与 0（`TestTopKTrustedPrefix` 两例逐个断言）。
