# 布谷鸟过滤器：十步推导与不变量

参数：numBuckets=4, entriesPerBucket=2, maxKicks=4。f(x)=1+x%7，i1=x%4，o(f)=(f%3)+1，i2=i1^o(f)。
各键：1→f2,i1=1,i2=2；5→f6,1,0；9→f3,1,0；2→f3,2,3；6→f7,2,0；10→f4,2,0；14→f1,2,0。

| # | 操作 | b0 | b1 | b2 | b3 | 结果 |
|---|---|---|---|---|---|---|
| 1 | Insert(1) | [] | [2] | [] | [] | f2 放入 b1 |
| 2 | Insert(5) | [] | [2,6] | [] | [] | f6 放入 b1 |
| 3 | Insert(9) | [3] | [2,6] | [] | [] | b1 满，f3 放入 i2=b0 |
| 4 | Insert(2) | [3] | [2,6] | [3] | [] | f3 放入 b2 |
| 5 | Insert(6) | [3] | [2,6] | [3,7] | [] | f7 放入 b2 |
| 6 | Insert(10) | [3,4] | [2,6] | [3,7] | [] | b2 满，f4 放入 i2=b0 |
| 7 | Insert(14) | [3,4] | [2,6] | [7,1] | [3] | 两桶满，踢 b2 首条目 f3→b3，f1 放入 b2 |
| 8 | Lookup(2) | [3,4] | [2,6] | [7,1] | [3] | b3 含 f3 → true |
| 9 | Delete(9) | [4] | [2,6] | [7,1] | [3] | 删 b0 中的 f3 |
| 10 | Lookup(2) | [4] | [2,6] | [7,1] | [3] | b3 含 f3 → true |

(甲) 第 7 步把指纹 f=3 从 b2 踢到 b3（alternate(2,3)=2^1=3），b2 终态 [7,1]；第 8 步 Lookup(2)=true。若踢出后丢弃受害者不重插入，b3 无 f3，Lookup(2) 错成 false。
(乙) 第 3 步 b1 已满，f(9)=3 放进第二候选桶 i2=b0。若只用 i1 一个候选桶：Insert(9) 无处可放（失败或覆盖），随后 Lookup(9) 错成 false（正确应为 true）。
(丙) f(23)=3，候选桶 {i1=3, i2=2}。朴素删除先扫 i1=b3，删掉 b3 里的 f=3——那是第 7 步被踢到 b3 的 x=2 的指纹 → 之后 Lookup(2) 错成 false。正确实现：23 不在精确插入集合，返回「删除未插入键」错误，状态不变。

## 四条不变量及其保证位置与钉住测试

1. 无假阴性：踢出的受害者必被重插入（cuck.Insert 踢出循环），失败整体回滚；钉于 TestNoFalseNegatives、TestTenStep。
2. 成员与存储一致：Lookup 只扫 i1/i2 两桶（cuck.Lookup），与朴素两桶扫描逐键一致；钉于 TestMembershipConsistent。
3. 指纹守恒：每次踢出的指纹恰好重插一次，count 随 Insert/Delete 成对增减（cuck.Insert/Delete）；钉于 TestFingerprintConservation。
4. 失败不留痕：参数/负键先校验后动状态，过滤器满恢复快照，Delete 先查精确集合（cuck.New/Insert/Delete）；钉于 TestFailureNoTrace。
