# AUDIT：语义逐条对照

| # | 语义 | 代码保证位置 | 钉住它的测试 |
| --- | --- | --- | --- |
| 1 | 往返 | `enc/enc.go` process + `dec/dec.go` backref/emit | TestRoundTrip（空/全同/随机/周期/文本） |
| 2 | 重叠回指 | `dec/dec.go` backref 逐字节向前复制循环 | TestOverlapBackref（周期 1/2/3，长度 3000） |
| 3 | 压缩切法无关 | `enc/enc.go` process：匹配未闭合即挂起，字面量攒段 | TestWriteSplitting（块 1/7/整段，含 Flush@63） |
| 4 | Flush 承诺 | `enc/enc.go` Flush：process(true)+空缓冲直接返回 | TestFlushPromise（部分可解、二次 Flush 零字节） |
| 5 | 解压切法无关 | `dec/dec.go` step/varint 逐字节状态机 | TestSplitFeedAndEmpty（遍历全部切分点） |
| 6 | 空与缺 | `enc/enc.go` Close 必写流尾；`dec/dec.go` Close 非 stDone 即截断 | TestSplitFeedAndEmpty（空流/零长/仅流头） |
| 7 | 损坏检测 | `dec/dec.go` gotValue/backref 各分支 + `wire/wire.go` Error 带偏移 | TestCorruption（9 类，断言哨兵与偏移） |
| 8 | 炸弹防护 | `dec/dec.go` 写出前查 maxOut，err 置终态 | TestOutputLimit（回指/字面量各 2^40，终态粘滞） |
| 9 | 并行确定 | `enc/enc.go` CompressParallel：块输出只依赖本块+前块字典 | TestCompressParallel（workers 1/2/4/8 + 30 次） |

故障注入：TestTruncation（逐字节截断，输出恒为原文前缀）、TestFlip（逐字节逐位翻转，无 panic、不越限、不静默错）。配置拒绝：TestConfigRejected、match.TestNewRejectsBadChain。并发：TestConcurrentDecoders、TestCompressParallel，`go test -race` 干净。

## 复杂度实测（`go test -v`，链长上限 16，窗口 32KB）

| 输入 | 考察候选总数 | 每字节考察数 |
| --- | --- | --- |
| 全同 64KB | 65533 | 1.0000 |
| 全同 4MB | 4194301 | 1.0000 |
| 随机 64KB | 48947 | 0.7469 |
| 随机 4MB | 4181372 | 0.9969 |

两档全同输入每字节考察数之比为 1（不随规模增长），四档均 ≤ 16×n（TestExaminedBound）。全同 4MB 压缩后 24 字节 < 64KB（TestLongBackref），证明使用了长回指。
