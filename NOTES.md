# OR-Set 推导与不变量

## 十一步分步表（标签 A1/A2…=副本0 序号，B1…=副本1；元素集合格式 `元素[存活标签]`）

| 步 | 操作 | 新标签 | A 墓碑 | B 墓碑 | A 元素 | B 元素 |
|---|---|---|---|---|---|---|
| 1 | Add(A,x) | A1 | ∅ | ∅ | x[A1] | ∅ |
| 2 | Merge(B,A) | 无 | ∅ | ∅ | x[A1] | x[A1] |
| 3 | Remove(B,x) | 无 | ∅ | {A1} | x[A1] | ∅ |
| 4 | Add(A,x) | A2 | ∅ | {A1} | x[A1,A2] | ∅ |
| 5 | Add(A,y) | A3 | ∅ | {A1} | x[A1,A2] y[A3] | ∅ |
| 6 | Add(B,y) | B1 | ∅ | {A1} | x[A1,A2] y[A3] | y[B1] |
| 7 | Remove(A,y) | 无 | {A3} | {A1} | x[A1,A2] | y[B1] |
| 8 | Merge(A,B) | 无 | {A1,A3} | {A1} | x[A2] y[B1] | y[B1] |
| 9 | Merge(B,A) | 无 | {A1,A3} | {A1,A3} | x[A2] y[B1] | x[A2] y[B1] |
| 10 | Remove(B,x) | 无 | {A1,A3} | {A1,A2,A3} | x[A2] y[B1] | y[B1] |
| 11 | Merge(A,B) | 无 | {A1,A2,A3} | {A1,A2,A3} | y[B1] | y[B1] |

- (甲) 按元素名删除：第 8 步后 A 的名字墓碑 = {x,y}，A 元素集合 = ∅。丢失：x（第 4 步 Add 生成的标签 A2 被误抹）与 y（第 6 步 Add 生成的标签 B1 被误抹）。
- (乙) Merge 不合墓碑：第 11 步后 A 墓碑仍 {A1,A3}，A = {x[A1,A2], y[B1]}。被复活的是 x，它原本在第 3 步（A1 被删）与第 10 步（A2 被删）被删。按正确规则第 11 步后 A、B 状态完全相同，再 Merge(A,B) 无任何变化。
- (丙) 第 4 步挪到第 2 步前：Remove(B,x) 观察到 {A1,A2} 并全部墓碑化，第 8 步后 A = {y[B1]}。x 不再 add 胜，因为两次 Add 都发生在 Merge(B,A) 之前、都被那次 Remove 观察到，没有未被观察的并发 Add。此顺序下第 10 步 Remove(B,x) 报「元素不存在」错误，状态无任何变化。

## 四条不变量的落点

1. 朴素一致：`cluster.SyncAll` 全对全合并使各副本收敛到全量并集；`TestNaiveReference` 钉住。
2. 合并律：`orset.Set.MergeFrom` 只取并集、幂等；`TestMergeLaws` 钉住。
3. add-wins：`orset.Set.Remove` 只墓碑化本副本当前存活标签；`TestAddWins` 钉住。
4. 失败不留痕：所有校验先于变更（`Add` 先查容量再 `seq++`，`MergeFrom` 先算增量再落盘）；`TestRejectNoSideEffect` 钉住。

复杂度：`Contains`/`Remove` 只按元素索引查 `adds[e]`，非导出计数器 `checked` 记录检查条数；`TestIndexedLookup` 钉住其不随 m 增长。
