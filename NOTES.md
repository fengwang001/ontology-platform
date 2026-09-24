# NOTES

## 第三节：maxKeys=8 八行分步表
1. Set(a,1),Set(b,2),Set(c,3) | 活跃 {a:1 b:2 c:3}
2. Checkpoint①                | base = {a:1 b:2 c:3}
3. a=10; Delete(b); Set(d,4)  | 活跃 {a:10 c:3 d:4}
4. Checkpoint② delta          | a→10, b→TOMB, d→4（c 未变，不出现）
5. a=1; c=30; e=5             | 活跃 {a:1 c:30 d:4 e:5}
6. Checkpoint③ delta          | a→1, c→30, e→5（b 已删，d 未变，不出现）
7. Set(c,3)                   | 活跃 {a:1 c:3 d:4 e:5}
8. Checkpoint④ delta          | c→3；Recover = {a:1 c:3 d:4 e:5}

(甲) b 必须记 tombstone；若 delta 不记 tombstone，base 的 b=2 残留（正确：b 不存在）。
(乙) delta 相对上一检查点（a=10），必须记 a→1；若相对 base 计算会漏掉 a，恢复得 a=10（正确 1）。
(丙) 正确 c=3；first-wins 合并时 delta③ 的 c=30 挡住 delta④ 的 c=3，恢复得 c=30（正确 3）。

## 第二节四条不变量：保证位置 + 钉住的测试
1. 与朴素参照一致：snap.(*Store).Recover 按检查点顺序覆盖（snap/snap.go）；TestReferenceEquivalence（api/api_test.go，随机表驱动）。
2. 覆盖合并=重放：Checkpoint 用 last 增量同步、Recover 后写覆盖先写+tomb 删键；TestMergeMatchesReplay（snap/snap_test.go）。
3. 增量最小且 tombstone 完整：Checkpoint 只遍历 dirty 键并与 last 比对，删除记 Change{Tomb:true}；TestDeltaMinimalAndTombs、TestDeltaTraversalIsDirtyBound（snap/snap_test.go）。
4. 失败不留痕：api.Set 在任何改写前判空键/超上限（api/api.go），无 base 时 snap.Recover 返回 ErrNoBase；TestRejectedOpsLeaveNoTrace、TestSentinelErrors（api/api_test.go）。
