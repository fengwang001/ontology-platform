# NOTES

## 回代递推推导（题目第三节）

设递归层已解出 `b·x' + (a mod b)·y' = g`。由 `a mod b = a - (a/b)·b` 代入：

    g = b·x' + (a - (a/b)·b)·y' = a·y' + b·(x' - (a/b)·y')

比较 `a·x + b·y = g`，本层应取 **x = y'，y = x' - (a/b)·y'**。

- 直接对调 x、y（x=x'、y=y'）算的是 `a·x' + b·y'`，而 x'、y' 满足的是
  以 (b, a mod b) 为系数的恒等式，一般不等于 g。
- 漏掉 `(a/b)·y'` 项（y=x'）相当于把 `a mod b` 误当成 `a`，只在 a<b 的层
  偶然成立，回代到顶层即破坏恒等式；互质时「逆元」x 不满足 a·x ≡ 1 (mod m)。
- 测试 TestWrongBackSubstitution 内联漏项实现并断言其失败。

## 复杂度

欧几里得算法每两步使较小数至少减半（Lamé），递归层数 ≤ 2·log2(max(a,b))+2。
egcd 非导出计数器逐层计数、越界报错，大数（斐波那契对最坏情形）测试钉住上界。

## 第二节语义 → 代码位置 / 测试

1. 贝祖恒等式：egcd/egcd.go ExtendedGCD / TestExtendedGCD
2. 模反元素：num/num.go ModInverse（x 模 m 归一化）/ TestModInverse
3. 哨兵错误：num.ErrBadArg、num.ErrNoInverse，errors.Is 区分 / TestModInverse
4. 边界 (0,0)、(a,0)、负数、1e18：符号归一化 / TestExtendedGCD
5. 确定性与并发：纯函数、计数器为局部变量 / TestConcurrentDeterministic（-race）
