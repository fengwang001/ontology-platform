# NOTES

n=5,b=2，k 块为 [0,2)、[2,4)、[4,5)；A[3][k]=16+k，B[k][2]=k-2。C[3][2] 五行累加：

| k | k 块区间 | A[3][k] | B[k][2] | 乘积 | 部分和 |
|---|---|---|---|---|---|
| 0 | [0,2) | 16 | -2 | -32 | -32 |
| 1 | [0,2) | 17 | -1 | -17 | -49 |
| 2 | [2,4) | 18 | 0 | 0 | -49 |
| 3 | [2,4) | 19 | 1 | 19 | -30 |
| 4 | [4,5) | 20 | 2 | 40 | 10 |

正确值 C[3][2]=10。三问：

- (甲) 只处理完整块、跳过尾块 [4,5)：少加 k=4 的 40，错成 **-30**（正确 10）。
- (乙) 块内上界写成 <=(k+1)*b：k=2 重复 18*0=0、k=4 重复 20*2=40，错成 **50**。
- (丙) 误读 B[j][k]=B[2][k]=2-k：32+17+0-19-40，错成 **-10**。

第二节四条不变量（保证位置 / 钉住的测试函数）：

1. 与朴素逐位相等：mul/mul.go 的 Blocked 按 i块→k块→j块分组、固定(i,j) 的 k 严格升序，累加次序与 Naive 相同；api/api_test.go 的 TestBlockedMatchesNaive（含不整除 n 与负值）。
2. 块覆盖完备、尾块=n%b：blk/blk.go 的 Parts 用 q*b..min((q+1)*b,n) 闭式切块；blk/blk_test.go 的 TestPartsCover 断言无重无缝且尾块大小=n%b。
3. 位级稳定：mul/mul.go 的 Blocked 只写本次新建的局部 c，无 map 遍历、无并发写；api/api_test.go 的 TestDeterministic 与 TestConcurrentMul（N goroutine 结果逐字节相同）。
4. 失败不留痕：api/api.go 的 Mul 先做全部校验、拒收时直接返回，绝不触达 mul/blk，状态不被触碰；api/api_test.go 的 TestRejectionsLeaveNoTrace（三类拒收后正常乘仍正确）。

另：尾块 O(1) 闭式由 blk/blk.go 的 TailBlock（q=k/b 整除）保证，非导出探针 boundaryProbe.n 每次原子存 0，blk/blk_test.go 的 TestTailBlockProbeConstant 对 n=100/1000/10000 断言恒为 0。
