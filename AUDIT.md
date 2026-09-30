# 语义审计

每条语义：保证位置 + 钉住它的测试。

| # | 语义 | 保证位置 | 测试 |
| --- | --- | --- | --- |
| 1 | 往返逐字节相等 | enc.core.process 贪心解析 + dec.step 状态机；流尾长度与 FNV-1a 校验 | TestRoundTrip（空/全同/随机/周期/混合） |
| 2 | 重叠回指逐字节复制 | dec.backref 的 `for ; length > 0; length--` 逐字节 emit（dec/dec.go:140） | TestOverlapBackref（dist 1/2/3，长度 1000） |
| 3 | 压缩与 Write 切法无关 | enc.core.process：匹配终点==缓冲末尾且未被 MaxLen 截断时暂缓定案；字面量只在匹配定案或 final 时成段输出（enc/enc.go:44） | TestWriteChunking（chunk 1/7/100/整段 × 有无 Flush） |
| 4 | Flush 承诺与连续 Flush 零字节 | Encoder.Flush：process(true) 强制定案 + 刷新标记；clean 标志使无新输入时不产出（enc/enc.go:95） | TestFlushPromise |
| 5 | 解压与切法无关 | dec.Write 逐字节状态机，无跨调用缓冲假设（dec/dec.go:48） | TestDecodeChunking（chunk 1/3/7/64/整段） |
| 6 | 空流合法非空；零长流/秃头流判截断 | enc.New 写流头 + Close 写流尾；dec.Close 要求 ended（dec/dec.go:60） | TestEmptyVsTruncated |
| 7 | 八类损坏可区分且带偏移 | wire 包哨兵错误 + wire.Error.Offset；dec.backref 与 dec.step/onValue 分别检查 | TestCorruption（9 种流逐条断言 errors.Is 与偏移） |
| 8 | 解压炸弹写出前拒绝 + 终态 | dec.onValue/backref 在 emit 前比较 `v > maxOut-total`；d.err 粘性（dec/dec.go:48） | TestOutputBomb（回指与字面量两档，终态复查） |
| 9 | 并行块压缩确定且可解 | enc.CompressParallel：块 i 仅依赖 data[dict:hi] 原文切片，静态分块 stride 分配，结果按序拼接（enc/enc.go:128） | TestParallelDeterministic（workers 1/2/4/8 + 30 次重复 + 可解） |

## 补充不变量

- 截断遍历：每个截断点 Close 均返回 ErrTruncated 且输出为原文前缀 —— TestTruncationTraversal。
- 翻转遍历：每字节翻转一位，无任何静默错误内容（全部报错或结果仍等于原文） —— TestFlipTraversal。
- 非法配置：窗口为 0 / 链长为 0 / 块大小为 0 / workers 为 0 构造时拒绝 —— TestInvalidConfig。
- 并发：并行压缩与多解压器实例在 `go test -race` 下干净 —— TestParallelDeterministic、TestConcurrentDecoders。
- 单实例非并发安全：已在 enc/dec 包注释中声明。

## 复杂度实测（TestCandidateBounds 输出）

链长上限 128，窗口 1 MB，MaxLen = 65536。

| 输入 | 考察总数 | 每字节考察 | 压缩后大小 |
| --- | --- | --- | --- |
| 全同 64 KB | 2 | 0.000031 | 26 B |
| 全同 4 MB | 65 | 0.000015 | 342 B |
| 随机 64 KB | 128 | 0.001953 | 65562 B |
| 随机 4 MB | 18423 | 0.004392 | 4197906 B |

- 考察总数均 <= 128 × 输入字节数（实际上界远未触及）。
- 全同两档每字节考察数之比约 2:1（常数级，不随规模增长），满足 <= 2 倍约束。
- 全同 4 MB 压缩后 342 B < 64 KB，证明使用了长回指而非字面量。
