# LU 分解推导 n=3：A=[[2,1,1],[4,3,3],[8,7,9]]，b=[7,19,49]（Doolittle 无主元）
1. k=0 主元=2 乘子 L10=4/2=2，行1-=2行0 -> [0,1,1]；矩阵 [[2,1,1],[0,1,1],[8,7,9]]
2. k=0 主元=2 乘子 L20=8/2=4，行2-=4行0 -> [0,3,5]；矩阵 [[2,1,1],[0,1,1],[0,3,5]]
3. k=1 主元=1 乘子 L21=3/1=3，行2-=3行1 -> [0,0,2]；矩阵 [[2,1,1],[0,1,1],[0,0,2]]
4. L=[[1,0,0],[2,1,0],[4,3,1]]（对角恒 1，上半全 0）
5. U=[[2,1,1],[0,1,1],[0,0,2]]（下半全 0）
前向代入：y0=7，y1=19-2*7=5，y2=49-4*7-3*5=6，y=[7,5,6]（不除 Lii，因其=1）
回代：x2=6/2=3，x1=(5-1*3)/1=2，x0=(7-1*2-1*3)/2=1，x=[1,2,3]，Ax=b 成立。
(甲) L00 误存成 2 且前向每步除以 Lii：y0=7/2=3.5（正确 7）
(乙) 乘子分子分母颠倒：L10=A00/A10=2/4=0.5（正确 2）
(丙) 前向减号写反成加号：y1=19+2*7=33（正确 5）

## 不变量 -> 代码保证位置 -> 钉住的测试函数
1. LU 逐元素还原 A（误差<=1e-9）：lufact/lufact.go 的 Factor 消元双重循环，L/U 一次性在新切片算完 -> TestFactorReconstructA
2. L 单位下三角、U 上三角：Factor 中 L 先置对角 1、乘子只写下三角，U 只写上三角 -> TestFactorUnitTriangular
3. 一次分解多次求解：api.Factor 成功后缓存 L/U，api.Solve 只调 solve.Forward/Backward，factorCalls 仅在 Factor 成功时原子加 1 -> TestSolveReuseCounter（m=100/1000/10000 计数恒 1）
4. 失败不留痕：api.Factor/Solve 全部校验与分解都在局部副本完成、通过后才整体替换缓存，计数器不增 -> TestRejectedOpsLeaveState
自检入口 api.SelfCheck 覆盖以上四条 -> TestSelfCheck；并发只读与逐字节一致 -> TestConcurrentSolveIdentical
哨兵错误：ErrDimension（维度不一致）、ErrEmpty（n<1）、ErrZeroPivot（零主元）在 lufact 包，ErrNotFactored 在 api 包，四者互不相同。
