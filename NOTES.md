# isqrt 推导与不变量

## 第三节：八行表（每行 isqrt(n)）

1. n=0 → 0
2. n=1 → 1
3. n=15 → 3（3²=9≤15<16=4²）
4. n=16 → 4
5. n=17 → 4（16≤17<25）
6. n=4503599761588224（=67108865²−1）→ 67108864
7. n=4503599627370496（=67108864²）→ 67108864
8. n=9223372036854775807（=2^63−1）→ 3037000499

- (甲) n=4503599761588224：正确值 67108864。math.Sqrt(float64(n))≈67108864.999999993，float64 舍入为 67108865，uint64 截断后偏大 1；其平方 4503599761588225>n，不变量被破坏。
- (乙) n=2^63−1：mid≈n/2≈4.6×10^18，mid*mid 远超 2^63−1，int64 乘法回绕成负数，`mid*mid<=n` 恒真，二分彻底走错。正确做法：mid>0 时用不溢出的 `mid <= n/mid`（上界侧用 `n/(r+1) < r+1`）。
- (丙) n=67108864²：isqrt 必须精确等于 67108864。停止/取整判据把 `x*x<=n` 错写成严格 `x*x<n` 时，等点不被接受，下偏 1 得 67108863。负输入在任何迭代之前先判 n==MinInt64（-n 溢出 int64）再判 n<0，分别返回不同哨兵错误，不 panic、不留半成品。

## 第二节：四条不变量的保证位置与钉住测试

1. 定义精确：sqrt/sqrt.go 的 newton.isqrt——初值 x≥⌊√n⌋，迭代 x=(x+n/x)/2 单调下降，y≥x 时返回 x；由 TestExactInvariant（api_test.go，含 8 个指定 n 与 k²/k²±1）钉住。
2. 朴素一致：同一 newton.isqrt 实现；sqrt/sqrt_test.go 的 TestNaiveAgreement 对 0..10^6 多档与「逐个平方扫描」参照逐条比对。
3. 边界无 off-by-one：收敛判据的 <= 语义（等点接受）；sqrt/sqrt_test.go 的 TestSquareBoundaries 钉 k²→k、k²−1→k−1。
4. 失败不留痕：newton.isqrt 入口先 ErrMinInt 后 ErrNegative，迭代超 128 次返回 ErrNotConverged，三者互不相同；api_test.go 的 TestSentinelErrors 与 TestRejectionNoSideEffects 钉住。

附：对数收敛由 TestNewtonIterationBounded 钉住（sqrt 包白盒直读非导出字段 lastIters，断言 ≤40）；并发由 TestConcurrentMatchesSerial 在 -race 下钉住。
