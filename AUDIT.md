# AUDIT

## 语义逐条核对（保证位置 / 钉住它的测试）

1. 往返：`enc.parse` 只产出字面量与回指，`dec.step` 逐条还原；
   `enc.TestRoundTrip`（空/全同/随机/周期2/周期3/文本）。
2. 重叠回指：`dec/dec.go` 的 `sRefLen` 用 `win.At(dist)` 逐字节前向
   复制；`match.Matcher.Longest` 在 l ≥ dist 时读本数据前缀。
   `match.TestLongest`（距离 1/2/3）+ `enc.TestRoundTrip` 的
   same/period2/periodic（长度远大于距离）。
3. 切法无关：`enc.Encoder.Write` 只追加缓冲，解析推迟到
   `Flush`/`Close`（DESIGN 推导一）；`enc.TestWriteChunking`
   （1/7/30/840 字节切法 + 相同 Flush 偏移，输出逐字节相同）。
4. Flush 承诺：`enc.Flush` 解析全部未决输入并写刷新标记；
   `e.pos == len(e.data)` 时直接返回（零字节）。
   `enc.TestFlush`（Flush 后即可解出前缀、连续 Flush 零字节、
   Flush 后回指仍可指向之前数据——由同一 Matcher/窗口持续保证）。
5. 解压切法无关：`dec.Write` 是逐字节状态机，与分段无关；
   `dec.TestChunkSplit`（遍历全部切分点）。
6. 空与缺：`enc.Close` 空输入也写流头+流尾；`dec.Close` 在
   `st != sDone` 时报 `ErrTruncated`。`dec.TestEmptyVsZeroLength`。
7. 损坏检测：`dec/dec.go` 各 `fail` 分支产生 12 个可区分哨兵错误，
   均包装在带偏移的 `dec.Error`；`dec.TestCorruption`（9 类逐一断言
   `errors.Is` 与偏移合法）。
8. 炸弹防护：`sLitLen`/`sRefLen` 在写出任何字节前比较
   `total+v > max` 并进入终态（`d.err` 粘滞）；`dec.TestBomb`
   （长度 2^40 提前拒绝、输出保留 1 字节、后续写入返回同一错误）。
9. 并行压缩：`enc.CompressParallel` 每块独立 Matcher+窗口，仅依赖
   本块与上一块字典，结果按下标拼接；`enc.TestParallel`
   （workers 1/2/4/8 逐字节相同、30 次重复确定、流式可解，
   `-race` 干净）+ `dec.TestConcurrent`。

## 复杂度实测（`match.TestComplexity` / `enc.TestCompressBound`）

| 输入 | 候选考察总数 | 每字节考察数 |
| --- | --- | --- |
| 全同 64 KB | 16 | 2.44e-04 |
| 全同 4 MB | 1024 | 2.44e-04 |
| 随机 64 KB | 3 | 4.58e-05 |
| 随机 4 MB | 65 | 1.55e-05 |

上限均为链长 128 × 输入字节数；全同两档每字节考察数相同（比值 1.0，
不随规模增长）。全同 4 MB 压缩后 4118 字节 < 64 KB（长回指生效，
匹配长度上限 `match.MaxMatch` = 4096）。

## 故障注入

- 截断遍历：`dec.TestTruncation` 对每个截断点断言 `ErrTruncated`
  且输出为原文前缀。
- 翻转遍历：`dec.TestBitFlip` 对每字节 8 位翻转断言不 panic、
  不越输出上限、不静默出错内容（错误或校验和拦下）。
- 非法配置：`match.TestBadConfig`、`enc.TestBadConfig`、
  `dec.TestBadConfig`（窗口 0 / 链长 0 / 块长 0 / workers 0）。
