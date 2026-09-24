# 布谷鸟过滤器：推导与不变量

参数：numBuckets=4, entriesPerBucket=2, maxKicks=4；f(x)=1+x%7；i1=x%4；o(f)=f%3+1；i2=i1^o(f)。

## 十行分步表（桶内按放入顺序）

| # | 操作 | 结果 | b0 | b1 | b2 | b3 |
|---|------|------|----|----|----|----|
| 1 | Insert(1) f=2 i1=1 | 入 b1 | - | 2 | - | - |
| 2 | Insert(5) f=6 i1=1 | 入 b1 | - | 2,6 | - | - |
| 3 | Insert(9) f=3 i1=1 满→i2=0 | 入 b0 | 3 | 2,6 | - | - |
| 4 | Insert(2) f=3 i1=2 | 入 b2 | 3 | 2,6 | 3 | - |
| 5 | Insert(6) f=7 i1=2 | 入 b2 | 3 | 2,6 | 3,7 | - |
| 6 | Insert(10) f=4 i1=2 满→i2=0 | 入 b0 | 3,4 | 2,6 | 3,7 | - |
| 7 | Insert(14) f=1 i1=2 双满 | 踢 b2 首条 f=3→b3，f=1 占其空位 | 3,4 | 2,6 | 1,7 | 3 |
| 8 | Lookup(2) f=3 i1=2 i2=3 | b3 命中 true | 3,4 | 2,6 | 1,7 | 3 |
| 9 | Delete(9) f=3 | 删 b0 中的 3 | 4 | 2,6 | 1,7 | 3 |
| 10 | Lookup(2) | b3 命中 true | 4 | 2,6 | 1,7 | 3 |

(甲) 第 7 步把指纹 3（x=2 的）从 b2 踢到 b3，f=1 占被踢空位；b2 最终 [1,7]；第 8 步 Lookup(2)=true。若踢出后丢弃受害者，b3 为空，Lookup(2) 错成 false（假阴性）。
(乙) 第 3 步 b1 已满，9 的指纹 3 放进 i2=b0。只用一个候选桶的朴素实现 Insert(9) 无处安放（报满失败），随后 Lookup(9) 错成 false。
(丙) Delete(23)：f(23)=3，候选桶 {3,2}。朴素删除在 b3 找到并删掉 x=2 的指纹 3 → Lookup(2) 错成 false。正确实现返回「删除未插入键」错误，状态不变。

## 四条不变量：保证位置与钉住测试

1. 无假阴性：cuck.Insert 踢出的受害者必重插入其 alternate 桶（hash.Alternate 对称），成功才入精确集合；测试 TestNoFalseNegatives。
2. 成员与存储一致：cuck.Lookup 只扫 i1/i2 两桶找指纹，无他径；测试 TestLookupMatchesNaive、TestCheckedBucketsConstant。
3. 指纹守恒：踢出只是移动不增删，Delete 恰好删一个指纹并同步精确集合；测试 TestFingerprintConservation。
4. 失败不留痕：Insert 满时先快照后整体回滚，Delete/New/负键先校验后动状态；测试 TestRollbackOnFull、TestRejectedOpsKeepState。
