# Notes

## 推导：N=10, s=4, r=1.0, d=N/s=2.5

| i | r + i·d | index_i = floor(r+i·d) |
|---|---------|------------------------|
| 0 | 1.0     | 1                      |
| 1 | 3.5     | 3                      |
| 2 | 6.0     | 6                      |
| 3 | 8.5     | 8                      |

- (甲) 正确下标序列 = [1 3 6 8]。若 d 错写成整数 floor(N/s)=2 且不再取整：index_i = 1 + 2i，错成 [1 3 5 7]。
- (乙) 偏移范围错成 [0,N)，取 r=4.0：floor([4.0 6.5 9.0 11.5]) = [4 6 9 11]；i=3 处下标 11 >= N=10 越界。
- (丙) floor 错成四舍五入：1.0→1, 3.5→4, 6.0→6, 8.5→9，错成 [1 4 6 9]。

## 四条不变量：保证位置 / 钉住的测试

1. 恰好 s 个：`samp.New` 固定循环 i=0..s-1 压入一个下标（samp/samp.go）；由 `TestExactCount` 钉住。
2. 界内且严格递增：`samp.New` 中 d=N/s>=1（s<=N），相邻差 floor(x+d)-floor(x)>=1；r<d 使末项 floor(r+(s-1)d) <= N-1、首项 floor(r)>=0（samp/samp.go）；由 `TestBoundsAndStrictIncrease` 钉住。
3. 与朴素参照一致：`samp.New` 本身就是逐项 floor(r+i*(N/s)) 顺序计算；测试用同样朴素公式重算并逐位比对（api/api_test.go）；由 `TestNaiveReference` 钉住。
4. 失败不留痕：非法 s、越界 r 在 `samp.New` 构造完成前返回哨兵错误、无对象产生；`Sample` 先校验 len(vals)==N，通过后才把计数器清零并开始计数（select/select.go）；由 `TestRejectedOpsLeaveNoTrace` 钉住。

访问计数为非导出字段 `accessed`，其**数值**仅由同包测试 `TestSampleAccessCountEqualsS` 直接读取断言；对外只有无参方法 `VerifyLastSampleTookExactlyS()` 返回「是否恰为 s」的固定布尔，任何数值都无法经导出接口读出或探取。
