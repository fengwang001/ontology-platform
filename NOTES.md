# 最小二乘（正规方程）推导与不变量

m=3,n=2, A=[[1,1],[1,2],[1,3]], b=[2,3,5]

1. Gram：G00=1+1+1=3，G01=G10=1+2+3=6，G11=1+4+9=14 → [[3,6],[6,14]]
2. Aᵀb：c0=2+3+5=10，c1=2+6+15=23 → [10,23]
3. 消元（无主元）：行1←行1−(6/3)·行0 → [[3,6 | 10],[0,2 | 3]]
4. 回代：x1=3/2；x0=(10−6·(3/2))/3=(10−9)/3=1/3 → x=[1/3,3/2]
5. 残差 b−Ax：[1/6,−1/3,1/6]，且 Aᵀr=[0,0]

- (甲) 只填上三角、下三角置 0（G10=0）：行1消元乘数为 0 不变，x1=23/14，x0=(10−6·23/14)/3=1/21 → 错成 [1/21, 23/14]
- (乙) c0 漏加最后一行（只累加到 k=1）：2+3=5（应为 10）
- (丙) x0 漏减 G01·x1：x0=10/3（应为 1/3）

## 不变量 → 代码保证位置 → 钉住的测试

1. 残差最小（Aᵀ(Ax−b)=0，每项≤1e-9）：gram.AtB 全列点积 + lsq.SolveG 对完整对称矩阵消元回代 → `TestOptimality`（api/api_test.go）
2. Gram 逐位对称且按完整对称求解：gram.Gram 将同一累加值同时写入 (i,j) 与 (j,i)；lsq.SolveG 读写完整 n×n → `TestGramSymmetric`（lsq/lsq_test.go）
3. 一次 Factor 多次 Solve：api.Factor 缓存 Gram 且只有它对计数器 atomic.Add；api.Solve 只复制缓存只读使用 → `TestFactorOnceGramCount`（api/api_test.go，m=100/1000/10000）
4. 失败不留痕：api.Factor/Solve 先做全部维度校验、秩亏用 Gram 副本探测，全部通过后才改状态/计数器 → `TestRejectedOpsLeaveNoTrace`（api/api_test.go）

并发只读缓存、计数器原子：`TestConcurrentSolveIdentical`（api/api_test.go，go test -race，逐字节一致）。
