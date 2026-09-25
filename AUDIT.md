# AUDIT

## 语义逐条核对（题面第二节）

1. **往返与行尾保留**：`lines.Split` 保留 `\n`/`\r\n`/无行尾（lines/lines.go），
   `udiff.Render`/`Parse` 处理 `\ No newline` 标记，`patch.Apply`/`Reverse` 往返。
   测试：`TestRoundTrip`（含 crlf、no-eol、eol-only、empty 用例，udiff/udiff_test.go）。
2. **最短**：`edit.Diff` 用 Myers O(ND)（edit/edit.go）。测试：`TestMinimal`
   对照 O(NM) LCS 动态规划（edit/edit_test.go）。
3. **确定性**：Myers 平局固定删除优先（DESIGN.md §3）。测试：`TestDeterminism`
   100 次渲染逐字节一致（udiff/udiff_test.go）。
4. **零计数行号**：`udiff.rangeStr`/`Parse` 的 idx 换算（udiff/udiff.go）。
   测试：`TestZeroCountHeaders` 三个样例（udiff/udiff_test.go）。
5. **合并阈值 2C**：`hunk.Build` 分组条件 `gap <= 2*ctx`（hunk/hunk.go）。
   测试：`TestMergeThreshold` 覆盖 g=2C 与 g=2C+1（udiff/udiff_test.go）。
6. **无换行标记**：`udiff.Render` 对无 `\n` 结尾的行写标记，`Parse` 剥掉行尾。
   测试：`TestRoundTrip` 的 no-eol/eol-only 用例（非空补丁且可应用）。
7. **偏移应用**：`patch.locate` 在 ±F 内精确匹配、最近优先、同距取前
   （patch/patch.go）。测试：`TestOffset`、`TestOffsetNearest`（patch/patch_test.go）。
8. **原子拒绝**：`patch.Apply` 全部 hunk 成功才返回结果，错误为 `hunk N: %w`。
   测试：`TestAtomic`（patch/patch_test.go）。
9. **解析严格**：`udiff.Parse` 校验计数恰合、行首字符、错误带行号。
   测试：`TestParseStrict`（udiff/udiff_test.go）。
10. **并发应用**：`patch.Store` 在互斥锁内判断、替换、版本+1、记提交日志。
    测试：`TestConcurrent`（patch/patch_test.go，`-race` 干净）。

## 计数器实测（题面第四节）

| 规模 N=M | D | 步数实测 | 上界 4(N+M)(D+1) |
|----------|---|----------|-------------------|
| 1000     | 6 | 1022     | 56000             |
| 100000   | 6 | 100022   | 5600000           |

比值 100022/1022 ≈ 97.9 ≤ 150（近似线性）。测试：`TestStepsBound`；
超限路径 `TestMaxDist`（返回 `edit.ErrTooBig`，计数器仍满足上界）。
