# NOTES

## 1. 分步推导：A=[[3,1],[1,3]], v0=[1,0]（每步 λ 取归一化后当前向量的 Rayleigh 商，λ0=0）

| 步 | v(步首) | w=A·v | max|w| | v' | λ=R(v') |
|---|---|---|---|---|---|
| 1 | [1,0] | [3,1] | 3 | [1,1/3] | 18/5 = 3.6 |
| 2 | [1,1/3] | [10/3,2] | 10/3 | [1,3/5] | 66/17 ≈ 3.88235 |
| 3 | [1,3/5] | [18/5,14/5] | 18/5 | [1,7/9] | 258/65 ≈ 3.96923 |
| 4 | [1,7/9] | [34/9,10/3] | 34/9 | [1,15/17] | 1026/257 ≈ 3.99222 |

- (甲) 忘记除以 v'ᵀv'，λ=v'ᵀ·A·v'：第 1 步错成 **4**（正确 3.6）。
- (乙) 分子分母颠倒，λ=(v'ᵀv')/(v'ᵀ·A·v')：第 1 步错成 **5/18 ≈ 0.27778**。
- (丙) 直接拿归一化因子 max|w| 当 λ：第 1 步错成 **3**。

> float64 地板（实测）：规定判据下 `[[3,1],[1,3]]` 的残差只能到 **7.45e-9**——Rayleigh 商被舍入成精确 4.0 时 |λk−λk−1|=0 立即停，而特征向量仅 √eps 精度（特征值二阶收敛、向量一阶）。故不变量1 的 1e-9 由**一步精确收敛**矩阵（残差严格 0）核验，`[[3,1],[1,3]]` 仅用于分步表与 λ 精度。

## 2. 四条不变量：保证位置 / 钉住的测试

1. 朴素参照一致（残差≤1e-9、λ 模最大）：`eigen/eigen.go` 的 `Iterate` 收敛返回处（归一化后算 Rayleigh 商，复用本轮 w 仅一次 matvec）；`api/api.go` 的 `SelfCheck` 三个一步精确矩阵处核验残差；测试 `TestIterate`、`TestEigenConvergence`。
2. 归一化确定（max|v_i|=1、符号保留、逐位可复现）：`eigen/eigen.go` 每轮 `v_i=w_i/power.InfNorm(w)`；测试 `TestIterate`、`TestDeterministic`。
3. 输入不被修改：`api/api.go` 的 `Eigen` 入口拷贝 a/v0，`eigen/eigen.go` 的 `Iterate` 内部再拷贝 v0 且全程只读 a；测试 `TestEigenInputUnchanged`。
4. 失败不留痕（含计数器）：`eigen/eigen.go` 全部校验先于循环，非收敛返回前原子回滚本轮全部计数；`api/api.go` 的 `New` 先拒非法 tol/maxIter；测试 `TestIterateErrors`、`TestRejectedLeavesNoTrace`、`TestEigenErrors`。
