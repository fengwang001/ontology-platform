# 扩展欧几里得：回代推导与索引

## 系数回代推导（事故根因）

递归层已知 `b·x' + (a mod b)·y' = g`。由 `a mod b = a - (a/b)·b` 代入：

    g = b·x' + (a - (a/b)·b)·y'
      = a·y' + b·(x' - (a/b)·y')

对照 `g = a·x + b·y`，得本层回代关系：

    x = y'
    y = x' - (a/b)·y'

若把 x、y 直接对调（`x = x'`、`y = y'`），得到的是 `a·x' + b·y'`，
它对应 gcd(b, a mod b) 的系数而非 gcd(a, b) 的，恒等式一般不成立。
若漏掉 `(a/b)` 项（`y = x'`），则结果与 g 相差 `(a/b)·b·y'`，
仅当 `(a/b)·y' == 0` 时侥幸成立；互质时算出的“逆元”也是错的。

## 语义与代码位置索引

- 贝祖恒等式：`egcd/egcd.go` `ExtendedGCD`/`extend`；测试 `TestExtendedGCD`、`TestPinned240_46`
- 错误回代钉住：`TestWrongBackSubstitution`（内联漏掉 `(a/b)` 项的错误实现）
- 模反元素：`num/num.go` `ModInverse`（互质时逆元为 x 归一化到 [0,m)）；测试 `TestModInverse`
- 哨兵错误：`num/num.go` `ErrBadArg`/`ErrNoInverse`，`errors.Is` 可区分；测试 `TestModInverse`
- 边界 gcd(0,0)=0、gcd(a,0)=|a|：`egcd/egcd.go` `mag`/`extend`；`TestExtendedGCD` 表内用例
- 大数不溢出：实现只用 int 加减乘（1e18 自洽），验证用 `check/check.go` `BezoutOK`（big.Int）
- 递归层数上界：`egcd/egcd.go` 非导出计数器 `maxDepth`+`track`；测试 `TestRecursionDepthBound`
- 确定性与并发：`TestConcurrentDeterministic`（-race 干净，纯函数）
