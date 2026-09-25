# Bloom Filter 推导与索引

## k 的最优值推导（第三节）

f(k) = (1 - e^(-kn/m))^k。令 a=n/m、x=e^(-ak)，则 k=-ln(x)/a：
ln f = -(1/a)·ln(x)·ln(1-x)。ln(x)·ln(1-x) 在 (0,1) 为正、关于
x=1/2 对称，极值点在 x=1/2（等价地 g(k)=k·ln(1-e^(-ak)) 求导：
g'=ln(1-x)+akx/(1-x)，代入 x=1/2 得 g'=-ln2+ak=0，且为最小值）。
故 e^(-ak)=1/2，即 k=(m/n)·ln2 时 f 最小，f_min=(1/2)^k≈0.6185^(m/n)。
反解 m=-n·ln p/(ln2)^2。k=1 时 f≈1-e^(-n/m)≈n/m，随负载线性恶化；
k 过大则位数组被填满（底数 1-e^(-kn/m)→1），f 重新上升。

## 语义 → 代码/测试索引（第二节）

1. 无假阴性：bloom/bloom.go Add/MaybeContains；TestNoFalseNegativeAndFPRate（check.Verify 对拍）
2. 参数推导：bloom/bloom.go New（m、k 公式）；TestNoFalseNegativeAndFPRate（fp≤0.02）
3. 确定性：bloom/bloom.go hashes（seed 固定的 FNV-1a）；TestConcurrentReads（同 seed 双实例）
4. ErrBadParam：bloom/bloom.go:15；TestBadParamAndEmpty（errors.Is）
5. 空过滤器安全：bloom/bloom.go MaybeContains 零值分支；TestBadParamAndEmpty

## 其他约束

- 位读取计数：bloom/bloom.go Filter.reads（atomic）；TestNoFalseNegativeAndFPRate 断言 Reads==K
- 并发只读：TestConcurrentReads（16 goroutine，-race 干净）
- k=1 对拍：TestNoFalseNegativeAndFPRate 内联 k=1，断言 fpK1>fpOpt
- 位数组：bits/bits.go Set/Get/Len；TestBitSet（含 ErrOutOfRange）
- 朴素参照：check/check.go Set/Verify（ErrFalseNegative）
