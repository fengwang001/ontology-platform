# NOTES

## 九行分步表（W=10，B=Backfill O=Online）

| # | 事件 | 判定 | a | b | c | Seen |
|---|------|------|---|---|---|------|
| 1 | B(1,a)  | 应用       | 1 | 0 | 0 | 1 |
| 2 | B(3,b)  | 应用       | 1 | 1 | 0 | 2 |
| 3 | O(12,b) | 应用       | 1 | 2 | 0 | 3 |
| 4 | B(5,a)  | 应用       | 2 | 2 | 0 | 4 |
| 5 | O(5,a)  | 去重 no-op | 2 | 2 | 0 | 4 |
| 6 | B(7,c)  | 应用       | 2 | 2 | 1 | 5 |
| 7 | O(11,c) | 应用       | 2 | 2 | 2 | 6 |
| 8 | B(9,a)  | 应用       | 3 | 2 | 2 | 7 |
| 9 | O(3,b)  | 去重 no-op | 3 | 2 | 2 | 7 |

- (甲) `CompleteBackfill()` 后 `Backfill(2,a)` 报 `ErrBackfillClosed`，`View` 不变（a 仍 3）；漏掉完成闸门则 Seq2<W 被收下，a 错成 **4**。边界误写成 `Seq > W` 时 `Backfill(10,d)`（Seq==W）被错误收下，d 错成 **1**（正确：整批拒，d 不出现）。
- (乙) 不去重：a 收到 1,5,5,9 共 **4**（对 3）；b 收到 3,12,3 共 **3**（对 2）。
- (丙) 覆盖式切换只留回填段 {1a,3b,5a,7c,9a}：b 错成 **1**、c 错成 **1**（在线增量 O(12,b)、O(11,c) 被丢）。两流可任意交错，切换前在线段已贡献计数，故不变量 1 必须定义为「按 Seq 去重后的全序集合」计数，分段各自累加再覆盖必然丢增量。

## 四条不变量的落实位置与钉住测试

1. 与朴素参照一致：`backfill.Engine.apply` 中 `dedup.Add` 返回 true 才 `counts[key]++`（backfill.go）；测试 `TestNineStepSequence`、`TestInterleaveInvariance`。
2. 切换一致/顺序无关：`Complete` 只置 `done` 标志、不触碰计数；去重集合对两流统一。测试 `TestCutoverKeepsCounts`、`TestInterleaveInvariance`。
3. 去重幂等：`dedup.Set.Add` 对已存在 Seq 返回 false 且不写任何状态。测试 `TestNineStepSequence`（第 5、9 步即 no-op 断言）、`TestInterleaveInvariance`（含重复 Seq 的乱序重放）。
4. 失败不留痕：`Backfill`/`Online` 先整批 validate（含 W 边界、done 闸门、空 Key）再统一 mutate，校验失败零写入。测试 `TestRejectLeavesState`（含四类哨兵互不相同断言）、`TestCutoverKeepsCounts`。
