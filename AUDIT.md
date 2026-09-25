# AUDIT — 语义与复杂度核对

## 语义逐条（保证位置 / 钉住它的测试）

| # | 语义 | 代码位置 | 测试 |
|---|---|---|---|
| 1 | 往返逐字节相等 | `enc.process` + `dec.step` | `TestRoundtrip` |
| 2 | 重叠回指逐字节向前复制 | `dec.onDist` 复制循环（`window.Get` O(1)） | `TestOverlapBackref`（dist 1/2/3，长度远大于距离） |
| 3 | 与 Write 切法无关 | `enc.process` 终局规则（匹配不过界、字面量挂起） | `TestRoundtrip`（1/7/整段切法输出相同） |
| 4 | Flush 承诺 + 空 Flush 零字节 | `enc.Flush`（强制截断 + 标记；`pending` 门控） | `TestFlushPromise` |
| 5 | 解压与切法无关 | `dec.step` 逐字节状态机，每字节只消费一次 | `TestDecChunking`（遍历全部切分点） |
| 6 | 空流合法非空；零长/缺尾判截断 | `enc.Close` 总写流尾；`dec.Close` 要求 stDone | `TestEmptyVsZeroLength` |
| 7 | 八类损坏错误可区分且带偏移 | `dec` 包哨兵错误 + `Write` 包装 `at offset` | `TestCorruption`（九行表） |
| 8 | 炸弹在写出前拒绝、终态保留输出 | `dec.onDist`/`onTag` 先比上限再复制；`d.err` 粘性 | `TestOutputLimit`（len=2^40） |
| 9 | 并行块压缩确定且可流式解开 | `enc.CompressParallel`（字典=上一块尾部，按序拼接） | `TestParallel`（workers 1/2/4/8 + 30 次重复） |

故障注入：截断遍历 `TestTruncationWalk`（全部前缀判 ErrTruncated 且输出为原文前缀）；
翻转遍历 `TestBitFlipWalk`（任意单比特翻转：报错或校验和拦下，不 panic、不越上限、不静默错）。
并发：`go test -race` 干净（`TestParallel`/`TestConcurrentDecoders`）；非法配置 `TestBadConfig`。

## 复杂度实测（`TestComplexity`，链长上限 32）

| 输入 | 候选考察总数 | 每字节考察数 | 压缩后大小 |
|---|---|---|---|
| 全同 64 KB | 16 | 0.000244 | — |
| 全同 4 MB | 1024 | 0.000244 | 5137 B（< 64 KB，证明用了长回指） |
| 随机 64 KB / 4 MB | <= 32 x n | 远低于上限 | — |

两档每字节考察数相同（不随规模增长）；所有输入候选总数 <= 32 x 输入字节数。
