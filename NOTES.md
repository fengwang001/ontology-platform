# 计数布隆过滤器 — 推导与不变量

m=7, k=3, maxCount=2；h1=x mod 7，h2=1+(x mod 6)。
位置：1→[1,3,5]；4→[4,2,0]；3→[3,0,4]；2→[2,5,1]。

| 步 | 操作 | 计数器 [0..6] | 结果 |
|---|---|---|---|
| 1 | Insert(1) | [0,1,0,1,0,1,0] | 成功 |
| 2 | Insert(4) | [1,1,1,1,1,1,0] | 成功 |
| 3 | Insert(3) | [2,1,1,2,2,1,0] | 成功 |
| 4 | Insert(3) | [2,1,1,2,2,1,0] | ErrOverflow，整体不生效 |
| 5 | Contains(2) | [2,1,1,2,2,1,0] | true（假阳性，位置 2,5,1 均 >0） |
| 6 | Delete(2) | [2,1,1,2,2,1,0] | ErrDeleteUninserted，状态不变 |
| 7 | Contains(1) | [2,1,1,2,2,1,0] | true |

（甲）第4步：第二次 Insert(3) 的位置 [3,0,4] 均已=2，预检溢出整条拒绝；[0]=2、[3]=2、[4]=2 不变。朴素不检查直接+1会把这三个都错成 3。
（乙）第6步：精确键计数无 2，返回 ErrDeleteUninserted，不减任何位置。朴素误信假阳性会减 [2],[5],[1]，[1]、[5] 错成 0，第7步 Contains(1)（位置1,3,5）错成 false（假阴性）。
（丙）第3步后计数器之和=9=3×3。朴素饱和法第4步后和仍=9（位置3停在2），却按4次成功插入记，应为12，差3。

## 四条不变量（保证位置 / 钉住的测试）

1. 无假阴性：cbf.go 的 Insert/Delete 成功后必增减同一组 k 个位置，Contains 判全 >0 — `TestNoFalseNegatives`
2. 计数守恒：cbf.go Insert 全成后 k 个位置各+1、Delete 各−1，拒绝路径不触碰计数器 — `TestConservation`
3. 与朴素重算一致：hash.go Positions 双重散列 + cbf.go Contains；测试用 multiset 重算全部计数器逐格比对 — `TestNaiveRecomputation`
4. 失败不留痕：hash.go New 参数校验、cbf.go Insert 先检 max 后写、Delete 先查精确键计数后写 — `TestRejectedOpsLeaveNoTrace`

SelfCheck 在内置序列上复核以上四条：`TestSelfCheck`；访问数=k 的复杂度断言：`TestVisitedCountEqualsK`；并发：`TestConcurrentContains`。
