# NOTES

## 九步推导（右表初始为空；视图列只写该步受影响的 leftID）

| # | 操作 | 右表 | 反向索引 | 受影响 leftID 新视图 |
|---|------|------|----------|----------------------|
| 1 | UpsertRight(a,v1) | {a:v1} | {} | 无 |
| 2 | Left(L1,a) | {a:v1} | {a:{L1}} | L1=v1 |
| 3 | UpsertRight(b,w1) | {a:v1,b:w1} | {a:{L1}} | 无 |
| 4 | Left(L2,b) | {a:v1,b:w1} | {a:{L1},b:{L2}} | L2=w1 |
| 5 | UpsertRight(a,v2) | {a:v2,b:w1} | {a:{L1},b:{L2}} | L1=v2 |
| 6 | Left(L3,a) | {a:v2,b:w1} | {a:{L1,L3},b:{L2}} | L3=v2 |
| 7 | DeleteRight(b) | {a:v2} | {a:{L1,L3}} | L2=∅ |
| 8 | Left(L4,b) | {a:v2} | {a:{L1,L3},b:{L4}} | L4=∅ |
| 9 | UpsertRight(b,w2) | {a:v2,b:w2} | {a:{L1,L3},b:{L4}} | L4=w2 |

最终：L1=v2，L2=∅，L3=v2，L4=w2。

- (甲) 第 5 步若漏重新求值，`GetView("L1")` 错成 **v1**（正确 v2），漏的是 L1。
- (乙) 第 7 步若漏置 ∅，`GetView("L2")` 错成 **w1**（正确 ∅），漏的是 L2。
- (丙) 第 8 步 L4 当时无匹配，若漏登记反向索引，第 9 步后 `GetView("L4")` 错成 **∅**（正确 w2），漏的是 L4。不变量 1 要求任意时刻视图==用最终右表批量重算；右表日后可能补上该 key，只有无匹配也登记，补 join 才能找到 L4。

## 四条不变量的保证位置与钉住测试

1. 与批量重算一致：`rjoin.Join.Left/UpsertRight/DeleteRight` 每次变更立即重写受影响视图；测试 `TestBatchConsistency`。
2. 反向索引一致：`rjoin.Join.Left` 无条件登记（含无匹配），`DeleteRight` 仅移除该 key 条目；测试 `TestReverseIndexConsistency`。
3. 更新/删除一致：`rjoin.Join.reeval` 在 Upsert/Delete 同一临界区内遍历反向索引重写视图；测试 `TestUpdateDeletePropagation`。
4. 失败不留痕：各入口先校验（哨兵错误）再改状态；测试 `TestFailureAtomicity`。
