# AUDIT — 语义逐条核对

1. 往返/行尾保留：`lines.Split/Join` 原样保留行尾（lines/lines.go），`udiff.Render` 对无
   `\n` 结尾的行写 `\ No newline` 标记；测试 `udiff_test.TestRoundTrip`（含 CRLF、无末尾
   换行、仅末尾换行变化、100 组随机对，正向+反向逐字节相等）。
2. 最短：`edit.Diff` 为 Myers O(ND) 最短脚本；测试 `edit_test.TestShortest` 用 O(NM)
   DP 求 LCS 对照 300 组随机输入，并验证脚本作用后等于 b。
3. 确定性：Myers 并列时固定取插入移动（edit/edit.go `v[off+k-1]+1 > v[off+k+1]` 才删），
   效果为 hunk 内删除优先（DESIGN.md 第 3 节）；测试 `udiff_test.TestDeterministic`
   重复生成 100 次逐字节相同。
4. 零计数行号：`hunk.Build` 计数为 0 时 start 取前一行行号（hunk/hunk.go 末尾 `--`），
   `udiff.rng` 在计数为 1 时省略 `,1`；测试 `udiff_test.TestZeroCountHeaders` 钉住
   `-0,0 +1`、`-2,0 +3`、`-1 +0,0` 三个样例。
5. 合并阈值：`hunk.Build` 中 `gap > 2*ctx` 才断开（DESIGN.md 第 2 节推导）；测试
   `udiff_test.TestMergeThreshold` 覆盖 g=2C（合并）与 g=2C+1（拆分），C∈{0,1,3}。
6. 无换行标记：`udiff.Render` 对末行无 `\n` 的行追加标记，`Parse` 识别并剥掉 `\n`；
   测试 `TestRoundTrip` 的 `a\nb`↔`a\nb\n` 用例（仅末尾换行变化产生非空补丁并还原）。
7. 偏移应用：`patch.apply` 在 guess±fuzz 内精确匹配，候选取 |j-guess| 最小、并列取靠前
   （严格 `<` 不替换），后续 hunk 起点随累计 delta 平移；测试 `patch_test.TestOffset`
   （shifted/nearest/tie-first 三例）。
8. 原子拒绝：`patch.apply` 任一 hunk 失败即返回 `hunk N: %w`，结果 nil、输入不变；
   测试 `patch_test.TestAtomicReject`（第 2 个 hunk 失败，错误含 "hunk 2"）。
9. 解析严格：`udiff.Parse` 校验头部、行首字符、声明计数与实际恰好一致（多/少行均报错），
   错误带补丁行号；单空格上下文行合法；测试 `udiff_test.TestParseStrict` 12 例表驱动。
10. 并发应用：`patch.Store` 在互斥锁内于一致快照上应用、成功才替换并版本+1、记提交日志；
    测试 `patch_test.TestConcurrent`：16 goroutine、成功+失败=16、版本=成功数、
    最终文本=按 `Store.Log` 串行重放；`go test -race` 干净。

## 故障注入与限额（第五节）

- 逐字节截断：`udiff_test.TestTruncation` 对合法补丁每个前缀解析/应用，无 panic、无部分生效。
- 逐行翻转：`udiff_test.TestFlip` 对每行做 `-`→`+` 与数字+1，均被解析或应用拒绝。
- 限额：`patch.Store.Apply` 检查 MaxBytes/MaxHunks，超限返回 `ErrLimit` 且状态零变化；
  测试 `patch_test.TestLimits`。
- 四类错误可区分：`udiff.ErrFormat` / `patch.ErrContext` / `patch.ErrOffset` /
  `edit.ErrTooLarge`；测试 `patch_test.TestErrorClasses`。

## 计数器实测（第四节）

- n=1000（改 3 处）：steps=1022，上界 4(N+M)(D+1)≈56000。
- n=100000（改 3 处）：steps=100022，上界≈5.6×10^6；与上档比值 ≈98 ≤ 150，近似线性。
- 测试 `edit_test.TestStepsBound`；距离上限路径 `edit_test.TestMaxDist`。
