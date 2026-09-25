# Fisher-Yates 洗牌笔记

## 推导：为什么必须从未洗部分选随机下标
- 正确做法：第 i 步（i 从 0 到 n-2）在 `[i, n)` 中均匀选 j，交换
  a[i] 与 a[j]。各步选择数依次为 n, n-1, ..., 2，路径总数 = n!。
  每条路径唯一对应一个排列（逐步可逆），故每种排列概率恰为 1/n!。
- 错误做法（线上事故）：每步从全范围 `[0, n)` 选。路径总数 = n^n，
  而排列只有 n! 种。n! 不整除 n^n（n=4 时 256 条路径对 24 种排列，
  24 不整除 256），路径到排列的映射不可能均匀：某些排列被更多
  路径命中，概率被放大，另一些被压缩。
- 精确枚举全部路径（已用程序验证）：错误实现 max/min 次数比
  n=3 为 5/4=1.25，n=4 为 15/8=1.875，n=6 为 159/32 约 4.97，
  n=7 为 543/64 约 8.48。n=4 时偏斜不足 5 倍，故偏斜断言
  （比 > 5）在 n=7 上做，均匀断言（比 <= 1.5）在 n=4 上做。

## 语义到代码与测试位置
测试均为 check/check_test.go 中 TestShuffle 的表驱动子测试；判定为 cmd/demo/main.go 输出。
- 确定性可复现：shuffle.Shuffle 内 rng.New(seed)；子测试 deterministic permutation；demo 第 1 条
- 无丢失（多集置换）：check.VerifyPermutation；子测试同上（n=0..100）；demo 第 2 条
- 均匀性：shuffle/shuffle.go 第 i 步从 [i,n) 选；子测试 uniformity good vs buggy；demo 第 5、6 条
- 空/单元素合法：shuffle.go 开头 n<2 原样返回；子测试 deterministic permutation 含 n=0,1；demo 第 3 条
- n=2 各约 50%：子测试 uniformity good vs buggy 中 ratio<=1.2；demo 第 4 条
- 恰好 n-1 次随机：shuffle.go 循环 n-1 次，randCalls 原子计数经 RandCalls 读；子测试 rand calls exactly n-1；demo 第 7 条
- 哨兵错误：rng.ErrBadBound、check.ErrNotPermutation、check.ErrNonUniform；子测试 sentinel errors；demo 第 8 条
- 并发：每次 Shuffle 用独立 rng.Source，仅共享原子计数器；子测试 concurrent independent（go test -race 验证）
