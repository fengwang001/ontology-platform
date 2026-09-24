# FINDINGS

## 五种聚合可信度结论（值 / 标注）

| 聚合 | 全部成功 | 缺一个分片 | 缺一半分片 |
| --- | --- | --- | --- |
| Count | 精确，`= c` | 下界，`≥ c_S` | 下界，`≥ c_S`（区间更宽） |
| Sum | 精确，`= s` | 下界，`≥ s_S` | 下界，`≥ s_S`（区间更宽） |
| Min | 精确，`= m` | `m* ≤ m_S` | `m* ≤ m_S` |
| Max | 精确，`= x` | `x* ≥ x_S` | `x* ≥ x_S` |
| TopK | 精确，前缀 = K | 前缀按 `t_j > B` 判定 | 同样规则，B 更大前缀更短 |

## 故障注入检出与分片状态

| 故障 | 检出方式 | 分片状态 |
| --- | --- | --- |
| 超时 | ctx 截止，`err ≈ context.DeadlineExceeded` | Timeout |
| 损坏 | `shard.Validate`：条数不符 / OK=false | Corrupt |
| 重复返回 | 同 `(shard,seq)` 结果去重 | Duplicate |
| 全部失败 | 无 StatusOK 结果 | Failed + `ErrAllFailed` |

> 表格随各包测试完成逐步核对；sharding 层校验见 shard 包表驱动测试。
