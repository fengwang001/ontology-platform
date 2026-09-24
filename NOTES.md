# OR-Set 推导与不变量

## 十一步分步表（A=副本0，B=副本1；元素后[]内为存活标签；墓碑“-”为空）

| 步 | 新标签 | A墓碑 | B墓碑 | A元素 | B元素 |
|---|---|---|---|---|---|
| 1 Add(A,x) | A1 | - | - | x[A1] | - |
| 2 Merge(B,A) | 无 | - | - | x[A1] | x[A1] |
| 3 Remove(B,x) | 无 | - | A1 | x[A1] | - |
| 4 Add(A,x) | A2 | - | A1 | x[A1,A2] | - |
| 5 Add(A,y) | A3 | - | A1 | x[A1,A2] y[A3] | - |
| 6 Add(B,y) | B1 | - | A1 | x[A1,A2] y[A3] | y[B1] |
| 7 Remove(A,y) | 无 | A3 | A1 | x[A1,A2] | y[B1] |
| 8 Merge(A,B) | 无 | A1,A3 | A1 | x[A2] y[B1] | y[B1] |
| 9 Merge(B,A) | 无 | A1,A3 | A1,A3 | x[A2] y[B1] | x[A2] y[B1] |
| 10 Remove(B,x) | 无 | A1,A3 | A1,A2,A3 | x[A2] y[B1] | y[B1] |
| 11 Merge(A,B) | 无 | A1,A2,A3 | A1,A2,A3 | y[B1] | y[B1] |

(甲) 按元素名删除：第3步B墓碑={x}、第7步A墓碑={y}，第8步合并后A墓碑={x,y}，A元素集=空集（正确结果为{x[A2], y[B1]}）。丢x：第4步Add生成的A2被第3步的名字墓碑x误杀；丢y：第6步Add生成的B1被第7步的名字墓碑y误杀。
(乙) 不合并墓碑：A的墓碑始终只有{A3}，第11步后A={x[A1,A2], y[B1]}，x的存活标签为A1、A2。被复活的是x，它曾在第3步（B墓碑化A1）和第10步（B墓碑化A2）被删，两块墓碑都没传到A。按正确规则第11步后A、B状态已完全一致，再执行Merge(A,B)无任何变化。
(丙) 新顺序：Add(A,x)得A1、Add(A,x)得A2、Merge(B,A)、Remove(B,x)时B已观察到A1、A2并全部墓碑化；第8步后A={y[B1]}。x不再add胜：两次Add都先于Remove被观察到，删除与Add不并发，无“未观察到的Add”可胜。第10步Remove(B,x)：x在B无存活标签，拒绝（ErrNotFound），状态无任何变化。

## 四条不变量 → 代码位置 → 钉住它的测试

1. 与朴素参照一致：orset.Add/Remove/Merge 的集合语义 + cluster.SyncAll 全量两两并集 → TestNaiveConsistency
2. 合并律：orset.Merge 纯集合并集（先算容量再落盘） → TestMergeLaws
3. add-wins：orset.Remove 只墓碑化 live() 算出的本副本当前存活标签 → TestAddWins
4. 失败不留痕：所有校验先于任何变更，Merge 计数通过后才并集 → TestFailuresLeaveNoTrace
