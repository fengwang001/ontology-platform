# NOTES

## 八步推导（headerSize=8，next 从 0 开始）

| # | 操作 | ptr / Free 读出 base | 该步后 next |
|---|---|---|---|
| 1 | Alloc(10,16) | ptr=16 | 33 |
| 2 | Alloc(4,8) | ptr=48 | 52 |
| 3 | Alloc(8,8) | ptr=64 | 75 |
| 4 | Free(16) | base=0 | 75 |
| 5 | Free(48) | base=33 | 75 |
| 6 | Alloc(1,8) | ptr=88 | 91 |
| 7 | Alloc(16,16) | ptr=112 | 130 |
| 8 | Free(64) | base=52 | 130 |

（甲）base=33：正确 ptr=alignUp(41,8)=**48**（8 的倍数）；不对齐 ptr=41，41%8=1 非倍数。
（乙）头写 [base,base+8) 时 Free(16) 读 [8,16)：零内存巧合读到 0（恰对）；但 Free(48) 读
[40,48)=0 而非存在 [33,41) 的 33，Free(64) 读到 0 而非 52 → 回溯 base 错误、记账/释放全失效。
（丙）不校验则 Alloc(4,6) 被接受，位运算 (8+5)&^5=8 非 6 倍数，返回未对齐 ptr；
正确应返回哨兵 **ErrBadAlign** 且状态不变。

## 不变量：保证位置 + 钉住的测试

1. 对齐合法：alloc.Alloc 用 align.AlignUp 算 ptr —— alloc/alloc_test.go `TestAllocAlignment`。
2. 与朴素参照一致（指针集合 + 每头部 base）：alloc.SelfCheck 内建朴素模拟逐步比对 ——
   `TestSelfCheckAndComplexity`；随机序列另由 api/api_test.go `TestNaiveMatch` 钉住。
3. 释放回溯正确：alloc.Free 只读 [ptr-8,ptr) 的 base 且与记录核对 —— `TestFreeReadsHeaderBase`。
4. 失败不留痕：所有校验先于状态修改 —— alloc `TestRejectionsAreDistinctAndTraceless`、
   api `TestRejectedOpsLeaveNoTrace`；复杂度 probes 不导出，`TestFreeProbeCountConstant` 钉 ≤1。
