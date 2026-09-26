# Notes — condition number estimation (∞-norm, n=2, k=2)

A=[[1,3],[0,2]], A⁻¹=[[1,-3/2],[0,1/2]], ‖A⁻¹‖∞=5/2, true κ∞=4·5/2=10.

| 行 | ‖A‖∞ | b | y / ‖y‖∞ | sign(y) | 估计值 |
|---|---|---|---|---|---|
| 0 初始 | 4 | [1,1] | — | — | — |
| 1 轮1 | 4 | [1,1] | [-1/2, 1/2] / 1/2 | [-1,1] | — |
| 2 轮2 | 4 | [-1,1] | [-5/2, 1/2] / 5/2 | [-1,1] | — |
| 3 汇总 | 4 | — | max‖y‖∞=5/2 | — | ‖A⁻¹‖∞估计=5/2 |
| 4 结果 | 4 | — | — | — | κ∞估计=4·5/2=**10** |

- (甲) 忘记取逆，用 ‖A‖∞·‖A‖∞：4·4=**16**（真 10）。
- (乙) 用 1/‖A‖∞ 估计 ‖A⁻¹‖∞：1/4，κ=4·(1/4)=**1**。
- (丙) 误解 Aᵀy=b：轮1 b=[1,1]→y=[1,-1]/1，sign=[1,-1]；轮2 b=[1,-1]→y=[1,-2]/2；est=2，κ=4·2=**8**（这是 ‖A⁻¹‖₁=2）。

## 四条不变量（位置 / 钉住的测试）

1. 有效下界 est≤true（每轮 ‖b‖∞≤1 ⇒ ‖y‖∞≤‖A⁻¹‖∞），且内置矩阵上 est≥true/n：`normest.go` EstimateInvInf 的取 max；`api.go` naiveInverse 参照。测试 **TestCondBounds**。
2. 不显式求逆、求解次数恰为 k：core 只做前向/回代，`solves` 原子计数，`normest.go`。测试 **TestSolveCounterConstantInN**（n=100/1000/10000 恒为 3）。
3. 一次分解多次求解：Factor 在 k 轮循环前只调一次，轮内仅 solve，`normest.go` EstimateInvInf。测试 **TestSolveCounterConstantInN**。
4. 失败不留痕：n、len、k、奇异四类校验全部在 Factor 与计数自增之前，`api.go` Cond 与 `normest.go` Estimate。测试 **TestSentinelErrors**（四哨兵互不相同、计数器不变、被拒后仍可正常使用）。
