# NOTES

## 系数回代推导（需求第三节）

欧几里得一步：a = b·q + r，其中 q = a/b，r = a mod b，故 gcd(a,b) = gcd(b,r)。
设递归层已返回 x'、y'，满足 b·x' + r·y' = g。把 r = a − b·q 代入：

    g = b·x' + (a − b·q)·y' = a·y' + b·(x' − q·y')

故本层必须取 x = y'，y = x' − (a/b)·y'。直接对调（x=x', y=y'）得
a·x' + b·y'，一般 ≠ g；漏掉 (a/b)·y' 项则 a·x + b·y = g + b·q·y'，
仅当 q·y' = 0 侥幸成立。两种错法都破坏恒等式；互质时 x 不再是逆元。
实例 (240,46)：正确 x=−9, y=47，240·(−9)+46·47 = 2；漏项实现返回
x=0, y=1，240·0+46·1 = 46 ≠ 2。

## 复杂度上界（需求第四节）

每两轮迭代 max(a,b) 至少减半，递归层数 ≤ 2·log2(max(a,b))+2。egcd 用
非导出计数器（指针穿透递归，无共享状态）统计层数，测试断言该上界。

## 语义与代码位置

- 贝祖恒等式：egcd/egcd.go ExtendedGCD；TestExtendedGCDTable、TestBezout240_46Pinned
- 模反元素：num/num.go ModInverse（由 x 折入 [0,m)）；TestModInverse
- 哨兵错误：num/num.go ErrBadArg/ErrNoInverse，errors.Is 区分；TestModInverse
- 边界 gcd(0,0)=0、gcd(a,0)=|a|：egcd.go 基底 b==0；TestExtendedGCDTable
- 大数：int 为 64 位，|x|≤|b|、|y|≤|a| 不溢出；TestExtendedGCDTable（1e18 用例）
- 确定性/并发：纯函数无共享状态；TestConcurrentDeterministic（-race）
- 回代缺陷对照：check/check_test.go buggyEGCD；TestBuggyBackSubstitutionFails
- 递归层数上界：egcd.go RecursionDepth；TestRecursionDepthBound
