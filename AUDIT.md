# AUDIT

第二节十条语义的保证位置与钉住测试：

1. 替换粒度：`u8.Decode` 的 maximal-subpart 切分（u8/u8.go:53）+ `stream.step` 的 Bad 分支（stream/stream.go:93）；测试 `TestReplaceSamples`（七样例）。
2. 严格模式：`flushBad` 置 `&Error{ErrInvalid, Off, Len}` 且 `t.err` 粘滞（stream/stream.go:127），后续 `Write` 直接返回该错误（stream/stream.go:49）；测试 `TestStrictOffsetLen`（含终态后再写）。
3. 跨切分一致：状态机只依赖 `pend`（≤3 字节合法前缀），与切分无关；测试 `TestSplitConsistency`（所有切点 + 1 字节喂入 + 严格偏移）、`TestUTF16` 末尾切点循环。
4. 流结束半个字符：`Close` 对残留 `pend` 替换模式出 1 个 FFFD、严格模式返回 `ErrTruncated`（stream/stream.go:63），与 `ErrInvalid` 哨兵不同；测试 `TestTruncationInjection`（逐字节截断）、`TestUTF16`（奇数字节、高代理残留）。
5. UTF-16 代理：`u16.Decode` 组合代理对、孤立代理 Bad 且只吞 2 字节（u16/u16.go:33），`step` 把后续单元重新处理；`u16.Append` 出代理对（u16/u16.go:64）；测试 `TestUTF16`。
6. BOM：`flushGood` 仅在首单元识别 BOM（`bomSeen`），UTF-16 遇 FFFE 换序，流中 U+FEFF 原样输出（stream/stream.go:106）；测试 `TestBOM`（含切开的 BOM）、`TestUTF16` BOM 行。
7. 往返与幂等：`u8.Append`/`u16.Append` 只编合法标量，FFFD 本身是合法标量；测试 `TestRoundtripIdempotent`（对照 `unicode/utf8`）。
8. 字节守恒：`flushGood`/`flushBad`/BOM 分支分别累计 GoodBytes/BadBytes/BOMBytes，`Write` 每字节累 Consumed；测试 `TestConservationCap`（三种配置随机输入逐字节喂入）。
9. 输出上限：`emit` 先编码再按检查点回滚（stream/stream.go:138），`Write` 返回 `Consumed-len(pend)`（stream/stream.go:59），缓存字节不算已消费；测试 `TestLimitResume`（含代理对不劈半）。
10. 并行一致：`par.align` 从 b-3 步进对齐（par/par.go:18），段边界必为全局单元边界；测试 `TestParAllK`（K=1..8）、`TestParAllCutPoints`（所有偏移）、`TestParStrict`、`TestParDeterministic`（50 次）。

第四节检查次数实测（`TestCheckCounts`、`TestParChecks`，计数器=状态机取走的输入字节数，缓存内重组合不重复计）：

- stream 1 MB：1,048,576 次（整段与 1 字节喂入相同），上限 2,097,152。
- stream 16 MB：16,777,216 次（两种喂法相同），上限 33,554,432。
- par 1 MB K=8：1,048,619 次 = 输入 + 43，上限 输入 + 128。

故障注入：四类错误 `ErrInvalid`/`ErrTruncated`/`ErrLimit`/`ErrClosed` 为可判定哨兵（`errors.Is`），前两类经 `*stream.Error` 带偏移与长度；截断注入见第 4 条；缓存硬上限 3 由 `TestConservationCap` 在每次 Write 后断言。并发：`par` 各段写独立槽位，`go test -race` 干净；单实例非并发安全已写入 stream 包文档。
