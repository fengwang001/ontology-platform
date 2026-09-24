# Window revival 推导（T=10，窗口 id=1）

| 步 | 操作 | agg | trg | state | 本步触发 | 第7步GC |
|---|---|---|---|---|---|---|
| 1 | Ingest(7) | 7 | 0 | active | 无 | — |
| 2 | Ingest(5) | 12 | 1 | active | 产出1条 | — |
| 3 | Purge(1) | 12 | 1 | purged | 无 | — |
| 4 | Late(1,3) | 3 | 1 | revived | 无 | — |
| 5 | Late(1,8) | 11 | 1 | revived | 无（抑制） | — |
| 6 | Ingest(1,2) | 13 | 1 | revived | 无（抑制） | — |
| 7 | GC() | 13 | 1 | revived | 无 | 保留，不删 |

- (甲) 第4步正确 `agg=3`：复活丢弃第2步冻结值 12，只服务本条 val=3。错误实现「在冻结历史上累加」会得 12+3=**15**。
- (乙) 第5步 agg=11 越过 10：正确 `trg=1`、**无**触发器事件（revived 永久抑制）。错误实现仍按 active 触发则 `trg=2`，多产出一条 `Late(1,8)` 越过阈值的触发器事件，违反不变量2。
- (丙) 第7步 revived 窗口必须**保留**。GC 只看 purged 不看 revived 会把已复活窗口误删，违反不变量3。若 GC 与 Late 不互斥：GC 已把该 id 判为可回收并从 map 删除，Late 同时复活——要么复活作用在已删条目上、复活结果丢失（之后 Ingest 隐式建出空 active 新窗口），要么复活后又被 GC 删除，即「既被删又复活」，违反不变量3。

## 不变量落点（代码位置 / 钉住的测试）

1. 复活只服务一条：`win/win.go` 的 `Revive` 中 `agg=val`（丢弃冻结值），由 `wmgr/wmgr.go` 的 `Late` purged 分支调用。测试 `TestReviveServesOneEvent`（api/api_test.go）。
2. 复活后抑制触发：revived 窗口只走 `win.Window.AddQuiet`（只加 agg，不碰 trg），`wmgr` 的 `Ingest`/`Late` 按 state 分派。测试 `TestRevivedSuppressesTrigger`（api/api_test.go）；七步表由 `TestSevenStepSequence` 钉住。
3. GC 与复活互斥：`wmgr.Manager` 所有操作持同一把 `mu`；复活即从 `purged` 集合删除；GC 只遍历 `purged` 集合，revived 不在其中故保留。测试 `TestGCVsLateMutualExclusion`（wmgr/wmgr_test.go）。
4. 失败不留痕：`wmgr.Ingest`/`Late` 在持锁改状态前先校验 id、val、窗口存在性、容量；哨兵错误 `ErrInvalidID`/`ErrInvalidVal`/`ErrLateNeverCreated`/`ErrTooManyWindows` 互不相同。测试 `TestRejectedOpsLeaveNoTrace`（api/api_test.go）。
5. 复杂度：非导出字段 `gcInspected` 只记录本集合检查数，不经任何导出方法暴露；测试 `TestGCInspectedCountBounded`（wmgr/wmgr_test.go）直接读同包字段，断言 m=100/1000/10000 时恒为 1。
