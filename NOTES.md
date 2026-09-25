# Fisher-Yates 均匀洗牌推导笔记

## 为什么必须从未洗部分选下标

正确做法（从后往前）：第 i 步在 `[0, i]` 中等概率选 j，与 a[i] 交换。
共做 n-1 次选择，每步可选个数依次为 n, n-1, ..., 2，
选择路径总数 = n!，且每条路径概率均为 1/n!。
关键性质：该过程与排列一一对应——给定最终排列可唯一反推每步的 j
（从最后一步向前还原交换即可），故每种排列恰好对应一条路径，
概率恰为 1/n!，严格均匀。

错误做法：每步都从 `[0, n)` 全范围选 j。
每步 n 种选择，路径总数为 n 的幂次（如 n 步即 n^n）条等可能路径，
但排列只有 n! 种。路径到排列是多对一映射，而 n! 不能整除 n^n
（n>=3 时 n^n / n! 非整数，如 n=4：256/24 不是整数），
故各排列分到的路径数必然不等：有的排列概率为零或翻倍，分布严重不均。
直觉：全范围选会把已洗好的尾部元素再次搅动，且交换可重复作用于同一位置，
破坏了「一步定一个位置、一一对应」的结构。

注：枚举实测 n=4 全范围选（n-1 步，64 条路径）各排列计数为 1..5，最大/最小
恰为 5.0，故测试中的错误实现对照改用 n=5（625 条路径，计数 1..15，比 15）。

## 代码与测试位置

- 确定性/无丢失/空与单元素合法/随机调用恰 n-1 次：shuffle.Shuffle（shuffle/shuffle.go），测试 TestShuffle
- 均匀性（n=4 阈值 1.5、n=2 约 50%）与错误实现 naive 对照：TestUniformity
- 哨兵错误（rng.ErrNonPositiveN、shuffle.ErrNilSlice、check.ErrEmptyCounts）与并发安全：TestErrorsAndConcurrent
- 统计函数 check.MaxMinRatio、check.ChiSquare（check/check.go）；演示 cmd/demo/main.go；测试均在 check/check_test.go
