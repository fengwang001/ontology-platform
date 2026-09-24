# 窗口状态复活器 NOTES

## 七步推导（T=10，id=1）

| 步 | 操作 | agg | trg | state | 本步触发器输出 |
|---|---|---|---|---|---|
| 1 | Ingest(1,7) | 7 | 0 | active | 无（7<10 未越过） |
| 2 | Ingest(1,5) | 12 | 1 | active | 1 条（7→12 从下往上越过 10） |
| 3 | Purge(1) | 12（冻结） | 1 | purged | 无 |
| 4 | Late(1,3) | 3 | 1 | revived | 无（复活不触发） |
| 5 | Late(1,8) | 11 | 1 | revived | 无（ revived 抑制触发） |
| 6 | Ingest(1,2) | 13 | 1 | revived | 无（ revived 抑制触发） |
| 7 | GC() | 13 | 1 | revived | 无；GC 不删除（revived 保留） |

- (甲) 第 4 步正确 `agg=3`（只取这一条 val）。若错误地在冻结历史上累加：第 2 步后冻结值是 12，会错成 `12+3=15`。
- (乙) 第 5 步后 `agg=11` 越过 T=10，但正确实现 `trg=1`、不产出触发器事件。若复活后未抑制、仍按 active 触发：`trg` 错成 2、多产出 1 条触发器事件，违反不变量 2（复活后抑制触发）。
- (丙) 第 7 步 revived 窗口应**保留**。若 GC 只看 purged 不看 revived 标记，会把它误删。GC 与 Late 若不互斥：同一 purged 窗口可能既被 GC 删除又被 Late 复活——Late 返回成功但窗口已消失（或删除后残留一个复活副本），最终状态自相矛盾，违反不变量 3（GC 与复活互斥）。

## 四条不变量的保证位置与钉住测试

1. 复活只服务一条：`win.Win.Revive` 直接 `agg=val`（win/win.go）；测试 `TestReviveServesSingleLate`。
2. 复活后抑制触发：`win.Win.Add` 仅 `state==active` 时判定触发（win/win.go）；测试 `TestRevivedSuppressesTriggers`。
3. GC 与复活互斥：`wmgr` 的 `Late`/`GC` 同持一把 `mu`，GC 只遍历 purged 集合且 revived 已从中移除（wmgr/wmgr.go）；测试 `TestGCKeepsRevived`、`TestConcurrentGCLate`。
4. 失败不留痕：`wmgr` 先校验（id/val/存在性/上限）再动状态（wmgr/wmgr.go）；测试 `TestErrorsDistinctAndNoTrace`。
