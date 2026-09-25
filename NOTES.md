# NOTES — 扩展欧几里得（extended GCD）

## 系数回代推导（事故根因）

递归层不变式：对 `(a, b)`，递归调用 `(b, a mod b)` 返回 `x'、y'` 满足
`b·x' + (a mod b)·y' = g`。代入整除恒等式 `a mod b = a − (a/b)·b`：

    g = b·x' + (a − (a/b)·b)·y' = a·y' + b·(x' − (a/b)·y')

比较 `a·x + b·y = g`，得回代关系：`x = y'`，`y = x' − (a/b)·y'`。

**对调或漏项为何错**：`y'` 是 `a mod b` 的系数，须整体换到 `x`；`x'`
还要补偿整除丢掉的 `(a/b)·b` 部分。对调成 `x=x'、y=y'` 或漏掉
`(a/b)·y'` 项，等式一般不再成立。反例 `(240,46)`：正确 `x=−9、y=47`
（`240·(−9)+46·47=2`）；漏项实现回代出 `x=−3、y=5`，
`240·(−3)+46·5=−490≠2`，互质时模反元素随之算错。

## 代码位置与测试索引

- 贝祖恒等式：`egcd/egcd.go` `ExtendedGCD`/`solve`；`TestExtendedGCDIdentity`
- 模反元素：`num/num.go` `ModInverse`（贝祖 x 规范化到 [0,m)）；`TestModInverse`
- 错误：`num.ErrBadArg`/`num.ErrNoInverse`，`errors.Is` 区分；`TestModInverse`
- 边界：gcd(0,0)=0、gcd(a,0)=|a|、10^18 大数（int64 宽度）；`TestExtendedGCDIdentity`、`TestDepthBound`
- 确定性：同输入同 (x,y)（`TestExtendedGCDIdentity` 二次调用）；并发纯函数 `TestConcurrent`
- 事故钉住：`TestDroppedQuotientTermFails`（内联漏掉 (a/b) 项的错误实现）
