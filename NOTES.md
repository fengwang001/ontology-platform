# 增量快照与恢复：推导与不变量

## 第三节推导（maxKeys=8；TOMB=tombstone）
| # | 动作后活跃状态 | 本次 Checkpoint 写入 |
|---|---|---|
| 1 | {a:1,b:2,c:3} | —（尚未 Checkpoint） |
| 2 | {a:1,b:2,c:3} | base 全量：{a:1,b:2,c:3} |
| 3 | {a:10,c:3,d:4} | — |
| 4 | {a:10,c:3,d:4} | delta1：a->10、b->TOMB、d->4 |
| 5 | {a:1,c:30,d:4,e:5} | — |
| 6 | {a:1,c:30,d:4,e:5} | delta2：a->1、c->30、e->5 |
| 7 | {a:1,c:3,d:4,e:5} | — |
| 8 | {a:1,c:3,d:4,e:5} | delta3：c->3；Recover={a:1,c:3,d:4,e:5} |

甲：b 必须记 tombstone；不记则 base 里 b=2 在恢复后残留（正确：b 不存在）。
乙：a 必须记 a->1（相对上次检查点的 a=10）；相对 base 计算会漏掉 a，恢复停在 delta1 的 a=10（正确是 1）。
丙：正确 c=3；first-wins（后写不覆盖先写）下 delta2 的 c=30 挡住 delta3 的 c=3，c 错成 30。

## 第二节四条不变量：保证位置 / 钉住测试
1. 与朴素参照一致：snap/snap.go 的 Recover（base 后顺序覆盖 delta）；api/api.go 的 SelfCheck 朴素对拍。测试 TestReferenceEquivalence。
2. 覆盖合并正确：snap/snap.go Recover 中按 history 顺序的 delta 合并循环（后写覆盖、TOMB 删键）。测试 TestMergeOverwriteAndTombstone。
3. 增量最小且 tombstone 完整：snap/snap.go Checkpoint 只遍历 DrainDirty 的脏键并与 prev 比对取差。测试 TestMergeOverwriteAndTombstone、TestSectionThreeScript。
4. 失败不留痕：state/state.go Set 先判空键再判上限、通过后才写 map/脏集；snap/snap.go 无检查点时 Recover 返回 ErrNoBase。测试 TestRejectedOpsLeaveNoTrace。
