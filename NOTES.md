# NOTES

## 回代递推推导（题目第三节）
设递归层已求得 b·x' + (a mod b)·y' = g。由 a mod b = a - (a/b)·b 代入：
  g = b·x' + (a - (a/b)·b)·y' = a·y' + b·(x' - (a/b)·y')
故本层系数为 x = y'，y = x' - (a/b)·y'。
若把 x、y 直接对调（取 x = x'、y = y'）或漏掉 -(a/b)·y' 项，
则本层实际算出 a·x' + b·y'，一般不等于 g，贝祖恒等式被破坏；
互质时 x 不再是 a 模 b 的逆元，ModInverse 随之算错。
反例：egcd(240,46)，g=2，正确 (x,y)=(-9,47)；漏项实现得 (0,1)，
240·0 + 46·1 = 46 ≠ 2。测试见 TestBuggyImplFailsIdentity。

## 语义落点（题目第二节，测试均在 check/check_test.go）
1. 贝祖恒等式：egcd/egcd.go ExtendedGCD；TestExtendedGCDBezout
2. 模反元素：num/num.go ModInverse（由 x 取最小非负剩余）；TestModInverse
3. 哨兵错误 ErrBadArg / ErrNoInverse：num/num.go；TestModInverseErrors
4. 边界 gcd(0,0)=0、gcd(a,0)=|a|、大数不溢出（int 即 int64，
   回代项 |x|≤b/g、|y|≤2a/g 不溢出）；TestExtendedGCDBezout
5. 确定性：egcd 为纯函数、无全局态；TestConcurrentPure

## 复杂度（题目第四节）
egcd.solve 内维护非导出递归层数计数器，由 Depth 暴露供测试断言：
depth ≤ 2·log2(max(a,b))+2（a,b ≤ 1e18），与欧几里得算法对数收敛一致；
TestRecursionDepthBound 含 Fibonacci 相邻项这一最坏情形。

## 参照实现
check.NaiveGCD（枚举约数）与 check.BigBezout（math/big.GCD）交叉对照：
与恒等式断言同在 TestExtendedGCDBezout 内逐用例核对。
