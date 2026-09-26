# Householder QR — 推导与不变量

A=[[3,1],[4,1]]。规则：v=x−sign(x0)·‖x‖·e1（sign(0)=+1），再 v←v/v0；H=I−2vvᵀ/(vᵀv)。

1. k=0：x=[3,4]，‖x‖=5，v=[-2,-4]→归一化[1,-2]，vᵀv=5，H0=[[3/5,4/5],[4/5,-3/5]]；列0→[5,0]，H0须同时作用列1→[7/5,1/5]。
2. k=1：x=[1/5]，‖x‖=1/5；x[1:]为空即下三角已全零，v=—（跳过不构造），H1=I，列1不变=[7/5,1/5]。
3. R=H1·H0·A=[[5,7/5],[0,1/5]]。
4. Q=H0·H1=H0=[[3/5,4/5],[4/5,-3/5]]（H0对称且H0²=I，故QᵀQ=I，Q·R=A）。

(甲) 漏因子2，H=I−vvᵀ/(vᵀv)：H[3,4]=[4,2]，故 R[0][0]=4（应5）、R[1][0]=2（应0）。
(乙) 符号取反，v=x+sign·‖x‖·e1=[8,4]→[1,1/2]：H[3,4]=[-5,0]，故 R[0][0]=-5（应+5）。
(丙) 反射子只作用当前列：列1保持[1,1]，故 R[0][1]=1（应7/5）。

## 四条不变量：保证位置 / 钉住测试

1. QR逐元素还原A（≤1e-9）：qr.go 的 Factor 在独立副本上消元、Q/R 均新分配；qr_test.go `TestFactorReconstruct` 钉。
2. QᵀQ=I（≤1e-9）、R严格上三角：hh.go Apply 的 beta=2/(vᵀv) 与行尾嵌入；api_test.go `TestOrthogonalUpper` 钉。
3. 前k-1列逐字节不变：hh.go Apply 列循环 `for j:=k; j<n`，绝不触 j<k；qr_test.go `TestApplyPreservesEarlierColumns` 钉。
4. 失败不留痕（含内部计数器）：qr.go Factor 三项校验先于副本分配与任何计数，成功末尾才 Store 计数；qr_test.go `TestRejectionNoSideEffect` 钉。

计数器为 qr 包非导出 atomic.Int64：`TestUpperTriangleZeroReflectors` 对 n=100/1000/10000 上三角矩阵断言恒0；并发逐字节一致由 api_test.go `TestConcurrentDeterministic` 钉。
