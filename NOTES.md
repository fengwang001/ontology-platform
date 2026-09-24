# NOTES

## 十二步推导（undoLimit=100，格式：store | 活跃保存点 | 结果）

1. `Set("a",1)`        → `{a:1}` | `{}` | OK
2. `Savepoint()`       → `{a:1}` | `{0}` | OK，id=0
3. `Set("a",2)`        → `{a:2}` | `{0}` | OK
4. `Savepoint()`       → `{a:2}` | `{0,1}` | OK，id=1
5. `Set("b",5)`        → `{a:2,b:5}` | `{0,1}` | OK
6. `Release(1)`        → `{a:2,b:5}` | `{0}` | OK，只删标记不撤销
7. `Set("c",8)`        → `{a:2,b:5,c:8}` | `{0}` | OK
8. `RollbackTo(0)`     → `{a:1}` | `{}` | OK，弹到 id0 标记
9. `Set("b",3)`        → `{a:1,b:3}` | `{}` | OK，回滚后可继续
10. `Savepoint()`      → `{a:1,b:3}` | `{2}` | OK，id=2（不重用 1）
11. `Set("c",6)`       → `{a:1,b:3,c:6}` | `{2}` | OK
12. `RollbackTo(1)`    → `{a:1,b:3,c:6}` | `{2}` | ErrSavepoint，状态不变

（甲）第 6 步：正确为 `{a:2,b:5}`、活跃 `{0}`；若把 Release 错当 Rollback，会撤销
`Set b` 与 `Set a=2`，store 错成 `{a:1}`。
（乙）第 8 步：正确为 `{a:1}`（第 1 步的 a=1 保留，b/c 删除、a 还原）；若错当「回滚到
空库」，store 错成 `{}`，a=1 被误删。
（丙）第 12 步：id1 已释放，正确返回 ErrSavepoint、store 不变 `{a:1,b:3,c:6}`；若不校验
活跃性而错回滚到顶层 id2，会撤销 `Set c=6`（删 c），store 错成 `{a:1,b:3}`。

## 四条不变量（代码保证位置 / 钉住的测试）

1. 部分回滚正确：`store.(*Store).RollbackTo` 依 `pos[id]` 索引逐条弹到该标记；TestTwelveStepTrace
2. 与朴素重算一致：`api.(*KV).SelfCheck` 内置朴素模型逐拍对比，随机序列见 TestNaiveReplay
3. 释放/回滚互不干扰：`store.(*Store).Release` 只删标记+`undo.Active.Drop`，不碰 store；TestReleaseRollbackIndependence
4. 失败不留痕：store.New/Set/RollbackTo/Release 全部先校验后改状态；TestSentinelErrors、TestRejectedLeavesState
