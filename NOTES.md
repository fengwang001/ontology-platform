# MTF 推导与不变量

字母表 `[a b c d e f]`，数据 `"ababaadd"`（先输出当前下标，再移到队首）：

| # | 符号 | 下标 | 移动后列表 |
|---|---|---|---|
| 1 | a | 0 | `[a b c d e f]` |
| 2 | b | 1 | `[b a c d e f]` |
| 3 | a | 1 | `[a b c d e f]` |
| 4 | b | 1 | `[b a c d e f]` |
| 5 | a | 1 | `[a b c d e f]` |
| 6 | a | 0 | `[a b c d e f]` |
| 7 | d | 3 | `[d a b c e f]` |
| 8 | d | 0 | `[d a b c e f]` |

输出序列：`0,1,1,1,1,0,3,0`

(甲) 先移到队首再输出：每步符号都已在队首，恒输出 0 → `0,0,0,0,0,0,0,0`；从第 2 个符号 b 起出错（应为 1）。
(乙) 下标 1 起始 (+1)：`1,2,2,2,2,1,4,1`；交给按 0 起始读的正确解码器，逐步还原为 `b c a b c b e b` = `"bcabcbeb"` ≠ 原文。
(丙) `Encode([], alpha)` 返回 `[]`、无错；字母表为空或含重复元素均返回 `ErrInvalidAlphabet`；单符号 `[a]` 下 `Encode("aaa")` = `[0,0,0]`。

## 四条不变量（保证位置 / 钉住的测试）

1. 与朴素参照一致：`mtf/mtf.go` 的 `EncodeSymbol` 先读 `pos[s]` 后移动、`DecodeSymbol` 先取 `list[i]` 后移动；`mtfseq/seq.go` 逐符号委托。测试 `mtf.TestEncodeSymbolMatchesNaive`、`mtf.TestDecodeSymbolMatchesNaive`、`api.TestSelfCheck`。
2. 确定性/往返：`mtfseq.Encode/Decode` 每次经 `mtf.New` 从字母表副本建全新状态；测试 `api.TestDeterministic`、`api.TestRoundTrip`。
3. 列表恒为排列且映射一致：`mtf/mtf.go` 的 `moveToFront` 只做区间平移并同步重写 `pos`，不增删符号；测试 `mtf.TestListIsPermutation`。
4. 失败不留痕：`EncodeSymbol/DecodeSymbol` 在校验通过前不修改任何字段，`mtfseq` 出错返回 nil；测试 `mtf.TestStepFailureStateUnchanged`、`api.TestAtomicFailure`。
