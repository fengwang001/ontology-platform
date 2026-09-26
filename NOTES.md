# EWMA 推导与不变量

α=0.9, seed=0, 不校正，递推 s ← 0.9·x + 0.1·s_old：

| 步 | x | s |
|---|---|---|
| 1 | 10 | 9 |
| 2 | 20 | 18.9 |
| 3 | 10 | 10.89 |
| 4 | 20 | 19.089 |
| 5 | 10 | 10.9089 |

(甲) 权重反转为 s=0.1x+0.9s_old：第1步=1（正确 9）；第5步=5.7241（正确 10.9089）。
(乙) seed 改成 x1=10：第1步=0.9·10+0.1·10=10（正确 seed=0 时为 9），首观测即被钉死。
(丙) n=1 偏置校正：9/(1−0.1^1)=9/0.9=10；s1=α·x1、分母 1−(1−α)=α，相约恰为首个观测 x1。

不变量（保证位置 / 钉住测试）：

1. 范围：ewma/ewma.go 的 Update 为凸组合、Value 仅除以 1−β^n∈(0,1]（seed=0 时常数输入仍等于 c），区间不扩张；测试 TestRangeInvariant。
2. 常数精确：ewma/ewma.go Value 用 β^n 计数幂做校正，seed=0 时常数 c 校正后恰为 c；测试 TestConstantInput。
3. 闭式一致：ewma 递推即闭式 α·Σβ^(n−i)·x_i+β^n·seed 的逐项归纳；api/api_test.go TestClosedForm 对随机序列批量重算比对。
4. 失败不留痕：api/api.go 哨兵 ErrInvalidAlpha/ErrEmptyKey/ErrNotFound 先判定后触态，空 key 不建表项；测试 TestRejectedOperationsLeaveNoTrace。
