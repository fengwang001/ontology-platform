# AUDIT — 语义保证与测试对照

## 第二节十条语义

1. **往返/行尾保留**：`lines.Split` 保留 `\n`/`\r\n`/无行尾，`Join` 原样拼回；
   `patch.Apply`/`Reverse` 只做整行替换。测试：`udiff_test.TestRoundtrip`（含 CRLF、
   无末尾换行、空文件表项 + 100 组随机）。
2. **最短**：`edit.Diff` 为 Myers O(ND)，D 即最短距离。测试：`edit_test.TestMinimal`
   用 O(NM) DP 求 LCS 对照 300 组随机输入。
3. **确定性**：`edit.Diff` 前进时 `V[k-1] < V[k+1]` 才选插入，并列归删除（见
   DESIGN.md 第 3 节）。测试：`edit_test.TestDeterministic`（100 次重复 + 并列样例）。
4. **零计数行号**：`hunk.Build` 中 `start = 之前行数 + (count>0 ? 1 : 0)`。
   测试：`udiff_test.TestZeroCountHeaders`（题面三个样例）。
5. **合并阈值 2C**：`hunk.Build` 中 `gap > 2*ctx` 才拆分。测试：
   `udiff_test.TestMergeThreshold`（g=2C 与 g=2C+1 两侧，C∈{0,1,3}）。
6. **无换行标记**：`udiff.Render` 对无 `\n` 结尾的行追加 `\ No newline at end of
   file`，`Parse` 消费并剥回。测试：`udiff_test.TestNoNewlineOnly` + 往返表。
7. **偏移应用**：`patch.locate` 在记录位置 ±F 内按距离升序、同距取下标较小者；
   后续 hunk 起点随上一 hunk 偏移平移。测试：`patch_test.TestOffset`（最近者、
   同距取前、漂移传播）。
8. **原子拒绝**：`patch.Apply` 先全程算好再返回，失败返回 (nil, err)；
   `Store.Apply` 失败零写入。测试：`patch_test.TestAtomic`（文本、版本、日志均不变）。
9. **解析严格**：`udiff.Parse` 校验头声明计数与实际行数恰好一致、行首字符合法、
   标记位置合法，错误均带补丁行号。测试：`udiff_test.TestParseStrict`（8 个坏例 +
   空上下文行好例）。
10. **多文档并发**：`patch.Store` 单互斥锁串行化应用，成功才换文本、版本+1、
    追加提交日志。测试：`patch_test.TestStoreConcurrent`（N=16，`-race`，
    成功+失败==N、版本==成功数、终态==按日志串行重放）。

## 第四节复杂度（实测）

`edit` 内非导出计数器 `steps`（`Steps()` 读取），对角线前进一步与 snake
逐行比较各计 1。实测（b 为 a 改 3 行，D=6）：

| N=M | steps | 上界 4(N+M)(D+1) | 与上一档比值（限 150） |
|---|---|---|---|
| 1000 | 1022 | 56000 | — |
| 100000 | 100022 | 5600000 | 97.9 |

测试：`edit_test.TestStepsScale`（两档上界 + 比值）、`edit_test.TestLimit`
（超限返回 `edit.ErrTooBig` 且计数仍满足上界）。

## 第五节故障注入与上限

- 逐字节截断 + 逐行字符翻转：`patch_test.TestTruncateAndFlip`，不 panic、
  应用成功者必可逆还原、被拒者状态零变化。
- 字节数/hunk 数上限：`udiff.Limits`，超限返回 `udiff.ErrLimit`。
  测试：`patch_test.TestLimits`。
- 四类错误可区分：`udiff.ErrFormat` / `patch.ErrContext` / `patch.ErrOffset` /
  `edit.ErrTooBig`，均可用 `errors.Is` 判定。测试：`patch_test.TestErrorClasses`。
