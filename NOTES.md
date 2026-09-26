# 系统采样 NOTES

## 一、推导（N=10, s=4, r=1.0，故 d=N/s=2.5）

| i | r + i·d | index_i = ⌊r + i·d⌋ |
|---|---------|---------------------|
| 0 | 1.0     | 1                   |
| 1 | 3.5     | 3                   |
| 2 | 6.0     | 6                   |
| 3 | 8.5     | 8                   |

- (甲) 正确下标序列 `[1 3 6 8]`。若把 d 错写成整数 ⌊N/s⌋=2（index_i = r+i·2，不取整）：错成 `[1 3 5 7]`。
- (乙) 偏移范围错成 [0,N)，取 r=4.0：⌊4.0+2.5i⌋ 得 `[4 6 8 10]`；越界下标是 **10**（i=3 处，10 ≥ N=10）。
- (丙) ⌊·⌋ 错成四舍五入：1.0→1、3.5→4、6.0→6、8.5→9，错成 `[1 4 6 9]`。

## 二、四条不变量：保证位置 / 钉住测试

1. 恰好 s 个：`samp.Indices` 按 i=0..s−1 循环恰好产出 s 项（samp/samp.go）；`api.New` 拒绝 s≤0、s>N。测试 `TestIndicesExactlyS`。
2. 界内且严格递增：0≤r<d 且 d=N/s≥1 ⇒ ⌊r+id⌋∈[0,N−1] 且逐项至少 +1（samp.Indices 取整计算，samp/samp.go）。测试 `TestIndicesInBoundsStrictlyIncreasing`。
3. 与朴素参照一致：`samp.Indices` 逐项计算 ⌊r+i·(N/s)⌋，与朴素式同式同序同取整。测试 `TestIndicesMatchesNaive`。
4. 失败不留痕：`api.New` 先校验再构造；`api.Sample` 先校验 len(vals)≠N 再委托；`select.Sample` 先校验长度、通过后才写计数器（select/select.go）。测试 `TestRejectedOpsLeaveNoTrace`、`TestSampleLengthMismatch`。

附：目录 `select/` 的包名取 `sel`（`select` 是 Go 关键字，不能作包名），导入路径仍为 `ontology/select`。
