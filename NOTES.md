# NOTES

## 八步推导（A0 = [5,2,8,1,9,3,7,4]）

| 步 | 操作 | 结果 |
|---|---|---|
| 1 | Query(0,8) | 1 |
| 2 | Update(0,10) | A=[10,2,8,1,9,3,7,4] |
| 3 | Query(0,4) | 1 |
| 4 | Update(3,11) | A=[10,2,8,11,9,3,7,4] |
| 5 | Query(0,4) | 2 |
| 6 | Query(4,8) | 3 |
| 7 | Update(2,0) | A=[10,2,0,11,9,3,7,4] |
| 8 | Query(0,8) | 0 |

(甲) 误当闭区间 [0,2] 会把 A[2]=0 算入，错得 0；正确半开 [0,2)=min(10,2)=2。
(乙) 只改叶子不重算祖先：覆盖 [0,4) 的内部节点仍是旧的 1，第 5 步错得陈旧值 1；正确值 2。
(丙) Query(3,3) 空区间应返回 +Inf(MaxInt64)；错当「返回 A[3]」会得 11。非 2 的幂时补齐叶子若填 0（或任何非 +Inf 值），Query(0,n) 会把该填充值纳入取 min，正数数组会错得 0，污染一切覆盖补齐区的区间。

## 四条不变量 → 代码位置 → 钉住它的测试

1. 与朴素参照一致：rmq.Query 迭代只下潜与 [l,r) 相交的节点，单位元 seg.Inf 起累加；TestNaiveConsistency。
2. 更新后仍正确：rmq.Update 改叶子后沿父链重算到根；TestUpdateConsistency（逐条核对全部 (l,r)）。
3. 单位元无污染：rmq.Build 把 [n,size) 的补齐叶子显式填 seg.Inf；TestPaddingNoPollution。
4. 失败不留痕：api.New/Query/Update 先校验（四类哨兵错误）再触碰树，校验失败直接返回；TestRejectedNoSideEffect。
