# AUDIT — 语义保证与测试对照

## 第二节十条语义

1. **往返 + 行尾保留**：`lines.Split` 保留原行尾（lines/lines.go:10），
   渲染对无 `\n` 行补 `\ No newline` 标记（udiff/udiff.go Render），
   解析还原标记（同文件 Parse）。测试：`udiff.TestRoundTrip`（含 CRLF、
   无末尾换行、空文件、随机 200 组正反向）。
2. **最短**：Myers O(ND)（edit/edit.go Diff），测试 `edit.TestShortest`
   用 O(NM) DP 对照 400 组随机输入，并重放脚本验证得到 b。
3. **确定性**：Myers 贪心 + 固定并列规则（V 相等时优先删除，
   edit/edit.go:47）。测试：`udiff.TestDeterministic` 重复渲染 100 次逐字节相同。
4. **零计数行号**：`hunk.Group` 计数为 0 时行号减一（hunk/hunk.go），
   规则推导见 DESIGN.md 第 1 节。测试：`udiff.TestZeroCountHeaders` 三个样例。
5. **hunk 合并**：`hunk.Group` 中保留行间隔 ≤ 2C 时同簇（hunk/hunk.go），
   推导见 DESIGN.md 第 2 节。测试：`udiff.TestMergeThreshold` 覆盖 g=2C 与 g=2C+1。
6. **无换行标记**：Render 对无行尾行输出标记（udiff/udiff.go），
   仅末尾换行变化也产生非空补丁。测试：`udiff.TestNoNewline`。
7. **偏移应用**：`patch.find` 在 center±F 内按距离升序、同距取前查找
   （patch/patch.go find）；后续 hunk 平移规则见 DESIGN.md 第 4 节。
   测试：`patch.TestOffset`（含最近选择与同距取前）。
8. **原子拒绝**：`patch.apply` 先定位全部 hunk 再生成输出，任一失败即返回
   `*HunkError`（带序号与类别），输入不被修改；Store 失败零变化。
   测试：`patch.TestAtomic`。
9. **解析严格性**：`udiff.Parse` 计数恰好一致，非法行首、多余/不足行均报
   `LineError`（带补丁行号）；单空格上下文行合法。测试：`udiff.TestParseStrict`。
10. **多文档并发**：`patch.Store` 在互斥锁内基于一致快照应用并记提交日志
    （patch/patch.go Store.Apply）。测试：`patch.TestStoreConcurrent`
    （16 goroutine，成功+失败=16，版本=成功数，终态=按日志串行重放，-race 干净）。

## 第四节复杂度实测（edit.TestCounter 输出）

| 规模 N=M | 编辑距离 D | 计数器实测 | 上界 4(N+M)(D+1) |
|----------|-----------|-----------|------------------|
| 1000     | 6         | 1022      | 56000            |
| 100000   | 6         | 100022    | 5600000          |

比值 100022/1022 ≈ 98 ≤ 150，近似线性。上限错误 `edit.ErrTooLarge` 由
`edit.TestMaxDist` 钉住（超限时计数器同样满足上界）。

## 第五节故障注入与上限

- 全截断点循环不 panic、不部分生效：`patch.TestTruncation`。
- 逐行字符翻转（`-`→`+`、数字改一）被拒绝且原文不变：`patch.TestFlip`。
- 字节数/hunk 数上限：`udiff.Limits`（Parse 立即拒绝），`patch.TestLimits`。
- 四类错误可区分：`udiff.ErrFormat` / `patch.ErrContext` / `patch.ErrOffset` /
  `edit.ErrTooLarge`，测试 `patch.TestErrorKinds`。
