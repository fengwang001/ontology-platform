# NOTES — 嵌套保存点与部分回滚

## 十二步推导（undoLimit=100，id 从 0 递增且不重用）

| # | 操作 | store | 活跃集合 | 结果 |
|---|---|---|---|---|
| 1 | Set("a",1) | {a:1} | ∅ | OK |
| 2 | Savepoint() | {a:1} | {0} | 返回 0 |
| 3 | Set("a",2) | {a:2} | {0} | OK |
| 4 | Savepoint() | {a:2} | {0,1} | 返回 1 |
| 5 | Set("b",5) | {a:2,b:5} | {0,1} | OK |
| 6 | Release(1) | {a:2,b:5} | {0} | OK，撤销记录全保留 |
| 7 | Set("c",8) | {a:2,b:5,c:8} | {0} | OK |
| 8 | RollbackTo(0) | {a:1} | ∅ | OK：c/b 删除、a 恢复为 1 |
| 9 | Set("b",3) | {a:1,b:3} | ∅ | OK |
| 10 | Savepoint() | {a:1,b:3} | {2} | 返回 2（不复用已释放的 1） |
| 11 | Set("c",6) | {a:1,b:3,c:6} | {2} | OK |
| 12 | RollbackTo(1) | {a:1,b:3,c:6} | {2} | ErrSavepointNotFound，状态不变 |

- (甲) 第 6 步正确结果：store={a:2,b:5}、活跃={0}，变化保留。若误把 Release 当 Rollback：会弹出 rec(b,不存在) 与标记 M1，store 错成 **{a:2}**（b:5 被错误删除）。
- (乙) 第 8 步正确结果：store=**{a:1}**（M0 之上的 c、b 删除，a 写回旧值 1；M0 之前第 1 步的 a=1 保留）。若误当「回滚到空库」：store 错成 **{}**（保存点之前的 a:1 被错误删除）。
- (丙) 第 12 步：id1 已在第 6 步释放，正确实现返回 **ErrSavepointNotFound**，store 不变仍为 **{a:1,b:3,c:6}**，活跃仍 {2}。若不校验活跃、错回滚到顶层 id2：弹出 rec(c,不存在)，**键 c（c:6）被错误删除**，store 错成 **{a:1,b:3}**。

## 四条不变量的保证位置与钉住测试

1. 部分回滚正确：`store/store.go` 的 RollbackTo 经 `pos` 索引定位 M_id，只弹出其上方撤销记录并逐条逆放（旧值写回/原不存在则删），再弹标记本身。钉住：`TestPartialRollback`。
2. 与朴素重算一致：Set 只追加「旧值+是否存在」记录、Rollback 只弹栈逆放、Release 只删标记留记录，日志即完整历史；随机操作序列对照独立朴素模型。钉住：`TestNaiveEquivalence`。
3. 释放与回滚互不干扰：Release 只压缩日志中的标记条目、绝不触碰 Record，故 store 不变；RollbackTo/Release 末尾都把 ≥id 的保存点逐出 pos 与活跃集合。钉住：`TestReleaseSemantics`。
4. 失败不留痕：四个错误分支都在任何状态变更之前 return（New 校验 limit；Set 先查空键再查容量后写；RollbackTo/Release 先查 pos 命中才动日志）。钉住：`TestRejectedOpsLeaveNoTrace`。
