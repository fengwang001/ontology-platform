# NOTES：布隆过滤器 k 的推导与实现位置

## 最优 k 的推导（题目第三节）

设位数组 m 位、已加入 n 个元素、k 个哈希函数。某一位在一次插入后仍为 0
的概率约 e^(-k/m)，n 次插入后仍为 0 的概率约 e^(-kn/m)。一次查询恰好
命中 k 个已置位的概率，即假阳性率：

  f(k) = (1 - e^(-kn/m))^k

令 a = kn/m，对 ln f = k·ln(1 - e^(-a)) 关于 k 求导：

  d(ln f)/dk = ln(1 - e^(-a)) + k·(n/m)·e^(-a)/(1 - e^(-a))

代入 e^(-a) = 1/2（即 a = ln2）：ln(1/2) + a·(1/2)/(1/2) = -ln2 + ln2 = 0，
导数为零且两侧符号由负转正，为极小值点。故

  k* = (m/n)·ln2，此时 f = (1/2)^k ≈ 0.6185^(m/n)

由目标 p 反解 m = -n·ln p / (ln2)^2，再得 k = (m/n)·ln2。

- k=1：f = 1 - e^(-n/m)，同样 m 下远高于最优（每位信息未被充分利用）。
- k 过大：每次插入置位太多，位数组迅速饱和，1-e^(-kn/m) 趋近 1，f 回升。

## 代码与测试位置（题目第二节，测试全部位于 check/check_test.go）

1.无假阴性 bloom.MaybeContains→TestBloom/NoFalseNegative；2.参数推导 bloom.New→TestFalsePositiveRate（n=10000、p=0.01，实测≤0.02 且优于内联 k=1）
3.确定性 bloom.hash（seed 混入 FNV）→TestBloom/Deterministic；4.ErrBadParam（bloom/bloom.go）→TestBloom/BadParam（errors.Is 区分）
5.空过滤器安全 MaybeContains/Add 的 m==0 分支→TestBloom/EmptySafe；第四节 reads 计数器（atomic，恰好 k 次）→TestBloom/ReadsEqualK
第五节并发只读→TestBloom/ConcurrentRead（16 goroutine，-race 干净）；demo 判定见 cmd/demo/main.go
