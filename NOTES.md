# 逆序对计数：归并贡献时机推导

## 归并时的贡献规则

归并已排序半段 L、R 时，逆序对必为左半 a[i] 与右半 a[j] 且 a[i] > a[j]（i 天然 < j）。
取右半 a[j] 时若左半指针停在 a[i]（a[i] > a[j]）：L 已排序，故 a[i..] 全都 > a[j]，
且下标都在 j 之前，各与 a[j] 构成一个逆序对。正确做法：累加左半剩余数量 len(L)-i。
错误做法（线上事故）：累加右半已取走数量 j；已取走元素都 <= a[j] < a[i]，与逆序对无关。

## 小例子走查：[2,4,1,3,5]，真实逆序对 3 个：(2,1),(4,1),(4,3)

归并 L=[2,4]、R=[1,3,5]：
- 取 1：a[i]=2 > 1，左半剩余 2（2,4）→ 正确 +2；错误实现 +0（j=0）。
- 取 3：a[i]=4 > 3，左半剩余 1（4）→ 正确 +1；错误实现 +1（j=1）。
- 取 5：4 <= 5，不累加。
正确算法合计 3；错误算法合计 1。两者均与朴素参照对比可分辨。

## 语义与实现位置

- 计数正确：cnt/cnt.go merge 累加左半剩余；测试 TestCountInversions
- 稳定归并：cnt/cnt.go 相等取左半（a[i] <= a[j]）；TestCountInversions「含相等元素(稳定)」
- 溢出安全：int64 计数，arr.MaxN=100000；测试 TestLargeDescendingAndBound
- 空/单元素返回 0：cnt/cnt.go CountInversions 入口；测试 TestCountInversions
- 边界（升序 0、降序 n(n-1)/2）：TestCountInversions、TestLargeDescendingAndBound
- 样例 [2,4,1,3,5]=3 与错误实现对照：TestCountInversions（内联 wrongCount）
- 比较次数上界 n·log2(n)+n：cnt 非导出计数器 comparisons；TestLargeDescendingAndBound
- 并发纯函数：TestConcurrentPure（-race）
- 哨兵错误 errors.Is：arr/arr.go ErrNilSlice/ErrOversize/ErrNegative；TestArrSentinelErrors
