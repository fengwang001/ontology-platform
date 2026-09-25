# AUDIT — 语义保证与测试对照

## 第二节十条语义

1. **往返/反向/行尾保留**：`lines.Split` 保留 `\n`/`\r\n`/无行尾，`Join` 原样
   拼回；`udiff.Render` 对无行尾行写 `\ No newline` 标记，`Parse` 还原；
   `patch.Apply`/`Reverse` 按字节精确匹配与替换。测试：`udiff.TestRoundtrip`
   （随机含 CRLF 与无末尾换行）、`edit.TestSplitJoin`。
2. **最短**：`edit.Diff` 为 Myers O(ND)，删除+插入数 = 最小编辑距离。测试：
   `edit.TestShortestAndValid`（300 组随机输入对照 O(NM) DP，并验证脚本可重放）。
3. **确定性**：`edit.Diff` 在 `V[k-1] < V[k+1]` 并列时走删除分支（DESIGN.md
   第 3 节）。测试：`edit.TestDeterministic`（100 次逐 op 相同）。
4. **零计数行号**：`hunk.Group` 在计数为 0 时写前一行行号，`udiff.Parse`/
   `patch.apply` 按 count==0 → 下标=起始字段解释。测试：`udiff.TestZeroCountHeads`
   （题面三个样例）。
5. **合并阈值 2C**：`hunk.Group` 中 `k-j > 2*ctx` 才拆分。测试：
   `udiff.TestMergeThreshold`（C∈{0,1,3}，g=2C 与 g=2C+1 两侧）。
6. **无换行标记**：`udiff.Render` 写标记、`Parse` 识别并剥掉行尾 `\n`。
   测试：`udiff.TestNoNewlineMark`（仅末尾换行变化也产生非空补丁并可双向应用）。
7. **偏移应用**：`patch.locate` 在 ±fuzz 内精确匹配，取最近、平手取靠前。
   测试：`patch.TestOffsetNearest`（含平手情形）。
8. **原子拒绝**：`patch.apply` 先全程在副本上构造，任一 hunk 失败直接返回
   错误，错误含 hunk 序号与类别；`Store.Apply` 失败零变化。测试：
   `patch.TestAtomic`。
9. **解析严格性**：`udiff.Parse` 逐行校验行首字符、声明计数恰好耗尽、每行
   以 `\n` 结尾，错误带补丁内行号。测试：`udiff.TestParseStrict`（含空上下文行）。
10. **多文档并发**：`patch.Store` 互斥锁内快照→应用→原子替换→版本+1→提交
    日志。测试：`patch.TestConcurrent`（16 goroutine，成功+失败=N、版本=
    成功数、终态=按日志串行重放；`go test -race` 干净）。

## 第四节复杂度实测

计数器：`edit.go` 中非导出变量 `steps`（`Steps()` 读取），记录对角线前进步数
（含 snake 逐行比较）。测试：`edit.TestStepBound`、`edit.TestMaxDist`（超上限
立即返回 `edit.ErrTooBig`，计数同样满足上界）。

| 规模 N=M | 编辑距离 D | 实测步数 | 上界 4(N+M)(D+1) |
|----------|-----------|----------|------------------|
| 1000     | 6         | 1022     | 56000            |
| 100000   | 6         | 100022   | 5600000          |

放大倍数 ≈ 97.8 ≤ 150，近似线性。

## 第五节故障注入与第六节并发
- 截断：`udiff.TestTruncation`（每个字节位置，无 panic，错误类别可判定）。
- 翻转：`udiff.TestFlip`（逐行变异，均被解析或应用拒绝，原文不变）。
- 资源上限：`udiff.TestLimits`（MaxBytes/MaxHunks → `udiff.ErrLimit`）。
- 四类错误区分：`patch.TestErrorClasses`（`edit.ErrTooBig`/`udiff.ErrFormat`/
  `patch.ErrContext`/`patch.ErrRange`，均可用 `errors.Is` 判定）。
- 并发：`patch.TestConcurrent`，`-race` 干净，无 sleep（chan 屏障 + WaitGroup）。
